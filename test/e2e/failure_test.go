//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestFailureScenarios proves what happens when a command outlives its limits, when an
// operator stops it, when the link to the Server drops mid-Run and when the Agent
// itself restarts. They share one harness because bringing the images up costs far more
// than any single scenario, and they all run on agent-a so nothing interferes.
func TestFailureScenarios(t *testing.T) {
	h := start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	h.enrolledAgent(ctx, "agent-a", "agent-a")

	machines := h.awaitOnlineMachines(ctx, 1)
	machine := machines["agent-a"].ID

	t.Run("a command that outlives its timeout is stopped and leaves nothing behind", func(t *testing.T) {
		job := h.createJob(ctx, jobRequest{
			Name:     "Overruns",
			Machine:  machine,
			Command:  "sleep 60",
			Schedule: "0 3 * * *",
			Timeout:  5,
		})

		run := h.runNow(ctx, job)
		final := h.awaitTerminal(ctx, run, 20*time.Second)

		if final.Status != "timed_out" {
			t.Fatalf("status is %q, want timed_out: %s", final.Status, final.Explanation)
		}

		if surviving := h.processCount(ctx, "agent-a", "sleep 60"); surviving != 0 {
			t.Errorf("%d processes survived the timeout, want none", surviving)
		}
	})

	t.Run("cancelling a command stops its children too", func(t *testing.T) {
		job := h.createJob(ctx, jobRequest{
			Name:     "Spawns children",
			Machine:  machine,
			Command:  "sleep 300 & sleep 300 & wait",
			Schedule: "0 4 * * *",
			Timeout:  600,
		})

		run := h.runNow(ctx, job)
		h.awaitStatus(ctx, run, "running", 60*time.Second)

		// The children are started by the shell, so give them a moment to exist before
		// asking for cancellation; otherwise the scenario could pass without them.
		eventually(t, 20*time.Second, "both children starting", func() (bool, string) {
			running := h.processCount(ctx, "agent-a", "sleep 300")

			return running >= 2, fmt.Sprintf("%d of 2 children running", running)
		})

		h.cancel(ctx, run)

		final := h.awaitTerminal(ctx, run, 60*time.Second)
		if final.Status != "cancelled" {
			t.Fatalf("status is %q, want cancelled: %s", final.Status, final.Explanation)
		}

		eventually(t, 30*time.Second, "the children stopping", func() (bool, string) {
			surviving := h.processCount(ctx, "agent-a", "sleep 300")

			return surviving == 0, fmt.Sprintf("%d children still running", surviving)
		})
	})

	t.Run("output survives the link dropping mid-run", func(t *testing.T) {
		const lines = 40

		job := h.createJob(ctx, jobRequest{
			Name:     "Talks for forty seconds",
			Machine:  machine,
			Command:  fmt.Sprintf("i=1; while [ $i -le %d ]; do echo line $i; sleep 1; i=$((i+1)); done", lines),
			Schedule: "0 5 * * *",
			Timeout:  120,
		})

		run := h.runNow(ctx, job)
		h.awaitStatus(ctx, run, "running", 60*time.Second)

		// Let a few lines reach the Server first, so the scenario proves the buffered
		// ones are replayed rather than that everything arrived at the end.
		time.Sleep(8 * time.Second)

		reconnected := false
		h.disconnect(ctx, "agent-a")
		defer h.reconnectOnce(context.WithoutCancel(ctx), "agent-a", &reconnected)

		// Shorter than OMJ_RUN_LOST_AFTER_SECONDS, so the Server has no reason to give
		// up on the Run: this scenario is about the output, not about being declared lost.
		time.Sleep(20 * time.Second)

		h.reconnectOnce(ctx, "agent-a", &reconnected)

		final := h.awaitTerminal(ctx, run, 90*time.Second)
		if final.Status != "success" {
			t.Fatalf("status is %q, want success: %s", final.Status, final.Explanation)
		}

		assertLinesInOrder(t, h.log(ctx, run), lines)
	})

	t.Run("a run whose agent restarts is reported lost and never runs twice", func(t *testing.T) {
		job := h.createJob(ctx, jobRequest{
			Name:     "Interrupted by a restart",
			Machine:  machine,
			Command:  "sleep 60",
			Schedule: "0 6 * * *",
			Timeout:  120,
		})

		run := h.runNow(ctx, job)
		h.awaitStatus(ctx, run, "running", 60*time.Second)

		if _, err := h.run(ctx, "docker", "restart", h.containerID(ctx, "agent-a")); err != nil {
			t.Fatalf("restart agent-a: %v", err)
		}

		// The daemon was started with docker exec, so a restart leaves the container
		// running its idle command with no Agent in it until the suite starts one.
		if err := h.startAgent(ctx, "agent-a"); err != nil {
			t.Fatalf("start the daemon again: %v", err)
		}

		// Waiting for the Machine to report in proves the daemon is really back, so a
		// Run still running after this is the Server's answer rather than a dead Agent.
		h.awaitOnlineMachines(ctx, 1)

		final := h.awaitTerminal(ctx, run, 150*time.Second)
		if final.Status != "lost" {
			t.Fatalf("status is %q, want lost: %s", final.Status, final.Explanation)
		}
		if !strings.Contains(final.Explanation, "restarted") {
			t.Errorf("the Run does not explain that the Agent restarted: %q", final.Explanation)
		}

		if surviving := h.processCount(ctx, "agent-a", "sleep 60"); surviving != 0 {
			t.Errorf("%d interrupted processes survived the restart, want none", surviving)
		}

		// The Agent's own log is the evidence that the restart is what ended this Run.
		if log := h.agentLog(ctx, "agent-a", "/tmp/omj-agent.log"); !strings.Contains(log, "reporting it as lost") {
			t.Errorf("the Agent did not report the interrupted Run after restarting: %s", tail(log, 10))
		}

		// A quick Job proves the Agent went back to work, without waiting out another
		// minute-long command.
		after := h.createJob(ctx, jobRequest{
			Name:     "Works after a restart",
			Machine:  machine,
			Command:  "echo back at work",
			Schedule: "0 10 * * *",
		})

		// The old Agent can leave a long-poll request alive on the Server. If that
		// request claims this Run after the container has gone, the dispatch lease
		// must expire before the new Agent receives it. Allow the 60-second lease and
		// the following minute scheduler sweep rather than assuming the first lease
		// reaches the surviving connection.
		if next := h.awaitTerminal(ctx, h.runNow(ctx, after), 180*time.Second); next.Status != "success" {
			t.Errorf("the first Run after the restart ended %q, want success: %s", next.Status, next.Explanation)
		}

		// The interrupted Run must stay exactly one terminal Run: an Agent that replayed
		// its state file, or a Server that re-leased the work, would show otherwise.
		if again := h.runPage(ctx, run); again.Status != "lost" {
			t.Errorf("the lost Run changed to %q after a later Run", again.Status)
		}

		if runs := h.runsOf(ctx, job); len(runs) != 1 {
			t.Errorf("the interrupted Job has %d Runs, want 1: the interrupted work was started again", len(runs))
		}
		assertStartedOnce(t, h.agentLog(ctx, "agent-a", "/tmp/omj-agent.log"), run)
	})

	t.Run("a run declared lost recovers on the next heartbeat and is never leased twice", func(t *testing.T) {
		job := h.createJob(ctx, jobRequest{
			Name:    "Silent for a while",
			Machine: machine,
			// Long enough to outlive being declared lost, so the recovery below really
			// is a heartbeat reviving a running command rather than a late finish.
			Command:  "sleep 200; echo finished anyway",
			Schedule: "0 7 * * *",
			Timeout:  300,
		})

		run := h.runNow(ctx, job)
		h.awaitStatus(ctx, run, "running", 60*time.Second)

		reconnected := false
		h.disconnect(ctx, "agent-a")
		defer h.reconnectOnce(context.WithoutCancel(ctx), "agent-a", &reconnected)

		// OMJ_RUN_LOST_AFTER_SECONDS is 60 in the harness and the sweep runs once a
		// minute, so the Server needs up to two minutes to give up on a Run whose
		// process is in fact still going.
		h.awaitStatus(ctx, run, "lost", 150*time.Second)

		h.reconnectOnce(ctx, "agent-a", &reconnected)

		// The heartbeat is what brings it back, before the command has finished.
		h.awaitStatus(ctx, run, "running", 60*time.Second)

		final := h.awaitTerminal(ctx, run, 150*time.Second)
		if final.Status != "success" {
			t.Fatalf("status is %q, want success after the heartbeat returned: %s", final.Status, final.Explanation)
		}

		if log := h.log(ctx, run); !strings.Contains(log, "finished anyway") {
			t.Errorf("the recovered Run lost its output: %q", log)
		}

		if runs := h.runsOf(ctx, job); len(runs) != 1 {
			t.Errorf("the Job has %d Runs, want 1: the Server re-leased work it had already given away", len(runs))
		}
		assertStartedOnce(t, h.agentLog(ctx, "agent-a", "/tmp/omj-agent.log"), run)
		if surviving := h.processCount(ctx, "agent-a", "sleep 200"); surviving != 0 {
			t.Errorf("%d processes survived the completed Run, want none", surviving)
		}
	})
}

// Count actual process starts, not just database rows: the same run_id must not
// execute twice even if a duplicated lease would leave only one Run record.
func assertStartedOnce(t *testing.T, log, runID string) {
	t.Helper()
	starts := 0
	for _, line := range strings.Split(log, "\n") {
		var entry struct {
			Message string `json:"msg"`
			RunID   string `json:"run_id"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Message == "run started" && entry.RunID == runID {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("Run %s started %d processes, want exactly one", runID, starts)
	}
}

// assertLinesInOrder checks that a log carries every line the command printed, exactly
// once each and in the order they were written. A replay after a disconnection is the
// one place where duplicates or reordering would show up.
func assertLinesInOrder(t *testing.T, log string, count int) {
	t.Helper()

	seen := make([]int, 0, count)

	for _, line := range strings.Split(log, "\n") {
		var number int

		if _, err := fmt.Sscanf(strings.TrimSpace(line), "line %d", &number); err == nil {
			seen = append(seen, number)
		}
	}

	if len(seen) != count {
		t.Fatalf("the log carries %d lines, want %d: %q", len(seen), count, excerptOf(log))
	}

	for i, number := range seen {
		if number != i+1 {
			t.Fatalf("line %d of the log reads %d: the output is out of order or duplicated", i+1, number)
		}
	}
}

func excerptOf(text string) string {
	const limit = 400

	if len(text) <= limit {
		return text
	}

	return text[:limit] + "…"
}
