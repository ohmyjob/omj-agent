//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Hourly schedules isolate scenarios sharing one Machine; only coalescing runs every minute.
// Wait for the Server's offline verdict instead of assuming the sweep's clock alignment.
func TestOfflineScenarios(t *testing.T) {
	h := start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	h.enrolledAgent(ctx, "agent-b", "agent-b")

	machines := h.awaitOnlineMachines(ctx, 1)
	machine := machines["agent-b"].ID

	t.Run("an occurrence a skipping job misses while its machine is away is recorded at once", func(t *testing.T) {
		reconnected := false
		h.disconnect(ctx, "agent-b")
		defer h.reconnectOnce(context.WithoutCancel(ctx), "agent-b", &reconnected)

		h.awaitMachineOffline(ctx, "agent-b", 60*time.Second)

		schedule, _ := dueNextMinute(10 * time.Second)
		job := h.createJob(ctx, jobRequest{
			Name:         "Skips when away",
			Machine:      machine,
			Command:      "echo never runs",
			Schedule:     schedule,
			MissedPolicy: "skip",
		})

		run := h.awaitRunOfJob(ctx, job, 90*time.Second)
		final := h.awaitTerminal(ctx, run.ID, 30*time.Second)

		if final.Status != "missed" {
			t.Fatalf("status is %q, want missed: %s", final.Status, final.Explanation)
		}

		if !strings.Contains(final.Explanation, "was offline.") {
			t.Errorf("the Run does not say the Machine was simply offline: %q", final.Explanation)
		}

		h.reconnectOnce(ctx, "agent-b", &reconnected)
	})

	t.Run("an occurrence within the grace period runs late when its machine returns", func(t *testing.T) {
		h.awaitOnlineMachines(ctx, 1)

		reconnected := false
		h.disconnect(ctx, "agent-b")
		defer h.reconnectOnce(context.WithoutCancel(ctx), "agent-b", &reconnected)

		h.awaitMachineOffline(ctx, "agent-b", 60*time.Second)

		schedule, due := dueNextMinute(10 * time.Second)
		job := h.createJob(ctx, jobRequest{
			Name:         "Waits for its machine",
			Machine:      machine,
			Command:      "echo ran late",
			Schedule:     schedule,
			GraceSeconds: 300,
		})

		run := h.awaitRunOfJob(ctx, job, 90*time.Second)

		if run.Status != "queued" {
			t.Fatalf("the Run is %q before the Machine returns, want queued: %s", run.Status, run.Explanation)
		}

		// The Server considers a start late only after it is more than a minute overdue.
		if wait := time.Until(due.Add(75 * time.Second)); wait > 0 {
			time.Sleep(wait)
		}

		h.reconnectOnce(ctx, "agent-b", &reconnected)

		final := h.awaitTerminal(ctx, run.ID, 120*time.Second)
		if final.Status != "success" {
			t.Fatalf("status is %q, want success: %s", final.Status, final.Explanation)
		}

		if final.LateBySeconds == nil || *final.LateBySeconds <= 0 {
			t.Errorf("the Run is not marked late: late_by_seconds is %v", final.LateBySeconds)
		}

		if !strings.Contains(final.Explanation, "late because") {
			t.Errorf("the Run page does not explain the delay: %q", final.Explanation)
		}

		wanted := due.Format("2006-01-02T15:04:")
		if final.ScheduledFor == nil || !strings.HasPrefix(*final.ScheduledFor, wanted) {
			t.Errorf("scheduled_for is %v, want the %s occurrence", final.ScheduledFor, wanted)
		}
	})

	t.Run("an occurrence beyond the grace period is given up on", func(t *testing.T) {
		h.awaitOnlineMachines(ctx, 1)

		reconnected := false
		h.disconnect(ctx, "agent-b")
		defer h.reconnectOnce(context.WithoutCancel(ctx), "agent-b", &reconnected)

		h.awaitMachineOffline(ctx, "agent-b", 60*time.Second)

		schedule, _ := dueNextMinute(10 * time.Second)
		job := h.createJob(ctx, jobRequest{
			Name:         "Gives up quickly",
			Machine:      machine,
			Command:      "echo too late",
			Schedule:     schedule,
			GraceSeconds: 60,
		})

		run := h.awaitRunOfJob(ctx, job, 90*time.Second)

		// The grace is a minute and reconcile sweeps every minute, so the Run has two
		// minutes at the outside to pass its expiry and be marked missed.
		final := h.awaitTerminal(ctx, run.ID, 150*time.Second)

		if final.Status != "missed" {
			t.Fatalf("status is %q, want missed: %s", final.Status, final.Explanation)
		}

		if !strings.Contains(final.Explanation, "longer than the") {
			t.Errorf("the Run does not blame the grace period: %q", final.Explanation)
		}

		h.reconnectOnce(ctx, "agent-b", &reconnected)
	})

	t.Run("occurrences missed during an outage are folded into one late run", func(t *testing.T) {
		h.awaitOnlineMachines(ctx, 1)

		reconnected := false
		h.disconnect(ctx, "agent-b")
		defer h.reconnectOnce(context.WithoutCancel(ctx), "agent-b", &reconnected)

		h.awaitMachineOffline(ctx, "agent-b", 60*time.Second)

		job := h.createJob(ctx, jobRequest{
			Name:     "Runs every minute",
			Machine:  machine,
			Command:  "echo caught up",
			Schedule: "* * * * *",
		})

		var queued runProps

		eventually(t, 4*time.Minute, "two occurrences folding into one Run", func() (bool, string) {
			runs := h.runsOf(ctx, job)
			if len(runs) != 1 {
				return false, fmt.Sprintf("the Job has %d Runs, want exactly 1", len(runs))
			}

			queued = runs[0]

			return queued.CoalescedCount >= 2, fmt.Sprintf("coalesced_count is %d", queued.CoalescedCount)
		})

		h.reconnectOnce(ctx, "agent-b", &reconnected)

		final := h.awaitTerminal(ctx, queued.ID, 120*time.Second)
		if final.Status != "success" {
			t.Fatalf("the folded Run ended %q, want success: %s", final.Status, final.Explanation)
		}

		if final.CoalescedCount < 2 {
			t.Errorf("coalesced_count is %d, want at least 2", final.CoalescedCount)
		}
	})
}
