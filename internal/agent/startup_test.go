package agent

import (
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/state"
)

func TestStaleActiveRunIsReportedAsLostWithoutBlockingPolling(t *testing.T) {
	var startedAt time.Time

	h := newHarness(t, harnessOptions{stopAfter: 1, before: func(h *harness) {
		startedAt = h.now.Add(-time.Hour)

		if err := h.store.MarkActive(state.ActiveRun{RunID: runA, PID: 4242, PGID: 4242, StartedAt: startedAt}); err != nil {
			t.Fatal(err)
		}
	}})

	release := h.server.HoldFinish()

	if err := h.agent.Run(h.ctx); err != nil {
		t.Fatalf("Run() = %v", err)
	}

	// The loop polled while the finish was still held by the server.
	requests := h.server.WorkRequests()
	if len(requests) != 1 || len(requests[0].ActiveRuns) != 0 {
		t.Fatalf("work requests = %+v, want one poll with no active runs", requests)
	}

	release()
	h.agent.Wait()

	finishes := h.server.Finishes()
	if len(finishes) != 1 || finishes[0].RunID != runA {
		t.Fatalf("finishes = %+v, want one for the stale run", finishes)
	}

	request := finishes[0].Request
	if request.Status != protocol.RunStatusLost || request.Reason == nil || *request.Reason != protocol.ReasonAgentRestarted || request.ExitCode != nil {
		t.Fatalf("finish = %+v, want lost with reason agent_restarted and no exit code", request)
	}

	if request.StartedAt == nil || !request.StartedAt.Equal(startedAt) {
		t.Fatalf("started_at = %v, want %s from the state file", request.StartedAt, startedAt)
	}

	if h.store.IsActive(runA) {
		t.Fatal("run still active in the state file")
	}

	outcome, ok := h.store.RecentOutcome(runA)
	if !ok || outcome.Status != string(protocol.RunStatusLost) || outcome.StartedAt == nil || !outcome.StartedAt.Equal(startedAt) {
		t.Fatalf("recent outcome = %+v, %v; want lost with the start time", outcome, ok)
	}

	if h.logs.count("reporting it as lost") != 1 || h.logs.count("lost run reported") != 1 {
		t.Fatal("the reconciliation was not logged")
	}
}

func TestReleasedRunWithAnOutcomeIsResentWithItsStartTime(t *testing.T) {
	var startedAt time.Time

	h := newHarness(t, harnessOptions{stopAfter: 2, realResender: true, before: func(h *harness) {
		startedAt = h.now.Add(-time.Minute)
		code := 0

		if err := h.store.MarkFinished(runA, state.Outcome{Status: string(protocol.RunStatusSuccess), ExitCode: &code, StartedAt: &startedAt}); err != nil {
			t.Fatal(err)
		}
	}})

	h.server.Enqueue(h.lease(runA, "true"))

	h.run(t)

	finishes := h.server.Finishes()
	if len(finishes) != 1 || finishes[0].Request.Status != protocol.RunStatusSuccess {
		t.Fatalf("finishes = %+v, want the stored success once", finishes)
	}

	if got := finishes[0].Request.StartedAt; got == nil || !got.Equal(startedAt) {
		t.Fatalf("started_at = %v, want %s", got, startedAt)
	}

	if starts := h.server.Starts(); len(starts) != 0 {
		t.Fatalf("starts = %v, want none", starts)
	}

	if reported := h.reporter.reported(); len(reported) != 0 {
		t.Fatalf("reported = %+v, want no process", reported)
	}
}

func TestStartupLogsTheConfigurationWithoutTheCredential(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 1})

	h.run(t)

	if h.logs.count("agent starting") != 1 {
		t.Fatal("startup not logged once")
	}

	for _, want := range []string{"machine_id=" + fakeMachineID, "user=ohmyjob", "uid=1000", "max_concurrent_runs=4", "max_timeout_seconds=259200", "max_output_bytes=104857600", "active_runs=0", "server_url=" + h.server.URL()} {
		if h.logs.count(want) == 0 {
			t.Errorf("startup line lacks %q", want)
		}
	}

	if h.logs.count(fakeSecret) != 0 {
		t.Fatal("the credential reached the log")
	}
}

func TestStartupRecordsTheMachineIdInTheStateFile(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 1})

	if got := h.store.MachineID(); got != "" {
		t.Fatalf("machine id before the run = %q, want empty", got)
	}

	h.run(t)

	if got := h.store.MachineID(); got != h.server.MachineID {
		t.Fatalf("machine id = %q, want %q", got, h.server.MachineID)
	}
}

func TestStateFileOfAnotherMachineIsSetAside(t *testing.T) {
	h := newHarness(t, harnessOptions{stopAfter: 1, before: func(h *harness) {
		if err := h.store.SetMachineID("11111111-2222-3333-4444-555555555555"); err != nil {
			t.Fatal(err)
		}

		if err := h.store.MarkActive(state.ActiveRun{RunID: runA, PID: 4242, PGID: 4242, StartedAt: h.now}); err != nil {
			t.Fatal(err)
		}
	}})

	h.run(t)

	if got := h.store.MachineID(); got != h.server.MachineID {
		t.Fatalf("machine id = %q, want %q", got, h.server.MachineID)
	}

	if active := h.store.Active(); len(active) != 0 {
		t.Fatalf("active runs = %+v, want none", active)
	}

	// The Runs belong to the previous enrolment, so the Server never hears
	// about them.
	if finishes := h.server.Finishes(); len(finishes) != 0 {
		t.Fatalf("finishes = %+v, want none", finishes)
	}

	if h.logs.count("state file belongs to another machine") != 1 {
		t.Fatalf("log = %q, want one warning about the foreign state file", h.logs.buf.String())
	}
}
