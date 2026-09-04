package agent

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/protocol"
)

// signalAtSecondPoll delivers the signals once the first poll's lease has
// been started: the hook runs before the second request is served.
func signalAtSecondPoll(h *harness, signals ...os.Signal) {
	h.server.OnWork(func(count int, _ protocol.WorkRequest) {
		if count == 2 {
			for _, sig := range signals {
				h.signals <- sig
			}
		}
	})
}

func TestStopSignalTerminatesRunsAndReportsThem(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	h.server.Enqueue(h.lease(runA, "sleep 30"))
	signalAtSecondPoll(h, os.Interrupt)

	started := time.Now()

	if err := h.agent.Run(h.ctx); err != nil {
		t.Fatalf("Run() = %v, want nil after a clean stop", err)
	}

	h.agent.Wait()

	if elapsed := time.Since(started); elapsed >= DefaultStopBudget {
		t.Fatalf("stop took %s, want less than the %s budget", elapsed, DefaultStopBudget)
	}

	reported := h.reporter.reported()
	if len(reported) != 1 {
		t.Fatalf("reported runs = %d, want 1", len(reported))
	}

	result := reported[0].Process.Wait()
	if !result.Cancelled || result.Reason != AgentStopped {
		t.Fatalf("result = %+v, want cancelled with reason %q", result, AgentStopped)
	}

	finishes := h.server.Finishes()
	if len(finishes) != 1 || finishes[0].RunID != runA {
		t.Fatalf("finishes = %+v, want one for the run", finishes)
	}

	request := finishes[0].Request
	if request.Status != protocol.RunStatusCancelled || request.Reason == nil || *request.Reason != protocol.ReasonAgentStopped {
		t.Fatalf("finish = %+v, want cancelled with reason agent_stopped", request)
	}

	if request.ExitCode == nil || *request.ExitCode != 143 {
		t.Fatalf("exit code = %v, want 143 (SIGTERM)", exitCode(request.ExitCode))
	}

	if polls := len(h.server.WorkRequests()); polls != 2 {
		t.Fatalf("work requests = %d, want polling to stop after the signal", polls)
	}

	if h.store.IsActive(runA) {
		t.Fatal("run still active in the state file")
	}

	if outcome, _ := h.store.RecentOutcome(runA); outcome.Status != string(protocol.RunStatusCancelled) {
		t.Fatalf("recent outcome = %+v, want cancelled", outcome)
	}

	if h.logs.count("stop requested") != 1 || h.logs.count("every run was reported") != 1 {
		t.Fatal("the stop was not logged as requested and completed")
	}
}

func TestSecondSignalStopsWithoutWaiting(t *testing.T) {
	h := newHarness(t, harnessOptions{stopBudget: 30 * time.Second})

	release := h.server.HoldFinish()
	h.server.Enqueue(h.lease(runA, "sleep 30"))
	signalAtSecondPoll(h, os.Interrupt, os.Interrupt)

	started := time.Now()
	err := h.agent.Run(h.ctx)

	if !errors.Is(err, ErrForcedStop) {
		t.Fatalf("Run() = %v, want ErrForcedStop", err)
	}

	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("forced stop took %s, want an immediate return", elapsed)
	}

	release()
	h.agent.Wait()

	if h.logs.count("second signal") != 1 {
		t.Fatal("the second signal was not logged")
	}
}

func TestStopBudgetEndsTheReporters(t *testing.T) {
	h := newHarness(t, harnessOptions{stopBudget: 300 * time.Millisecond})

	release := h.server.HoldFinish()
	defer release()

	h.server.Enqueue(h.lease(runA, "sleep 30"))
	signalAtSecondPoll(h, os.Interrupt)

	if err := h.agent.Run(h.ctx); err != nil {
		t.Fatalf("Run() = %v, want nil once the budget elapsed", err)
	}

	waited := make(chan struct{})

	go func() {
		h.agent.Wait()
		close(waited)
	}()

	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the reporter kept retrying after the stop budget elapsed")
	}

	if h.logs.count("stop budget elapsed") != 1 || h.logs.count("outcome not delivered") != 1 {
		t.Fatal("the spent budget and the undelivered outcome were not logged")
	}

	if finishes := h.server.Finishes(); len(finishes) != 0 {
		t.Fatalf("finishes = %+v, want none: the server never answered", finishes)
	}

	if outcome, ok := h.store.RecentOutcome(runA); !ok || outcome.Status != string(protocol.RunStatusCancelled) {
		t.Fatalf("recent outcome = %+v, %v; want cancelled kept for the next lease", outcome, ok)
	}
}
