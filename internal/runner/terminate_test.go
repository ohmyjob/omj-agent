package runner

import (
	"context"
	"testing"
	"time"
)

const (
	testGrace = 200 * time.Millisecond

	// Sleeps die on SIGTERM at once; anything slower than this means the
	// group was not signalled.
	promptExit = 5 * time.Second
)

func start(t *testing.T, runner Runner, spec Spec) (*Process, *recordingSink) {
	t.Helper()

	sink := newRecordingSink()

	process, err := runner.Start(context.Background(), spec, sink)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	return process, sink
}

// A signal sent before the shell has run its trap line kills the shell
// outright, so the scripts announce when they are ready to be signalled.
func awaitReady(t *testing.T, sink *recordingSink) {
	t.Helper()

	deadline := time.Now().Add(promptExit)

	for sink.String(Stdout) != "ready\n" {
		if time.Now().After(deadline) {
			t.Fatalf("stdout = %q, want the ready marker", sink.String(Stdout))
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func waitWithin(t *testing.T, process *Process, limit time.Duration) Result {
	t.Helper()

	started := time.Now()
	result := process.Wait()

	if result.Err != nil {
		t.Fatalf("Wait: %v", result.Err)
	}

	if elapsed := time.Since(started); elapsed > limit {
		t.Fatalf("Wait took %v, want at most %v", elapsed, limit)
	}

	return result
}

func assertGroupGone(t *testing.T, pgid int) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)

	for groupAlive(pgid) {
		if time.Now().After(deadline) {
			t.Fatalf("process group %d still has members", pgid)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func TestCancelTerminatesTheWholeGroup(t *testing.T) {
	process, sink := start(t, Runner{Grace: testGrace}, Spec{Command: "sleep 300 & sleep 300 & echo ready; wait"})

	awaitReady(t, sink)
	process.Cancel("cancel_requested")

	result := waitWithin(t, process, promptExit)
	assertGroupGone(t, process.PGID())

	if !result.Cancelled || result.TimedOut {
		t.Errorf("flags = cancelled %t, timed out %t; want cancelled only", result.Cancelled, result.TimedOut)
	}

	if result.Reason != "cancel_requested" {
		t.Errorf("reason = %q, want %q", result.Reason, "cancel_requested")
	}

	if result.ExitCode != 143 {
		t.Errorf("exit code = %d, want 143", result.ExitCode)
	}
}

func TestTerminationKillsAfterTheGrace(t *testing.T) {
	tests := []struct {
		name          string
		spec          Spec
		cancel        bool
		wantTimedOut  bool
		wantCancelled bool
	}{
		{name: "a timeout", spec: Spec{Command: "trap '' TERM; echo ready; sleep 300", Timeout: 100 * time.Millisecond}, wantTimedOut: true},
		{name: "a cancellation", spec: Spec{Command: "trap '' TERM; echo ready; sleep 300"}, cancel: true, wantCancelled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			process, sink := start(t, Runner{Grace: testGrace}, tt.spec)

			if tt.cancel {
				awaitReady(t, sink)
				process.Cancel("agent_stopped")
			}

			result := waitWithin(t, process, promptExit)
			assertGroupGone(t, process.PGID())

			if result.TimedOut != tt.wantTimedOut || result.Cancelled != tt.wantCancelled {
				t.Errorf("flags = timed out %t, cancelled %t; want %t and %t", result.TimedOut, result.Cancelled, tt.wantTimedOut, tt.wantCancelled)
			}

			if result.ExitCode != 137 || result.Signal == nil || result.Signal.String() != "killed" {
				t.Errorf("exit = %d %v, want 137 killed", result.ExitCode, result.Signal)
			}

			if result.FinishedAt.Sub(result.StartedAt) < testGrace {
				t.Errorf("finished after %v, want at least the grace of %v", result.FinishedAt.Sub(result.StartedAt), testGrace)
			}
		})
	}
}

func TestAProcessExitingDuringTheGraceKeepsItsExitCode(t *testing.T) {
	process, sink := start(t, Runner{Grace: testGrace}, Spec{Command: "trap 'exit 7' TERM; echo ready; sleep 300 & wait"})

	awaitReady(t, sink)
	process.Cancel("cancel_requested")

	result := waitWithin(t, process, promptExit)
	assertGroupGone(t, process.PGID())

	if result.ExitCode != 7 || result.Signal != nil {
		t.Errorf("exit = %d %v, want 7 and no signal", result.ExitCode, result.Signal)
	}

	if !result.Cancelled {
		t.Error("cancelled = false, want true")
	}
}

func TestTimeoutIsClampedToTheMaximum(t *testing.T) {
	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{name: "zero means the maximum", timeout: 0},
		{name: "more than the maximum is clamped", timeout: time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			process, _ := start(t, Runner{MaxTimeout: 100 * time.Millisecond, Grace: testGrace}, Spec{Command: "sleep 300", Timeout: tt.timeout})

			result := waitWithin(t, process, promptExit)

			if !result.TimedOut || result.Cancelled {
				t.Errorf("flags = timed out %t, cancelled %t; want timed out only", result.TimedOut, result.Cancelled)
			}

			if result.ExitCode != 143 {
				t.Errorf("exit code = %d, want 143", result.ExitCode)
			}
		})
	}
}

func TestRunnerTimeout(t *testing.T) {
	tests := []struct {
		name      string
		runner    Runner
		requested time.Duration
		want      time.Duration
	}{
		{name: "no limits at all fall back to the default maximum", want: DefaultMaxTimeout},
		{name: "zero means the local maximum", runner: Runner{MaxTimeout: time.Hour}, want: time.Hour},
		{name: "above the maximum is clamped", runner: Runner{MaxTimeout: time.Hour}, requested: 2 * time.Hour, want: time.Hour},
		{name: "within the maximum is kept", runner: Runner{MaxTimeout: time.Hour}, requested: 30 * time.Minute, want: 30 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.runner.timeout(tt.requested); got != tt.want {
				t.Errorf("timeout = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestANaturalExitSetsNoFlag(t *testing.T) {
	process, _ := start(t, Runner{Grace: testGrace}, Spec{Command: "exit 3", Timeout: 5 * time.Second})

	result := waitWithin(t, process, promptExit)

	if result.TimedOut || result.Cancelled || result.Reason != "" {
		t.Errorf("result = %+v, want no termination flag or reason", result)
	}

	if result.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", result.ExitCode)
	}
}

func TestTheFirstTerminationWins(t *testing.T) {
	process, _ := start(t, Runner{Grace: testGrace}, Spec{Command: "sleep 300", Timeout: 10 * time.Second})

	process.Cancel("first")
	process.Cancel("second")

	result := waitWithin(t, process, promptExit)

	if !result.Cancelled || result.TimedOut || result.Reason != "first" {
		t.Errorf("result = cancelled %t, timed out %t, reason %q; want the first cancellation", result.Cancelled, result.TimedOut, result.Reason)
	}
}

func TestCancelAfterExitChangesNothing(t *testing.T) {
	process, _ := start(t, Runner{Grace: testGrace}, Spec{Command: "exit 5"})

	first := process.Wait()
	process.Cancel("too late")
	second := process.Wait()

	if first.Cancelled || second != first {
		t.Errorf("results = %+v and %+v, want the same uncancelled result", first, second)
	}
}
