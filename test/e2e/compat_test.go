//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestProtocolRejection proves the compatibility promise: an Agent speaking a protocol
// version the Server does not support is refused, the Machine says so, and no work is
// ever handed to it. The Agent is the real binary, built with the e2e tag so its
// protocol version can be forced; a release binary has no such switch.
func TestProtocolRejection(t *testing.T) {
	h := start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	const (
		name      = "speaks-99"
		directory = "/tmp/protocol-99"
		logPath   = "/tmp/protocol-99.log"
	)

	token, err := h.enrollmentToken(ctx)
	if err != nil {
		t.Fatalf("issue a token: %v", err)
	}

	// Enrollment happens at the supported version: the Server would refuse it outright
	// otherwise and there would be no Machine to observe.
	if _, err := h.enrollInto(ctx, "agent-a", directory, name, token); err != nil {
		t.Fatalf("enroll %s: %v", name, err)
	}

	h.handOverDirectory(ctx, "agent-a", directory)

	machines := h.awaitOnlineMachines(ctx, 1)

	machine, ok := machines[name]
	if !ok {
		t.Fatalf("%s did not appear on the Machines page", name)
	}

	if err := h.startAgentIn(ctx, "agent-a", directory, logPath, map[string]string{
		"OMJ_TEST_PROTOCOL_VERSION": "99",
	}); err != nil {
		t.Fatalf("start the daemon that speaks 99: %v", err)
	}

	t.Run("the machine reports the agent as incompatible", func(t *testing.T) {
		eventually(t, 60*time.Second, "the Machine being marked incompatible", func() (bool, string) {
			current, ok := h.machine(ctx, name)
			if !ok {
				return false, name + " is not listed"
			}

			if !current.Incompatible {
				return false, "the Machine is still considered compatible"
			}

			if current.LastError == nil || *current.LastError == "" {
				return false, "the Machine carries no error to show"
			}

			return true, ""
		})
	})

	t.Run("the agent logs which versions the server speaks", func(t *testing.T) {
		eventually(t, 60*time.Second, "the Agent reporting the mismatch", func() (bool, string) {
			log := h.agentLog(ctx, "agent-a", logPath)

			if !strings.Contains(log, "supported_protocol_versions") {
				return false, "the log does not name the versions the Server supports"
			}

			return true, ""
		})
	})

	t.Run("no work is handed to an agent the server cannot talk to", func(t *testing.T) {
		job := h.createJob(ctx, jobRequest{
			Name:     "Never dispatched",
			Machine:  machine.ID,
			Command:  "echo unreachable",
			Schedule: "0 8 * * *",
		})

		run := h.runNow(ctx, job)

		// Long enough for several polls: an Agent that could be leased to would have
		// taken this Run within seconds.
		time.Sleep(20 * time.Second)

		current := h.runPage(ctx, run)
		if current.Status != "queued" {
			t.Fatalf("the Run is %q, want queued: the Server leased work to an Agent it refuses to talk to", current.Status)
		}
	})
}
