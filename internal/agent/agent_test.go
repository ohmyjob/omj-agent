package agent

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/client"
	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/output"
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/runner"
	"github.com/ohmyjob/omj-agent/internal/state"
	"github.com/ohmyjob/omj-agent/internal/sysinfo"
)

const (
	runA = "5b1e2c7a-8d4f-4c3b-9a2e-7f6d5c4b3a21"
	runB = "6c2f3d8b-9e5a-4d4c-8b3f-8a7e6d5c4b32"
)

type fakeSleeper struct {
	mu     sync.Mutex
	delays []time.Duration
}

func (s *fakeSleeper) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.delays = append(s.delays, d)
	s.mu.Unlock()

	return ctx.Err()
}

func (s *fakeSleeper) recorded() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]time.Duration(nil), s.delays...)
}

type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.Write(p)
}

func (l *logCapture) count(substr string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return strings.Count(l.buf.String(), substr)
}

// fakeTicker hands out one channel per interval and lets a test tick it by
// hand; a tick that nobody is waiting for is dropped rather than queued.
type fakeTicker struct {
	mu       sync.Mutex
	channels map[time.Duration]chan time.Time
}

func newFakeTicker() *fakeTicker {
	return &fakeTicker{channels: map[time.Duration]chan time.Time{}}
}

func (f *fakeTicker) factory(interval time.Duration) (<-chan time.Time, func()) {
	return f.channel(interval), func() {}
}

func (f *fakeTicker) channel(interval time.Duration) chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	ch, ok := f.channels[interval]
	if !ok {
		ch = make(chan time.Time)
		f.channels[interval] = ch
	}

	return ch
}

func (f *fakeTicker) tickUntil(t *testing.T, interval time.Duration, done func() bool) {
	t.Helper()

	ch := f.channel(interval)
	deadline := time.After(10 * time.Second)

	for !done() {
		select {
		case ch <- time.Time{}:
		case <-time.After(5 * time.Millisecond):
		case <-deadline:
			t.Fatalf("condition not met within 10s while ticking every %s", interval)
		}
	}
}

// recordingReporter remembers what the loop hands over, then behaves like
// the default reporter so processes are reaped and slots released.
type recordingReporter struct {
	inner  Reporter
	buffer *output.Buffer

	mu     sync.Mutex
	runs   []*Run
	stderr map[string]string
}

func (r *recordingReporter) Report(ctx context.Context, run *Run) {
	var text strings.Builder

	for _, chunk := range r.buffer.NextBatch(run.Lease.RunID) {
		text.Write(chunk.Data)
	}

	r.mu.Lock()
	r.runs = append(r.runs, run)
	r.stderr[run.Lease.RunID] = text.String()
	r.mu.Unlock()

	r.inner.Report(ctx, run)
}

func (r *recordingReporter) reported() []*Run {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]*Run(nil), r.runs...)
}

func (r *recordingReporter) stderrOf(runID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.stderr[runID]
}

type recordingResender struct {
	mu       sync.Mutex
	outcomes []state.RecentRun
}

func (r *recordingResender) Resend(_ context.Context, _ protocol.RunLease, outcome state.RecentRun) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.outcomes = append(r.outcomes, outcome)

	return nil
}

func (r *recordingResender) resent() []state.RecentRun {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]state.RecentRun(nil), r.outcomes...)
}

type harness struct {
	server   *fakeServer
	agent    *Agent
	sleeper  *fakeSleeper
	logs     *logCapture
	store    *state.Store
	buffer   *output.Buffer
	reporter *recordingReporter
	resender *recordingResender
	ticker   *fakeTicker
	signals  chan os.Signal
	now      time.Time
	ctx      context.Context
	cancel   context.CancelFunc
}

type harnessOptions struct {
	maxConcurrent int
	stopAfter     int
	batchChunks   int
	realResender  bool
	runAsAllowed  []string
	stopBudget    time.Duration
	before        func(h *harness)
}

// newHarness wires an Agent to a fake Server. The loop is stopped by
// cancelling the context when the stopAfter-th work request arrives, so that
// request is recorded but its response is never processed.
func newHarness(t *testing.T, opts harnessOptions) *harness {
	t.Helper()

	server := newFakeServer(t)

	credential, err := config.NewCredential(server.Secret)
	if err != nil {
		t.Fatal(err)
	}

	c, err := client.New(client.Options{ServerURL: server.URL(), Credential: credential, InsecureHTTP: true})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

	store, err := state.Loader{Now: func() time.Time { return now }}.Load(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.ServerURL = server.URL()
	cfg.InsecureHTTP = true
	cfg.MachineID = server.MachineID

	if opts.maxConcurrent > 0 {
		cfg.MaxConcurrentRuns = opts.maxConcurrent
	}

	h := &harness{
		server:   server,
		sleeper:  &fakeSleeper{},
		logs:     &logCapture{},
		store:    store,
		buffer:   output.NewBuffer(output.BufferOptions{BatchChunks: opts.batchChunks}),
		resender: &recordingResender{},
		ticker:   newFakeTicker(),
		signals:  make(chan os.Signal, 2),
		now:      now,
	}
	h.reporter = &recordingReporter{buffer: h.buffer, stderr: map[string]string{}}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	t.Cleanup(h.cancel)

	if opts.before != nil {
		opts.before(h)
	}

	var resender Resender = h.resender
	if opts.realResender {
		resender = nil
	}

	h.agent, err = New(Options{
		Config:       cfg,
		RunAsAllowed: opts.runAsAllowed,

		Client:     c,
		Info:       sysinfo.Info{Hostname: "nas01", OS: "linux", Arch: "arm64", ReportedIPs: []string{"192.168.1.20"}, AgentUser: "ohmyjob", AgentUID: 1000},
		State:      store,
		Runner:     runner.Runner{Grace: 200 * time.Millisecond},
		Buffer:     h.buffer,
		Reporter:   h.reporter,
		Resender:   resender,
		Logger:     slog.New(slog.NewTextHandler(h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Now:        func() time.Time { return now },
		Backoff:    &client.Backoff{Rand: func() float64 { return 0.5 }},
		Sleep:      h.sleeper.sleep,
		Ticker:     h.ticker.factory,
		Signals:    h.signals,
		StopBudget: opts.stopBudget,
	})
	if err != nil {
		t.Fatal(err)
	}

	h.reporter.inner = reporter{agent: h.agent}

	if opts.stopAfter > 0 {
		server.OnWork(func(count int, _ protocol.WorkRequest) {
			if count >= opts.stopAfter {
				h.cancel()
			}
		})
	}

	return h
}

func (h *harness) lease(runID, command string) protocol.RunLease {
	return protocol.RunLease{
		RunID:          runID,
		MachineID:      h.server.MachineID,
		JobID:          "9c2d6b7e-1f3a-4d5b-8e6f-2a1b3c4d5e6f",
		JobName:        "Nightly backup",
		Trigger:        protocol.TriggerManual,
		Command:        command,
		Environment:    map[string]string{},
		TimeoutSeconds: 60,
		MaxOutputBytes: 1 << 20,
		LeaseExpiresAt: h.now.Add(time.Minute),
	}
}

func (h *harness) run(t *testing.T) {
	t.Helper()

	if err := h.agent.Run(h.ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	h.agent.Wait()
}

func TestNewRequiresAnEnrolledMachine(t *testing.T) {
	_, err := New(Options{Config: config.Default(), Client: &client.Client{}, State: &state.Store{}, Buffer: output.NewBuffer(output.BufferOptions{})})
	if err == nil || !strings.Contains(err.Error(), "machine_id") {
		t.Fatalf("New() error = %v, want one naming machine_id", err)
	}
}

func TestForeignLeaseIsNeverStartedAndLoggedOnce(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 3})

	foreign := h.lease(runA, "true")
	foreign.MachineID = "11111111-2222-4333-8444-555555555555"
	h.server.Enqueue(foreign)
	h.server.Enqueue(foreign)

	h.run(t)

	if starts := h.server.Starts(); len(starts) != 0 {
		t.Fatalf("starts = %v, want none", starts)
	}

	if got := h.logs.count("lease ignored"); got != 1 {
		t.Fatalf("lease ignored logged %d times, want 1", got)
	}

	if got := h.logs.count("not this one"); got != 1 {
		t.Fatalf("reason logged %d times, want 1", got)
	}
}

func TestInvalidLeasesAreIgnored(t *testing.T) {
	tests := []struct {
		name   string
		change func(l *protocol.RunLease)
		reason string
	}{
		{name: "expired", change: func(l *protocol.RunLease) { l.LeaseExpiresAt = l.LeaseExpiresAt.Add(-2 * time.Minute) }, reason: "lease expired at"},
		{name: "no command", change: func(l *protocol.RunLease) { l.Command = "" }, reason: "has no command"},
		{name: "no timeout", change: func(l *protocol.RunLease) { l.TimeoutSeconds = 0 }, reason: "is not positive"},
		{name: "no output limit", change: func(l *protocol.RunLease) { l.MaxOutputBytes = 0 }, reason: "is not positive"},
		{name: "no run id", change: func(l *protocol.RunLease) { l.RunID = "" }, reason: "has no run id"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{stopAfter: 2})

			lease := h.lease(runA, "true")
			tt.change(&lease)
			h.server.Enqueue(lease)

			h.run(t)

			if starts := h.server.Starts(); len(starts) != 0 {
				t.Fatalf("starts = %v, want none", starts)
			}

			if h.logs.count(tt.reason) != 1 {
				t.Fatalf("reason %q not logged once", tt.reason)
			}
		})
	}
}

func TestExpiryToleratesClockSkew(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2})

	lease := h.lease(runA, "true")
	lease.LeaseExpiresAt = h.now.Add(-3 * time.Second)
	h.server.Enqueue(lease)

	h.run(t)

	if starts := h.server.Starts(); len(starts) != 1 {
		t.Fatalf("starts = %v, want one", starts)
	}
}

func TestActiveLeaseIsAcknowledgedButNotExecutedTwice(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 4})

	h.server.Enqueue(h.lease(runA, "sleep 30"))
	h.server.Enqueue(h.lease(runA, "sleep 30"))
	h.server.OnWork(func(count int, _ protocol.WorkRequest) {
		if count == 3 {
			h.server.Cancel(runA)
		}

		if count >= 4 {
			h.cancel()
		}
	})

	h.run(t)

	if starts := h.server.Starts(); len(starts) != 2 {
		t.Fatalf("starts = %d, want 2 (one per lease)", len(starts))
	}

	reported := h.reporter.reported()
	if len(reported) != 1 {
		t.Fatalf("processes = %d, want 1", len(reported))
	}

	result := reported[0].Process.Wait()
	if !result.Cancelled || result.Reason != CancelRequested {
		t.Fatalf("result = %+v, want cancelled with reason %q", result, CancelRequested)
	}

	outcome, ok := h.store.RecentOutcome(runA)
	if !ok || outcome.Status != string(protocol.RunStatusCancelled) {
		t.Fatalf("recent outcome = %+v, %v; want cancelled", outcome, ok)
	}

	if h.store.IsActive(runA) {
		t.Fatal("run still active in the state file")
	}
}

func TestLeaseLimitsAreClamped(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2})

	lease := h.lease(runA, "true")
	lease.TimeoutSeconds = 999999
	lease.MaxOutputBytes = 1 << 40
	h.server.Enqueue(lease)

	h.run(t)

	starts := h.server.Starts()
	if len(starts) != 1 {
		t.Fatalf("starts = %d, want 1", len(starts))
	}

	want := protocol.StartRequest{EffectiveTimeoutSeconds: 259200, EffectiveMaxOutputBytes: 104857600}
	if starts[0].Request != want {
		t.Fatalf("start request = %+v, want %+v", starts[0].Request, want)
	}

	reported := h.reporter.reported()
	if len(reported) != 1 || reported[0].Timeout != 259200*time.Second || reported[0].MaxOutput != 104857600 {
		t.Fatalf("reported run = %+v, want the clamped limits", reported)
	}
}

func TestBackoffGrowsThenResets(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 6})

	for range 3 {
		h.server.FailNext("work", http.StatusInternalServerError, "")
	}

	// The hook runs before the request it observes is served, so a failure
	// queued at the fifth poll hits the fifth poll, after the fourth succeeded.
	h.server.OnWork(func(count int, _ protocol.WorkRequest) {
		if count == 5 {
			h.server.FailNext("work", http.StatusBadGateway, "")
		}

		if count >= 6 {
			h.cancel()
		}
	})

	h.run(t)

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, time.Second}
	got := h.sleeper.recorded()

	if len(got) != len(want) {
		t.Fatalf("sleeps = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sleep #%d = %s, want %s (all: %v)", i+1, got[i], want[i], got)
		}
	}
}

func TestThrottlingWaitsAtLeastRetryAfter(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2})

	h.server.OnWork(func(count int, _ protocol.WorkRequest) {
		if count >= 2 {
			h.cancel()
		}
	})

	server := h.server
	server.mu.Lock()
	server.failures["work"] = append(server.failures["work"], failure{status: http.StatusTooManyRequests, body: protocol.ErrorResponse{Error: protocol.ErrThrottled}})
	server.mu.Unlock()

	// The fake writes no Retry-After, so the backoff decides; a zero header
	// must not shorten the wait below it.
	h.run(t)

	if got := h.sleeper.recorded(); len(got) != 1 || got[0] != time.Second {
		t.Fatalf("sleeps = %v, want [1s]", got)
	}
}

func TestRejectionsRetryEveryFiveMinutes(t *testing.T) {
	tests := []struct {
		name    string
		reject  func(f *fakeServer)
		message string
	}{
		{name: "credential", reject: func(f *fakeServer) { f.RejectCredential() }, message: "run omj-agent enroll again"},
		{name: "protocol", reject: func(f *fakeServer) { f.RejectProtocol([]int{2}, "9.0.0") }, message: "supported_protocol_versions=[2]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{stopAfter: 2})
			tt.reject(h.server)

			h.run(t)

			got := h.sleeper.recorded()
			if len(got) == 0 || got[0] != client.AuthRetryInterval {
				t.Fatalf("sleeps = %v, want the first to be %s", got, client.AuthRetryInterval)
			}

			if h.logs.count(tt.message) == 0 {
				t.Fatalf("message %q not logged", tt.message)
			}
		})
	}
}

func TestSlotsAndActiveRunsAreReported(t *testing.T) {
	h := newHarness(t, harnessOptions{maxConcurrent: 2, stopAfter: 3})

	h.server.Enqueue(h.lease(runA, "sleep 30"))
	h.server.OnWork(func(count int, _ protocol.WorkRequest) {
		if count == 2 {
			h.server.Cancel(runA)
		}

		if count >= 3 {
			h.cancel()
		}
	})

	h.run(t)

	requests := h.server.WorkRequests()
	if len(requests) != 3 {
		t.Fatalf("work requests = %d, want 3", len(requests))
	}

	if requests[0].Slots != 2 || len(requests[0].ActiveRuns) != 0 {
		t.Fatalf("first request = %+v, want 2 slots and no active runs", requests[0])
	}

	if !strings.Contains(h.server.WorkBodies()[0], `"active_runs":[]`) {
		t.Fatalf("first body = %s, want an empty list, not null", h.server.WorkBodies()[0])
	}

	want := []protocol.ActiveRun{{RunID: runA, Status: protocol.ActiveRunRunning}}
	if requests[1].Slots != 1 || len(requests[1].ActiveRuns) != 1 || requests[1].ActiveRuns[0] != want[0] {
		t.Fatalf("second request = %+v, want 1 slot and %v", requests[1], want)
	}

	if requests[0].WaitSeconds != 25 || requests[0].Hostname != "nas01" || requests[0].ReportedIPs[0] != "192.168.1.20" {
		t.Fatalf("request metadata = %+v", requests[0])
	}
}

func TestTheExecutionUserAllowlistIsReported(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		want    []string
		body    string
	}{
		{name: "an allowlist rides on every poll", allowed: []string{"ohmyjob", "deploy"}, want: []string{"ohmyjob", "deploy"}, body: `"run_as_allowed":["ohmyjob","deploy"]`},
		{name: "no allowlist leaves the field out", want: nil, body: `"reported_ips":["192.168.1.20"]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{stopAfter: 2, runAsAllowed: tt.allowed})

			h.run(t)

			for i, request := range h.server.WorkRequests() {
				if !reflect.DeepEqual(request.RunAsAllowed, tt.want) {
					t.Errorf("request %d run_as_allowed = %#v, want %#v", i, request.RunAsAllowed, tt.want)
				}
			}

			if body := h.server.WorkBodies()[0]; !strings.Contains(body, tt.body) {
				t.Errorf("body = %s, want it to contain %s", body, tt.body)
			}

			if tt.allowed == nil && strings.Contains(h.server.WorkBodies()[0], "run_as_allowed") {
				t.Errorf("body = %s, want no run_as_allowed at all", h.server.WorkBodies()[0])
			}
		})
	}
}

// PRD §21: the allowlist is the operator's, and nothing the Server answers
// may change it.
func TestNoServerAnswerChangesTheAllowlist(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 3, runAsAllowed: []string{"ohmyjob"}})

	h.server.Inject(map[string]any{
		"run_as_allowed": []string{"root"},
		"config":         map[string]any{"poll_wait_seconds": 25, "run_as_allowed": []string{"root"}},
	})

	h.run(t)

	requests := h.server.WorkRequests()
	if len(requests) < 2 {
		t.Fatalf("work requests = %d, want at least 2", len(requests))
	}

	for i, request := range requests {
		if !reflect.DeepEqual(request.RunAsAllowed, []string{"ohmyjob"}) {
			t.Errorf("request %d run_as_allowed = %#v, want [ohmyjob]", i, request.RunAsAllowed)
		}
	}
}

func TestSlotsNeverExceedSixteen(t *testing.T) {
	h := newHarness(t, harnessOptions{maxConcurrent: 64, stopAfter: 1})

	h.run(t)

	if got := h.server.WorkRequests()[0].Slots; got != MaxSlots {
		t.Fatalf("slots = %d, want %d", got, MaxSlots)
	}
}

func TestCancelledBeforeStartIsNeverStarted(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2})

	h.server.Enqueue(h.lease(runA, "true"))
	h.server.Cancel(runA)

	h.run(t)

	if starts := h.server.Starts(); len(starts) != 0 {
		t.Fatalf("starts = %v, want none", starts)
	}

	reported := h.reporter.reported()
	if len(reported) != 1 || !reported[0].CancelledBeforeStart || reported[0].Process != nil {
		t.Fatalf("reported = %+v, want one run cancelled before start", reported)
	}

	if outcome, _ := h.store.RecentOutcome(runA); outcome.Status != string(protocol.RunStatusCancelled) {
		t.Fatalf("recent outcome = %+v, want cancelled", outcome)
	}
}

func TestCancelForUnknownRunIsIgnored(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2})

	h.server.Cancel(runB)

	h.run(t)

	if h.logs.count("cancellation requested") != 0 {
		t.Fatal("a cancellation for an unknown run was acted on")
	}
}

func TestRecentOutcomeIsResentNotRerun(t *testing.T) {
	code := 0

	h := newHarness(t, harnessOptions{stopAfter: 2, before: func(h *harness) {
		if err := h.store.MarkFinished(runA, state.Outcome{Status: string(protocol.RunStatusSuccess), ExitCode: &code}); err != nil {
			t.Fatal(err)
		}
	}})

	h.server.Enqueue(h.lease(runA, "true"))

	h.run(t)

	if starts := h.server.Starts(); len(starts) != 0 {
		t.Fatalf("starts = %v, want none", starts)
	}

	resent := h.resender.resent()
	if len(resent) != 1 || resent[0].RunID != runA || resent[0].Status != string(protocol.RunStatusSuccess) {
		t.Fatalf("resent = %+v, want the stored success", resent)
	}
}

func TestStartConflictDropsTheLease(t *testing.T) {
	tests := []protocol.ErrorCode{protocol.ErrLeaseExpired, protocol.ErrRunCancelled, protocol.ErrRunFinished, protocol.ErrNotLeased}

	for _, code := range tests {
		t.Run(string(code), func(t *testing.T) {
			h := newHarness(t, harnessOptions{stopAfter: 2})

			h.server.Enqueue(h.lease(runA, "true"))
			h.server.RefuseStart(runA, http.StatusConflict, code)

			h.run(t)

			if starts := h.server.Starts(); len(starts) != 1 {
				t.Fatalf("starts = %d, want 1", len(starts))
			}

			if reported := h.reporter.reported(); len(reported) != 0 {
				t.Fatalf("reported = %+v, want none", reported)
			}

			if h.logs.count("lease dropped") != 1 {
				t.Fatal("drop not logged once")
			}
		})
	}
}

func TestStartRetriesTransientFailures(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2})

	h.server.Enqueue(h.lease(runA, "true"))
	h.server.FailNext("start", http.StatusServiceUnavailable, "")

	h.run(t)

	// The fake records a start only once it serves it, so the refused first
	// attempt leaves no trace and the retry is the one on record.
	if starts := h.server.Starts(); len(starts) != 1 {
		t.Fatalf("starts recorded = %d, want 1", len(starts))
	}

	if reported := h.reporter.reported(); len(reported) != 1 || reported[0].Process == nil {
		t.Fatalf("reported = %+v, want one started run", reported)
	}

	if got := h.sleeper.recorded(); len(got) != 1 || got[0] != time.Second {
		t.Fatalf("sleeps = %v, want one backoff before the second start", got)
	}
}

func TestSpawnFailureReachesTheReporter(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2})

	lease := h.lease(runA, "true")
	dir := "/nonexistent/omj-agent"
	lease.WorkingDirectory = &dir
	h.server.Enqueue(lease)

	h.run(t)

	reported := h.reporter.reported()
	if len(reported) != 1 || reported[0].SpawnErr == nil || reported[0].Process != nil {
		t.Fatalf("reported = %+v, want one spawn failure", reported)
	}

	if !strings.Contains(h.reporter.stderrOf(runA), dir) {
		t.Fatalf("stderr = %q, want the missing directory", h.reporter.stderrOf(runA))
	}

	if outcome, _ := h.store.RecentOutcome(runA); outcome.Status != string(protocol.RunStatusFailed) {
		t.Fatalf("recent outcome = %+v, want failed", outcome)
	}
}

func TestSuccessfulRunIsRecorded(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2})

	lease := h.lease(runA, "echo hello; exit 3")
	h.server.Enqueue(lease)

	h.run(t)

	outcome, ok := h.store.RecentOutcome(runA)
	if !ok || outcome.Status != string(protocol.RunStatusFailed) || outcome.ExitCode == nil || *outcome.ExitCode != 3 {
		t.Fatalf("recent outcome = %+v, want failed with exit code 3", outcome)
	}

	if h.buffer.Size() != 0 {
		t.Fatalf("buffer still holds %d bytes after the run was released", h.buffer.Size())
	}

	if len(h.agent.ActiveRuns()) != 0 {
		t.Fatal("run still registered")
	}
}

func TestConfigFromTheServerIsApplied(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2})

	h.server.SetConfig(protocol.AgentConfig{HeartbeatIntervalSeconds: 30, OutputFlushIntervalMS: 250, OutputChunkBytes: 1024, PollWaitSeconds: 10})

	h.run(t)

	requests := h.server.WorkRequests()
	if requests[0].WaitSeconds != 25 || requests[1].WaitSeconds != 10 {
		t.Fatalf("wait_seconds = %d then %d, want 25 then 10", requests[0].WaitSeconds, requests[1].WaitSeconds)
	}

	want := Settings{HeartbeatInterval: 30 * time.Second, FlushInterval: 250 * time.Millisecond, ChunkBytes: 1024, PollWait: 10 * time.Second}
	if got := h.agent.Settings(); got != want {
		t.Fatalf("settings = %+v, want %+v", got, want)
	}
}

func TestPollWaitIsCappedAtTheProtocolMaximum(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2})

	h.server.SetConfig(protocol.AgentConfig{PollWaitSeconds: 90})

	h.run(t)

	if got := h.server.WorkRequests()[1].WaitSeconds; got != 25 {
		t.Fatalf("wait_seconds = %d, want 25", got)
	}
}

func TestRunStopsWhenTheContextEnds(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 1})

	done := make(chan error, 1)

	go func() { done <- h.agent.Run(h.ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context ended")
	}

	if h.logs.count("agent loop stopped") != 1 {
		t.Fatal("stop not logged")
	}
}

func TestOutcomeOf(t *testing.T) {
	three := 3

	tests := []struct {
		name   string
		result runner.Result
		status protocol.RunStatus
		code   *int
		reason *protocol.FinishReason
	}{
		{name: "success", result: runner.Result{}, status: protocol.RunStatusSuccess, code: new(int)},
		{name: "failed", result: runner.Result{ExitCode: 3}, status: protocol.RunStatusFailed, code: &three},
		{name: "timed out", result: runner.Result{ExitCode: 143, TimedOut: true}, status: protocol.RunStatusTimedOut, code: ptr(143)},
		{name: "cancelled", result: runner.Result{ExitCode: 143, Cancelled: true, Reason: CancelRequested}, status: protocol.RunStatusCancelled, code: ptr(143)},
		{name: "agent stopped", result: runner.Result{ExitCode: 143, Cancelled: true, Reason: "agent_stopped"}, status: protocol.RunStatusCancelled, code: ptr(143), reason: ptr(protocol.ReasonAgentStopped)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, code, reason := OutcomeOf(tt.result)

			if status != tt.status || *code != *tt.code {
				t.Fatalf("OutcomeOf() = %s, %d; want %s, %d", status, *code, tt.status, *tt.code)
			}

			if (reason == nil) != (tt.reason == nil) || (reason != nil && *reason != *tt.reason) {
				t.Fatalf("reason = %v, want %v", reason, tt.reason)
			}
		})
	}
}

func ptr[T any](v T) *T {
	return &v
}

func TestRepeatedCancellationIsActedOnOnce(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	h.server.Enqueue(h.lease(runA, "sleep 30"))

	release := h.server.HoldFinish()

	// The Server lists a Run in cancel_run_ids until its finish is accepted,
	// so the Agent sees the same id on every poll.
	h.server.OnWork(func(count int, _ protocol.WorkRequest) {
		if count >= 2 {
			h.server.Cancel(runA)
		}

		if count >= 5 {
			h.cancel()
		}
	})

	if err := h.agent.Run(h.ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	release()
	h.agent.Wait()

	if got := h.logs.count("cancellation requested"); got != 1 {
		t.Fatalf("cancellation logged %d times, want 1", got)
	}

	reported := h.reporter.reported()
	if len(reported) != 1 || !reported[0].Process.Wait().Cancelled {
		t.Fatalf("reported = %+v, want one cancelled run", reported)
	}
}

// PRD §21: the Server picks from the operator's list and a name that is not on
// it is refused, so a Server that names the wrong user is wrong rather than
// dangerous.
func TestALeaseNamingAUserTheMachineDoesNotAllowIsRefused(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatalf("current user: %v", err)
	}

	tests := []struct {
		name       string
		allowed    []string
		runAs      string
		wantErr    string
		wantSpawn  bool
		wantStatus protocol.RunStatus
	}{
		{
			name:       "a user the operator never allowed",
			allowed:    []string{"deploy"},
			runAs:      "root",
			wantErr:    `does not allow work to run as "root"`,
			wantStatus: protocol.RunStatusFailed,
		},
		{
			name:       "any user at all when the machine has no allowlist",
			runAs:      "deploy",
			wantErr:    `allows no execution users, so it will not run work as "deploy"`,
			wantStatus: protocol.RunStatusFailed,
		},
		{
			name:       "an allowed user runs",
			allowed:    []string{current.Username},
			runAs:      current.Username,
			wantSpawn:  true,
			wantStatus: protocol.RunStatusSuccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{stopAfter: 2, runAsAllowed: tt.allowed})

			evidence := filepath.Join(t.TempDir(), "executed")
			lease := h.lease(runA, "touch "+evidence)
			lease.RunAs = &tt.runAs
			h.server.Enqueue(lease)

			h.run(t)

			reported := h.reporter.reported()
			if len(reported) != 1 {
				t.Fatalf("reported = %d runs, want 1", len(reported))
			}

			if spawned := reported[0].SpawnErr == nil; spawned != tt.wantSpawn {
				t.Fatalf("spawned = %v (err %v), want %v", spawned, reported[0].SpawnErr, tt.wantSpawn)
			}

			if _, err := os.Stat(evidence); (err == nil) != tt.wantSpawn {
				t.Errorf("command executed = %v, want %v", err == nil, tt.wantSpawn)
			}

			if outcome, _ := h.store.RecentOutcome(runA); outcome.Status != string(tt.wantStatus) {
				t.Errorf("recent outcome = %+v, want %s", outcome, tt.wantStatus)
			}

			if tt.wantErr != "" && !strings.Contains(h.reporter.stderrOf(runA), tt.wantErr) {
				t.Errorf("stderr = %q, want it to name the refusal %q", h.reporter.stderrOf(runA), tt.wantErr)
			}
		})
	}
}

// A refusal must reach the Server as an outcome rather than as silence, so the
// operator sees a Run that failed and why, not one that never happened.
func TestARefusedLeaseIsFinishedWithAReason(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2, runAsAllowed: []string{"deploy"}})

	runAs := "root"
	lease := h.lease(runA, "true")
	lease.RunAs = &runAs
	h.server.Enqueue(lease)

	h.run(t)

	finishes := h.server.Finishes()
	if len(finishes) != 1 {
		t.Fatalf("finishes = %d, want 1", len(finishes))
	}

	request := finishes[0].Request
	if request.Status != protocol.RunStatusFailed || request.Reason == nil || *request.Reason != protocol.ReasonSpawnFailed {
		t.Fatalf("finish = %+v, want failed with reason spawn_failed", request)
	}
}

// A lease with no run_as is every lease an older Server sends, and it must
// behave exactly as it did before per-Job users existed.
func TestALeaseWithoutARunAsUserIsUnchanged(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 2, runAsAllowed: []string{"deploy"}})

	h.server.Enqueue(h.lease(runA, "true"))

	h.run(t)

	reported := h.reporter.reported()
	if len(reported) != 1 || reported[0].SpawnErr != nil {
		t.Fatalf("reported = %+v, want one run that started", reported)
	}

	if outcome, _ := h.store.RecentOutcome(runA); outcome.Status != string(protocol.RunStatusSuccess) {
		t.Fatalf("recent outcome = %+v, want success", outcome)
	}
}
