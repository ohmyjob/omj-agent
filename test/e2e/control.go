//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// networkName matches the network the compose project declares, which is what a test
// disconnects a container from to simulate a Machine losing its link.
const networkName = "omj-e2e-harness"

// containerID resolves a compose service to the container a docker command needs.
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

// disconnect cuts a Machine off the way a network failure would: the Agent keeps
// running and its processes keep going, it simply cannot reach the Server.
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

// processCount counts matching processes inside a container. The bracket around the
// first character keeps grep from counting its own command line, and busybox ps prints
// the full argument list, so this works on the Alpine image the Agent runs in.
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

// cancel asks for cancellation through the page an operator uses.
func (h *harness) cancel(ctx context.Context, run string) {
	h.t.Helper()

	if _, err := h.client.post(ctx, fmt.Sprintf("/runs/%s/cancellation", run), map[string]any{}); err != nil {
		h.t.Fatalf("request cancellation of %s: %v", run, err)
	}
}

// awaitStatus waits for one particular state, which a scenario needs when the state it
// cares about is not terminal, such as a Run reaching running before it is interrupted.
func (h *harness) awaitStatus(ctx context.Context, run, status string, within time.Duration) runProps {
	h.t.Helper()

	var seen runProps

	eventually(h.t, within, fmt.Sprintf("Run %s reaching %s", run, status), func() (bool, string) {
		seen = h.runPage(ctx, run)

		return seen.Status == status, "status is " + seen.Status
	})

	return seen
}

// awaitMachineOffline waits for the Server to notice a Machine has stopped reporting.
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

// machine reads one Machine from the list page.
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

// dueNextMinute returns a cron expression that fires once, at a minute far enough ahead
// that the Job exists and its Machine has been seen offline before the scheduler claims
// it. A single occurrence per hour keeps a scenario from absorbing later ones by accident.
func dueNextMinute(margin time.Duration) (expression string, due time.Time) {
	due = time.Now().UTC().Add(margin).Truncate(time.Minute).Add(time.Minute)

	return fmt.Sprintf("%d * * * *", due.Minute()), due
}

// awaitRunOfJob waits for the scheduler to produce a Run for a Job and returns it.
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

// handOverDirectory gives a scratch configuration directory to the service user. Enroll
// runs as root and hands the two files over, but the directory it creates stays root's,
// so the daemon could not otherwise read its own configuration.
func (h *harness) handOverDirectory(ctx context.Context, service, directory string) {
	h.t.Helper()

	if _, err := h.compose(ctx, "exec", "-T", service, "chown", "ohmyjob:ohmyjob", directory); err != nil {
		h.t.Fatalf("hand %s on %s to the service user: %v", directory, service, err)
	}
}

// startAgentIn runs a daemon against its own configuration directory, with extra
// environment of the caller's choosing, and keeps its log where a scenario can read it.
// It is how a scenario runs a second Agent in a container that already has one, and it
// runs as the service user because that is who owns the credential.
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

// agentLog reads what a daemon has written so far.
func (h *harness) agentLog(ctx context.Context, service, path string) string {
	h.t.Helper()

	output, err := h.compose(ctx, "exec", "-T", service, "cat", path)
	if err != nil {
		return ""
	}

	return output
}
