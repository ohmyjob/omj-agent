//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const networkName = "omj-e2e-harness"

func (h *harness) containerID(ctx context.Context, service string) string {
	h.t.Helper()

	output, err := h.compose(ctx, "ps", "--quiet", service)
	if err != nil {
		h.t.Fatalf("find the container for %s: %v", service, err)
	}

	id := strings.TrimSpace(output)
	if id == "" {
		h.t.Fatalf("no container running for %s", service)
	}

	return id
}

func (h *harness) disconnect(ctx context.Context, service string) {
	h.t.Helper()

	if _, err := h.run(ctx, "docker", "network", "disconnect", networkName, h.containerID(ctx, service)); err != nil {
		h.t.Fatalf("disconnect %s: %v", service, err)
	}
}

func (h *harness) connect(ctx context.Context, service string) {
	h.t.Helper()

	if _, err := h.run(ctx, "docker", "network", "connect", networkName, h.containerID(ctx, service)); err != nil {
		h.t.Fatalf("reconnect %s: %v", service, err)
	}
}

// reconnectOnce puts a Machine back exactly once, so a deferred cleanup after an early
// failure cannot fight the reconnect a scenario already made.
func (h *harness) reconnectOnce(ctx context.Context, service string, done *bool) {
	if *done {
		return
	}

	*done = true

	h.connect(ctx, service)
}

// Bracket the first character so grep cannot match its own command line.
func (h *harness) processCount(ctx context.Context, service, pattern string) int {
	h.t.Helper()

	if pattern == "" {
		h.t.Fatal("processCount needs a pattern")
	}

	bracketed := "[" + pattern[:1] + "]" + pattern[1:]

	output, err := h.compose(ctx, "exec", "-T", service, "sh", "-c",
		fmt.Sprintf("ps | grep -c %q || true", bracketed))
	if err != nil {
		h.t.Fatalf("count %q on %s: %v", pattern, service, err)
	}

	count, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		h.t.Fatalf("read the process count on %s from %q: %v", service, output, err)
	}

	return count
}

func (h *harness) cancel(ctx context.Context, run string) {
	h.t.Helper()

	if _, err := h.client.post(ctx, fmt.Sprintf("/runs/%s/cancellation", run), map[string]any{}); err != nil {
		h.t.Fatalf("request cancellation of %s: %v", run, err)
	}
}

func (h *harness) awaitStatus(ctx context.Context, run, status string, within time.Duration) runProps {
	h.t.Helper()

	var seen runProps

	eventually(h.t, within, fmt.Sprintf("Run %s reaching %s", run, status), func() (bool, string) {
		seen = h.runPage(ctx, run)

		return seen.Status == status, "status is " + seen.Status
	})

	return seen
}

func (h *harness) awaitMachineOffline(ctx context.Context, name string, within time.Duration) {
	h.t.Helper()

	eventually(h.t, within, name+" going offline", func() (bool, string) {
		machine, ok := h.machine(ctx, name)
		if !ok {
			return false, name + " is not listed"
		}

		return !machine.IsOnline, name + " is still online"
	})
}

func (h *harness) machine(ctx context.Context, name string) (machineProps, bool) {
	h.t.Helper()

	page, err := h.client.get(ctx, "/machines")
	if err != nil {
		h.t.Fatalf("open the Machines page: %v", err)
	}

	listed, err := props[machineListProps](page)
	if err != nil {
		h.t.Fatalf("read the Machines page: %v", err)
	}

	for _, machine := range listed.Machines.Data {
		if machine.Name == name {
			return machine, true
		}
	}

	return machineProps{}, false
}

// Leave time for setup before the due minute; hourly recurrence avoids accidental coalescing.
func dueNextMinute(margin time.Duration) (expression string, due time.Time) {
	due = time.Now().UTC().Add(margin).Truncate(time.Minute).Add(time.Minute)

	return fmt.Sprintf("%d * * * *", due.Minute()), due
}

func (h *harness) awaitRunOfJob(ctx context.Context, job string, within time.Duration) runProps {
	h.t.Helper()

	var first runProps

	eventually(h.t, within, "a Run of Job "+job, func() (bool, string) {
		runs := h.runsOf(ctx, job)
		if len(runs) == 0 {
			return false, "no Runs yet"
		}

		first = runs[0]

		return true, ""
	})

	return first
}

// Enrollment transfers file ownership but leaves the directory owned by root.
func (h *harness) handOverDirectory(ctx context.Context, service, directory string) {
	h.t.Helper()

	if _, err := h.compose(ctx, "exec", "-T", service, "chown", "ohmyjob:ohmyjob", directory); err != nil {
		h.t.Fatalf("hand %s on %s to the service user: %v", directory, service, err)
	}
}

func (h *harness) startAgentIn(ctx context.Context, service, directory, logPath string, environment map[string]string) error {
	args := []string{"exec", "-d", "-u", "ohmyjob", "-e", "OMJ_CONFIG_DIR=" + directory, "-e", "OMJ_STATE_DIR=" + directory}

	for name, value := range environment {
		args = append(args, "-e", name+"="+value)
	}

	args = append(args, service, "sh", "-c",
		fmt.Sprintf("omj-agent run --log-format json >> %s 2>&1", logPath))

	_, err := h.compose(ctx, args...)

	return err
}

func (h *harness) agentLog(ctx context.Context, service, path string) string {
	h.t.Helper()

	output, err := h.compose(ctx, "exec", "-T", service, "cat", path)
	if err != nil {
		return ""
	}

	return output
}
