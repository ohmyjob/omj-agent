package agent

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/ohmyjob/omj-agent/internal/discovery"
	"github.com/ohmyjob/omj-agent/internal/protocol"
)

// fakeCollector counts its calls so a test can prove a discovery was answered
// once rather than on every poll.
type fakeCollector struct {
	result discovery.Result
	err    error

	mu    sync.Mutex
	calls int

	release chan struct{}
}

func (c *fakeCollector) Collect(ctx context.Context) (discovery.Result, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()

	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return discovery.Result{}, ctx.Err()
		}
	}

	return c.result, c.err
}

func (c *fakeCollector) called() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.calls
}

func sampleResult() discovery.Result {
	return discovery.Result{
		Entries: []discovery.Entry{
			{
				Source:       "crontab:root",
				Raw:          "0 3 * * * /usr/local/bin/backup.sh",
				Schedule:     "0 3 * * *",
				ScheduleKind: discovery.KindCron,
				Timezone:     "UTC",
				Command:      "/usr/local/bin/backup.sh",
				User:         "root",
			},
			{
				Source:      "crontab:ian",
				Raw:         "@reboot /home/ian/warm-cache.sh",
				User:        "ian",
				Unparseable: true,
				Note:        "@reboot has no time of day, so Oh My Job cannot schedule it.",
			},
		},
		Unreadable:     []discovery.Unreadable{{Source: "crontab:deploy", Reason: "permission denied"}},
		OmittedEntries: 0,
	}
}

func TestAskedForADiscoveryTheAgentPostsOne(t *testing.T) {
	collector := &fakeCollector{result: sampleResult()}
	h := newHarness(t, harnessOptions{stopAfter: 3, discoverer: collector})
	h.server.AskForDiscovery()

	h.run(t)
	h.agent.Wait()

	reported := h.server.Discoveries()
	if len(reported) != 1 {
		t.Fatalf("discoveries = %d, want 1", len(reported))
	}

	got := reported[0]

	if len(got.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(got.Entries))
	}

	first := got.Entries[0]
	if first.Source != "crontab:root" || first.RunAs == nil || *first.RunAs != "root" {
		t.Errorf("first entry = %+v, want crontab:root running as root", first)
	}

	if first.ScheduleKind == nil || *first.ScheduleKind != "cron" {
		t.Errorf("schedule_kind = %v, want cron", first.ScheduleKind)
	}

	// A source the collector could not read is named rather than passed over.
	if len(got.UnreadableSources) != 1 || got.UnreadableSources[0].Source != "crontab:deploy" {
		t.Errorf("unreadable_sources = %+v, want crontab:deploy", got.UnreadableSources)
	}

	// The unparseable entry keeps its raw text and states no schedule.
	second := got.Entries[1]
	if !second.Unparseable || second.Raw != "@reboot /home/ian/warm-cache.sh" || second.Schedule != nil {
		t.Errorf("second entry = %+v, want unparseable with its raw text and no schedule", second)
	}
}

// The Server clears the request when the discovery arrives, so the fake stops
// asking; the Agent must not keep collecting on the polls in between either.
func TestADiscoveryIsAnsweredOnce(t *testing.T) {
	collector := &fakeCollector{result: sampleResult()}
	h := newHarness(t, harnessOptions{stopAfter: 6, discoverer: collector})
	h.server.AskForDiscovery()

	h.run(t)
	h.agent.Wait()

	if got := len(h.server.Discoveries()); got != 1 {
		t.Fatalf("discoveries = %d, want 1", got)
	}

	if got := collector.called(); got != 1 {
		t.Fatalf("collected %d times, want 1", got)
	}
}

func TestAServerThatNeverAsksGetsNoDiscovery(t *testing.T) {
	collector := &fakeCollector{result: sampleResult()}
	h := newHarness(t, harnessOptions{stopAfter: 3, discoverer: collector})

	h.run(t)
	h.agent.Wait()

	if got := len(h.server.Discoveries()); got != 0 {
		t.Fatalf("discoveries = %d, want 0", got)
	}

	if got := collector.called(); got != 0 {
		t.Fatalf("collected %d times, want 0", got)
	}
}

// A Server built before discovery existed answers 404 for ever, so the Agent
// asks its collector once and then leaves the endpoint alone.
func TestAServerWithoutDiscoveryIsNotAskedTwice(t *testing.T) {
	collector := &fakeCollector{result: sampleResult()}
	h := newHarness(t, harnessOptions{stopAfter: 6, discoverer: collector})
	h.server.AskForDiscovery()

	for range 6 {
		h.server.FailNext("discovery", http.StatusNotFound, protocol.ErrRunNotFound)
	}

	h.run(t)
	h.agent.Wait()

	if got := collector.called(); got != 1 {
		t.Fatalf("collected %d times, want 1", got)
	}

	if got := len(h.server.Discoveries()); got != 0 {
		t.Fatalf("discoveries = %d, want 0", got)
	}

	if got := h.logs.count("does not collect scheduled work"); got != 1 {
		t.Errorf("logged the missing endpoint %d times, want once", got)
	}
}

// Collecting reads every crontab and queries systemd, so it must not stand
// between the loop and the Runs it owes heartbeats to. The collector is held
// until several more work requests have gone out, which could not happen if
// the loop were waiting on it.
func TestCollectingDoesNotHoldUpTheLoop(t *testing.T) {
	collector := &fakeCollector{result: sampleResult(), release: make(chan struct{})}
	h := newHarness(t, harnessOptions{stopAfter: 8, discoverer: collector})
	h.server.AskForDiscovery()

	released := false
	h.server.OnWork(func(count int, _ protocol.WorkRequest) {
		if count >= 4 && !released {
			released = true
			close(collector.release)
		}

		if count >= 8 {
			h.cancel()
		}
	})

	h.run(t)
	h.agent.Wait()

	if got := len(h.server.WorkRequests()); got < 4 {
		t.Fatalf("work requests = %d, want the loop to have kept polling while collecting", got)
	}

	if got := len(h.server.Discoveries()); got != 1 {
		t.Fatalf("discoveries = %d, want 1", got)
	}
}

func TestACollectorThatFailsReportsNothing(t *testing.T) {
	collector := &fakeCollector{err: errors.New("systemctl is not installed")}
	h := newHarness(t, harnessOptions{stopAfter: 3, discoverer: collector})
	h.server.AskForDiscovery()

	h.run(t)
	h.agent.Wait()

	if got := len(h.server.Discoveries()); got != 0 {
		t.Fatalf("discoveries = %d, want 0", got)
	}

	if h.logs.count("scheduled work could not be read") == 0 {
		t.Error("the logs do not say the collection failed")
	}
}

func TestDiscoveryPayload(t *testing.T) {
	tests := []struct {
		name   string
		result discovery.Result
		assert func(t *testing.T, got protocol.DiscoveryRequest)
	}{
		{
			name:   "an omitted entry marks the report truncated",
			result: discovery.Result{OmittedEntries: 3},
			assert: func(t *testing.T, got protocol.DiscoveryRequest) {
				if !got.Truncated || got.OmittedEntries != 3 {
					t.Errorf("truncated = %v, omitted = %d; want true, 3", got.Truncated, got.OmittedEntries)
				}
			},
		},
		{
			name:   "a complete report is not truncated",
			result: discovery.Result{Entries: []discovery.Entry{{Source: "crontab:root", Raw: "@daily x"}}},
			assert: func(t *testing.T, got protocol.DiscoveryRequest) {
				if got.Truncated || got.OmittedEntries != 0 {
					t.Errorf("truncated = %v, omitted = %d; want false, 0", got.Truncated, got.OmittedEntries)
				}
			},
		},
		{
			name:   "a field the source did not state is null rather than empty",
			result: discovery.Result{Entries: []discovery.Entry{{Source: "crontab:root", Raw: "x"}}},
			assert: func(t *testing.T, got protocol.DiscoveryRequest) {
				e := got.Entries[0]
				if e.Schedule != nil || e.ScheduleKind != nil || e.Timezone != nil || e.Command != nil || e.RunAs != nil || e.Unit != nil || e.Note != nil {
					t.Errorf("entry = %+v, want every unstated field null", e)
				}
			},
		},
		{
			name:   "empty lists encode as lists rather than null",
			result: discovery.Result{},
			assert: func(t *testing.T, got protocol.DiscoveryRequest) {
				if got.Entries == nil || got.UnreadableSources == nil {
					t.Errorf("entries = %v, unreadable = %v; want empty lists", got.Entries, got.UnreadableSources)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.assert(t, DiscoveryPayload(tt.result))
		})
	}
}
