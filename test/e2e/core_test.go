//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/cli"
)

type machineListProps struct {
	Machines struct {
		Data []machineProps `json:"data"`
	} `json:"machines"`
}

type machineProps struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	AgentUser string `json:"agent_user"`
	IsOnline  bool   `json:"is_online"`
}

type jobFormProps struct {
	Machines []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"machines"`
	DefaultTimezone string `json:"default_timezone"`
}

type jobPageProps struct {
	Job struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"job"`
}

type runPageProps struct {
	Run runProps `json:"run"`
}

type runProps struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Trigger    string `json:"trigger"`
	ExitCode   *int   `json:"exit_code"`
	IsTerminal bool   `json:"is_terminal"`
	Machine    struct {
		Name string `json:"name"`
	} `json:"machine"`
}

type runsListProps struct {
	Runs struct {
		Data []runProps `json:"data"`
	} `json:"runs"`
}

type logWindow struct {
	Status   string `json:"status"`
	Text     string `json:"text"`
	Terminal bool   `json:"terminal"`
}

// TestCoreScenarios runs the whole path once against one harness: two Agents enrol,
// Jobs run by hand and on a schedule, and enrollment tokens are refused when they should
// be. They share a harness because bringing the images up costs far more than the tests.
func TestCoreScenarios(t *testing.T) {
	h := start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	h.enrolledAgent(ctx, "agent-a", "agent-a")
	h.enrolledAgent(ctx, "agent-b", "agent-b")

	machines := h.awaitOnlineMachines(ctx, 2)

	t.Run("both agents enroll and report themselves", func(t *testing.T) {
		for _, name := range []string{"agent-a", "agent-b"} {
			machine, ok := machines[name]
			if !ok {
				t.Fatalf("%s is not on the Machines page", name)
			}

			if machine.AgentUser != "ohmyjob" {
				t.Errorf("%s runs as %q, want ohmyjob", name, machine.AgentUser)
			}

			if machine.OS != "linux" {
				t.Errorf("%s reports os %q, want linux", name, machine.OS)
			}

			if machine.Arch == "" {
				t.Errorf("%s reports no architecture", name)
			}
		}
	})

	t.Run("a job run by hand succeeds with its output", func(t *testing.T) {
		job := h.createJob(ctx, jobRequest{
			Name:     "Greets",
			Machine:  machines["agent-a"].ID,
			Command:  "echo hello from the harness",
			Schedule: "0 3 * * *",
		})

		run := h.runNow(ctx, job)
		final := h.awaitTerminal(ctx, run, 90*time.Second)

		if final.Status != "success" {
			t.Fatalf("status is %q, want success", final.Status)
		}

		if final.ExitCode == nil || *final.ExitCode != 0 {
			t.Errorf("exit code is %v, want 0", final.ExitCode)
		}

		if final.Machine.Name != "agent-a" {
			t.Errorf("ran on %q, want agent-a", final.Machine.Name)
		}

		if log := h.log(ctx, run); !strings.Contains(log, "hello from the harness") {
			t.Errorf("log does not carry the output: %q", log)
		}
	})

	t.Run("a failing command reports its exit code and stderr", func(t *testing.T) {
		job := h.createJob(ctx, jobRequest{
			Name:     "Fails",
			Machine:  machines["agent-b"].ID,
			Command:  "echo trouble ahead >&2; exit 2",
			Schedule: "0 4 * * *",
		})

		run := h.runNow(ctx, job)
		final := h.awaitTerminal(ctx, run, 90*time.Second)

		if final.Status != "failed" {
			t.Fatalf("status is %q, want failed", final.Status)
		}

		if final.ExitCode == nil || *final.ExitCode != 2 {
			t.Errorf("exit code is %v, want 2", final.ExitCode)
		}

		if log := h.log(ctx, run); !strings.Contains(log, "trouble ahead") {
			t.Errorf("log does not carry the standard error: %q", log)
		}
	})

	t.Run("an every-minute job dispatches on its own", func(t *testing.T) {
		job := h.createJob(ctx, jobRequest{
			Name:     "Ticks",
			Machine:  machines["agent-a"].ID,
			Command:  "echo scheduled",
			Schedule: "* * * * *",
		})

		var scheduled runProps

		eventually(t, 100*time.Second, "a scheduled Run", func() (bool, string) {
			for _, run := range h.runsOf(ctx, job) {
				if run.Trigger == "scheduled" {
					scheduled = run

					return true, ""
				}
			}

			return false, "no Run with a scheduled trigger yet"
		})

		final := h.awaitTerminal(ctx, scheduled.ID, 90*time.Second)
		if final.Status != "success" {
			t.Errorf("the scheduled Run ended %q, want success", final.Status)
		}
	})

	t.Run("a used token is refused", func(t *testing.T) {
		token, err := h.enrollmentToken(ctx)
		if err != nil {
			t.Fatalf("issue a token: %v", err)
		}

		if _, err := h.enrollInto(ctx, "agent-a", "/tmp/reuse-first", "reused-first", token); err != nil {
			t.Fatalf("first enrollment: %v", err)
		}

		output, err := h.enrollInto(ctx, "agent-a", "/tmp/reuse-second", "reused-second", token)
		if err == nil {
			t.Fatalf("the second enrollment was accepted: %s", output)
		}

		if code := exitCode(err); code != cli.ExitTokenInvalid {
			t.Errorf("exit code is %d, want %d for a token the Server refuses", code, cli.ExitTokenInvalid)
		}
	})

	t.Run("an expired token is refused", func(t *testing.T) {
		token, err := h.shortLivedToken(ctx)
		if err != nil {
			t.Fatalf("issue a short-lived token: %v", err)
		}

		time.Sleep(2 * time.Second)

		output, err := h.enrollInto(ctx, "agent-b", "/tmp/expired", "expired", token)
		if err == nil {
			t.Fatalf("an expired token was accepted: %s", output)
		}

		if code := exitCode(err); code != cli.ExitTokenExpired {
			t.Errorf("exit code is %d, want %d for an expired token", code, cli.ExitTokenExpired)
		}
	})
}

type jobRequest struct {
	Name     string
	Machine  string
	Command  string
	Schedule string
}

func (h *harness) createJob(ctx context.Context, request jobRequest) string {
	h.t.Helper()

	form, err := h.client.get(ctx, "/jobs/create")
	if err != nil {
		h.t.Fatalf("open the Job form: %v", err)
	}

	options, err := props[jobFormProps](form)
	if err != nil {
		h.t.Fatalf("read the Job form: %v", err)
	}

	created, err := h.client.post(ctx, "/jobs", map[string]any{
		"name":              request.Name,
		"machine_id":        request.Machine,
		"command":           request.Command,
		"cron_expression":   request.Schedule,
		"timezone":          options.DefaultTimezone,
		"enabled":           true,
		"shell":             nil,
		"working_directory": nil,
		"timeout_seconds":   120,
		"missed_policy":     "run_late",
		"grace_seconds":     3600,
	})
	if err != nil {
		h.t.Fatalf("create the Job %q: %v", request.Name, err)
	}

	page, err := props[jobPageProps](created)
	if err != nil {
		h.t.Fatalf("read the Job page: %v", err)
	}

	return page.Job.ID
}

func (h *harness) runNow(ctx context.Context, job string) string {
	h.t.Helper()

	started, err := h.client.post(ctx, fmt.Sprintf("/jobs/%s/runs", job), map[string]any{})
	if err != nil {
		h.t.Fatalf("run the Job now: %v", err)
	}

	page, err := props[runPageProps](started)
	if err != nil {
		h.t.Fatalf("read the Run page: %v", err)
	}

	return page.Run.ID
}

func (h *harness) runPage(ctx context.Context, id string) runProps {
	h.t.Helper()

	page, err := h.client.get(ctx, "/runs/"+id)
	if err != nil {
		h.t.Fatalf("open the Run page: %v", err)
	}

	shown, err := props[runPageProps](page)
	if err != nil {
		h.t.Fatalf("read the Run page: %v", err)
	}

	return shown.Run
}

func (h *harness) runsOf(ctx context.Context, job string) []runProps {
	h.t.Helper()

	page, err := h.client.get(ctx, "/runs?job="+job)
	if err != nil {
		h.t.Fatalf("open the Runs list: %v", err)
	}

	listed, err := props[runsListProps](page)
	if err != nil {
		h.t.Fatalf("read the Runs list: %v", err)
	}

	return listed.Runs.Data
}

func (h *harness) awaitTerminal(ctx context.Context, id string, within time.Duration) runProps {
	h.t.Helper()

	var final runProps

	eventually(h.t, within, "Run "+id+" finishing", func() (bool, string) {
		final = h.runPage(ctx, id)

		return final.IsTerminal, "status is " + final.Status
	})

	return final
}

func (h *harness) awaitOnlineMachines(ctx context.Context, count int) map[string]machineProps {
	h.t.Helper()

	online := map[string]machineProps{}

	eventually(h.t, 30*time.Second, fmt.Sprintf("%d Machines coming online", count), func() (bool, string) {
		page, err := h.client.get(ctx, "/machines")
		if err != nil {
			return false, err.Error()
		}

		listed, err := props[machineListProps](page)
		if err != nil {
			return false, err.Error()
		}

		online = map[string]machineProps{}

		for _, machine := range listed.Machines.Data {
			if machine.IsOnline {
				online[machine.Name] = machine
			}
		}

		return len(online) >= count, fmt.Sprintf("%d of %d online", len(online), count)
	})

	return online
}

func (h *harness) log(ctx context.Context, run string) string {
	h.t.Helper()

	var window logWindow

	if err := h.client.json(ctx, fmt.Sprintf("/runs/%s/log?offset=0", run), &window); err != nil {
		h.t.Fatalf("read the Run log: %v", err)
	}

	return window.Text
}

// shortLivedToken issues a token that expires almost at once, so the suite can watch
// one expire instead of waiting out the real fifteen minutes. The image caches its
// configuration at boot, so the cache has to come down for the override to be read and
// go back up afterwards; the override lives only in this one command's environment.
func (h *harness) shortLivedToken(ctx context.Context) (string, error) {
	if _, err := h.artisan(ctx, "config:clear"); err != nil {
		return "", fmt.Errorf("clear the cached configuration: %w", err)
	}

	defer func() {
		if _, err := h.artisan(context.WithoutCancel(ctx), "config:cache"); err != nil {
			h.t.Logf("restore the cached configuration: %v", err)
		}
	}()

	output, err := h.compose(ctx, "exec", "-T", "-e", "OMJ_ENROLLMENT_TOKEN_TTL_SECONDS=1",
		"server", "php", "artisan", "omj:enrollment-token")
	if err != nil {
		return "", err
	}

	token := tokenPattern.FindString(output)
	if token == "" {
		return "", fmt.Errorf("no token in the output: %s", output)
	}

	return token, nil
}
