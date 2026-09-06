package agent

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/runner"
	"github.com/ohmyjob/omj-agent/internal/state"
)

// startReporting hands one lease out on the first poll, stops the loop on
// the second, and leaves the reporter running on its own goroutine; the
// returned function waits for it to release the Run.
func startReporting(t *testing.T, h *harness, lease protocol.RunLease) func() {
	t.Helper()

	h.server.Enqueue(lease)
	h.server.OnWork(func(count int, _ protocol.WorkRequest) {
		if count >= 2 {
			h.cancel()
		}
	})

	done := make(chan struct{})

	go func() {
		defer close(done)
		h.run(t)
	}()

	return func() {
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatal("the reporter did not release the run within 15s")
		}
	}
}

func (h *harness) finished() bool {
	return len(h.server.Finishes()) > 0
}

func outputText(records []outputRecord) (string, []uint64) {
	var (
		text strings.Builder
		seqs []uint64
	)

	for _, record := range records {
		for _, chunk := range record.Request.Chunks {
			text.Write(chunk.Data)
			seqs = append(seqs, chunk.Seq)
		}
	}

	return text.String(), seqs
}

func TestOutputIsDeliveredOnceInOrderAfterAnOutage(t *testing.T) {
	h := newHarness(t, harnessOptions{batchChunks: 1})

	// Four-byte chunks split the output into four numbered pieces that each
	// travel in their own batch.
	h.server.SetConfig(protocol.AgentConfig{HeartbeatIntervalSeconds: 15, OutputFlushIntervalMS: 500, OutputChunkBytes: 4, PollWaitSeconds: 25})

	for range 5 {
		h.server.FailNext("output", http.StatusServiceUnavailable, "")
	}

	wait := startReporting(t, h, h.lease(runA, "printf 'one\\ntwo\\nthree\\n'"))
	wait()

	text, seqs := outputText(h.server.Outputs())
	if text != "one\ntwo\nthree\n" {
		t.Fatalf("delivered output = %q", text)
	}

	if len(seqs) != 4 || seqs[0] != 1 || seqs[1] != 2 || seqs[2] != 3 || seqs[3] != 4 {
		t.Fatalf("sequence numbers = %v, want 1 to 4 once each", seqs)
	}

	// The five failures add up to about thirty seconds of outage before the
	// first batch got through.
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	if got := h.sleeper.recorded(); len(got) != len(want) || got[0] != want[0] || got[4] != want[4] {
		t.Fatalf("sleeps = %v, want %v", got, want)
	}

	finishes := h.server.Finishes()
	if len(finishes) != 1 || finishes[0].Request.Status != protocol.RunStatusSuccess || finishes[0].Request.LastOutputSeq != 4 {
		t.Fatalf("finishes = %+v, want one success with last_output_seq 4", finishes)
	}

	if h.buffer.Size() != 0 {
		t.Fatalf("buffer still holds %d bytes", h.buffer.Size())
	}
}

func TestHeartbeatIsSentBeforeOutputIsReplayed(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	h.server.FailNext("output", http.StatusBadGateway, "")

	wait := startReporting(t, h, h.lease(runA, "echo hello"))
	wait()

	if beats := h.server.Heartbeats(); len(beats) != 1 || beats[0] != runA {
		t.Fatalf("heartbeats = %v, want one before the replay", beats)
	}

	if text, _ := outputText(h.server.Outputs()); text != "hello\n" {
		t.Fatalf("delivered output = %q", text)
	}
}

func TestCancelRequestedStopsTheProcess(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		interval func(h *harness) time.Duration
	}{
		{name: "on a heartbeat", command: "sleep 30", interval: func(h *harness) time.Duration { return h.agent.Settings().HeartbeatInterval }},
		{name: "on an output response", command: "echo hello; sleep 30", interval: func(h *harness) time.Duration { return h.agent.Settings().FlushInterval }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{})
			h.server.RequestCancel(runA)

			wait := startReporting(t, h, h.lease(runA, tt.command))
			h.ticker.tickUntil(t, tt.interval(h), h.finished)
			wait()

			finishes := h.server.Finishes()
			if len(finishes) != 1 || finishes[0].Request.Status != protocol.RunStatusCancelled || finishes[0].Request.Reason != nil {
				t.Fatalf("finishes = %+v, want one cancelled without a reason", finishes)
			}

			if code := finishes[0].Request.ExitCode; code == nil || *code != 143 {
				t.Fatalf("exit code = %v, want 143 (SIGTERM)", exitCode(code))
			}

			if outcome, _ := h.store.RecentOutcome(runA); outcome.Status != string(protocol.RunStatusCancelled) {
				t.Fatalf("recent outcome = %+v, want cancelled", outcome)
			}
		})
	}
}

func TestFinishAlreadyRecordedCountsAsDelivered(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	h.server.FailNext("finish", http.StatusConflict, protocol.ErrRunFinished)

	wait := startReporting(t, h, h.lease(runA, "true"))
	wait()

	if finishes := h.server.Finishes(); len(finishes) != 0 {
		t.Fatalf("finishes = %+v, want the refused one only, which is not recorded", finishes)
	}

	if got := h.sleeper.recorded(); len(got) != 0 {
		t.Fatalf("sleeps = %v, want none: a 409 is final", got)
	}

	if h.logs.count("already had an outcome") != 1 {
		t.Fatal("the conflict was not logged as delivered")
	}

	outcome, ok := h.store.RecentOutcome(runA)
	if !ok || outcome.Status != string(protocol.RunStatusSuccess) {
		t.Fatalf("recent outcome = %+v, %v; want success", outcome, ok)
	}
}

func TestFinishIsRetriedThroughAnOutage(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	h.server.FailNext("finish", http.StatusInternalServerError, "")
	h.server.FailNext("finish", http.StatusInternalServerError, "")

	wait := startReporting(t, h, h.lease(runA, "exit 3"))
	wait()

	finishes := h.server.Finishes()
	if len(finishes) != 1 || finishes[0].Request.Status != protocol.RunStatusFailed || *finishes[0].Request.ExitCode != 3 {
		t.Fatalf("finishes = %+v, want one failed with exit code 3", finishes)
	}

	if got := h.sleeper.recorded(); len(got) != 2 || got[0] != time.Second || got[1] != 2*time.Second {
		t.Fatalf("sleeps = %v, want 1s then 2s", got)
	}
}

func TestSpawnFailureIsReportedWithItsError(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	lease := h.lease(runA, "true")
	dir := "/nonexistent/omj-agent"
	lease.WorkingDirectory = &dir

	wait := startReporting(t, h, lease)
	wait()

	outputs := h.server.Outputs()
	if len(outputs) != 1 || len(outputs[0].Request.Chunks) != 1 {
		t.Fatalf("outputs = %+v, want one chunk", outputs)
	}

	chunk := outputs[0].Request.Chunks[0]
	if chunk.Stream != protocol.Stream(runner.Stderr) || !strings.Contains(string(chunk.Data), dir) {
		t.Fatalf("chunk = %+v, want the error on stderr", chunk)
	}

	finishes := h.server.Finishes()
	if len(finishes) != 1 {
		t.Fatalf("finishes = %+v, want one", finishes)
	}

	request := finishes[0].Request
	if request.Status != protocol.RunStatusFailed || request.Reason == nil || *request.Reason != protocol.ReasonSpawnFailed {
		t.Fatalf("finish = %+v, want failed with reason spawn_failed", request)
	}

	if request.ExitCode != nil || request.StartedAt != nil || request.LastOutputSeq != 1 {
		t.Fatalf("finish = %+v, want no exit code, no start time and last_output_seq 1", request)
	}
}

func TestCancelledBeforeStartIsFinishedAsCancelled(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	h.server.Cancel(runA)

	wait := startReporting(t, h, h.lease(runA, "true"))
	wait()

	finishes := h.server.Finishes()
	if len(finishes) != 1 || finishes[0].Request.Status != protocol.RunStatusCancelled || finishes[0].Request.ExitCode != nil || finishes[0].Request.StartedAt != nil {
		t.Fatalf("finishes = %+v, want one cancelled with no exit code and no start time", finishes)
	}

	if starts := h.server.Starts(); len(starts) != 0 {
		t.Fatalf("starts = %v, want none", starts)
	}
}

func TestStoredOutcomeIsResent(t *testing.T) {
	code := 3

	h := newHarness(t, harnessOptions{realResender: true, before: func(h *harness) {
		if err := h.store.MarkFinished(runA, state.Outcome{Status: string(protocol.RunStatusFailed), ExitCode: &code}); err != nil {
			t.Fatal(err)
		}
	}})

	wait := startReporting(t, h, h.lease(runA, "true"))
	wait()

	finishes := h.server.Finishes()
	if len(finishes) != 1 || finishes[0].Request.Status != protocol.RunStatusFailed || *finishes[0].Request.ExitCode != 3 {
		t.Fatalf("finishes = %+v, want the stored failure with exit code 3", finishes)
	}

	if !finishes[0].Request.FinishedAt.Equal(h.now) || finishes[0].Request.StartedAt != nil {
		t.Fatalf("finish = %+v, want the stored finish time and no start time", finishes[0].Request)
	}

	if starts := h.server.Starts(); len(starts) != 0 {
		t.Fatalf("starts = %v, want none", starts)
	}
}

func TestResendTreatsAConflictAsDelivered(t *testing.T) {
	h := newHarness(t, harnessOptions{realResender: true, before: func(h *harness) {
		if err := h.store.MarkFinished(runA, state.Outcome{Status: string(protocol.RunStatusLost)}); err != nil {
			t.Fatal(err)
		}
	}})

	h.server.FailNext("finish", http.StatusConflict, protocol.ErrRunFinished)

	wait := startReporting(t, h, h.lease(runA, "true"))
	wait()

	if h.logs.count("outcome not re-sent") != 0 {
		t.Fatal("a 409 run_finished was reported as a failure")
	}
}

func TestTruncatedStopsOutputButNotTheFinish(t *testing.T) {
	h := newHarness(t, harnessOptions{batchChunks: 1})

	h.server.TruncateOutput(runA)

	wait := startReporting(t, h, h.lease(runA, "echo out; echo err 1>&2"))
	wait()

	if outputs := h.server.Outputs(); len(outputs) != 1 {
		t.Fatalf("outputs = %d, want the one the server truncated", len(outputs))
	}

	finishes := h.server.Finishes()
	if len(finishes) != 1 || !finishes[0].Request.OutputTruncated || finishes[0].Request.LastOutputSeq != 2 {
		t.Fatalf("finishes = %+v, want one marked truncated with last_output_seq 2", finishes)
	}
}

func TestRunNotRunningStopsOutputButNotTheFinish(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	h.server.FailNext("output", http.StatusConflict, protocol.ErrRunNotRunning)

	wait := startReporting(t, h, h.lease(runA, "echo hello"))
	wait()

	if outputs := h.server.Outputs(); len(outputs) != 0 {
		t.Fatalf("outputs = %+v, want none after the server closed the run", outputs)
	}

	if finishes := h.server.Finishes(); len(finishes) != 1 || finishes[0].Request.Status != protocol.RunStatusSuccess {
		t.Fatalf("finishes = %+v, want one success", finishes)
	}

	if h.logs.count("no longer accepts output") != 1 {
		t.Fatal("the closed run was not logged")
	}
}

func TestHeartbeatFailureNeverKillsTheProcess(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	h.server.FailNext("heartbeat", http.StatusBadGateway, "")

	// The process ends when the test says so rather than after a fixed sleep,
	// because a machine slow enough to finish the Run before the next
	// heartbeat would hide the resumption this test is about.
	gate := filepath.Join(t.TempDir(), "gate")
	wait := startReporting(t, h, h.lease(runA, "until [ -f '"+gate+"' ]; do sleep 0.01; done"))

	h.ticker.tickUntil(t, h.agent.Settings().HeartbeatInterval, func() bool {
		return len(h.server.Heartbeats()) > 0
	})

	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	h.ticker.tickUntil(t, h.agent.Settings().HeartbeatInterval, h.finished)
	wait()

	finishes := h.server.Finishes()
	if len(finishes) != 1 || finishes[0].Request.Status != protocol.RunStatusSuccess || *finishes[0].Request.ExitCode != 0 {
		t.Fatalf("finishes = %+v, want one success", finishes)
	}

	if h.logs.count("heartbeat failed") != 1 {
		t.Fatal("the failed heartbeat was not logged once")
	}
}

func TestHeartbeatsStopOnceTheServerClosedTheRun(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	h.server.FailNext("heartbeat", http.StatusConflict, protocol.ErrRunNotRunning)

	wait := startReporting(t, h, h.lease(runA, "echo hello; sleep 0.3"))
	h.ticker.tickUntil(t, h.agent.Settings().HeartbeatInterval, h.finished)
	wait()

	if beats := h.server.Heartbeats(); len(beats) != 0 {
		t.Fatalf("heartbeats = %v, want none after the conflict", beats)
	}

	if outputs := h.server.Outputs(); len(outputs) != 0 {
		t.Fatalf("outputs = %+v, want none: a closed run takes no output", outputs)
	}

	if finishes := h.server.Finishes(); len(finishes) != 1 {
		t.Fatalf("finishes = %+v, want one", finishes)
	}
}

func TestNewUsesTheReporterByDefault(t *testing.T) {
	h := newHarness(t, harnessOptions{})

	a, err := New(Options{Config: h.agent.cfg, Client: h.agent.client, State: h.store, Buffer: h.buffer})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := a.reporter.(reporter); !ok {
		t.Fatalf("reporter = %T, want the package's own", a.reporter)
	}

	if _, ok := a.resender.(reporter); !ok {
		t.Fatalf("resender = %T, want the package's own", a.resender)
	}
}
