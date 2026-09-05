//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	adminEmail    = "e2e@example.com"
	adminPassword = "correct-horse-battery"
	serverURLName = "http://server:8080"
)

type harness struct {
	t       *testing.T
	dir     string
	baseURL string
	client  *inertiaClient
}

var tokenPattern = regexp.MustCompile(`omj_enroll_[A-Za-z0-9]{32}`)

func start(t *testing.T) *harness {
	t.Helper()

	requireDocker(t)

	dir := directory(t)
	h := &harness{t: t, dir: dir, baseURL: "http://127.0.0.1:" + port()}

	t.Cleanup(func() {
		if t.Failed() {
			h.dumpLogs()
		}

		_, _ = h.compose(context.WithoutCancel(context.Background()), "down", "--volumes", "--remove-orphans")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if _, err := h.compose(ctx, "up", "--detach", "--build", "--wait"); err != nil {
		t.Fatalf("bring the harness up: %v", err)
	}

	if _, err := h.artisan(ctx, "omj:install", "--name=E2E Admin", "--email="+adminEmail, "--password="+adminPassword); err != nil {
		t.Fatalf("create the administrator: %v", err)
	}

	client, err := newInertiaClient(h.baseURL)
	if err != nil {
		t.Fatalf("build the client: %v", err)
	}

	if err := client.login(ctx, adminEmail, adminPassword); err != nil {
		t.Fatalf("sign in: %v", err)
	}

	h.client = client

	return h
}

func requireDocker(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if err := exec.CommandContext(ctx, "docker", "info").Run(); err != nil {
		t.Fatalf("Docker is not available: %v. Start Docker and run the suite again.", err)
	}
}

func directory(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate the harness directory")
	}

	return filepath.Dir(file)
}

func port() string {
	if value := os.Getenv("OMJ_E2E_PORT"); value != "" {
		return value
	}

	return "8210"
}

func (h *harness) compose(ctx context.Context, args ...string) (string, error) {
	return h.run(ctx, "docker", append([]string{"compose"}, args...)...)
}

func (h *harness) run(ctx context.Context, name string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = h.dir
	command.Env = os.Environ()

	var out, errOut bytes.Buffer
	command.Stdout = &out
	command.Stderr = &errOut

	if err := command.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}

	return out.String(), nil
}

func (h *harness) artisan(ctx context.Context, args ...string) (string, error) {
	return h.compose(ctx, append([]string{"exec", "-T", "server", "php", "artisan"}, args...)...)
}

func (h *harness) enrollmentToken(ctx context.Context) (string, error) {
	output, err := h.artisan(ctx, "omj:enrollment-token")
	if err != nil {
		return "", err
	}

	token := tokenPattern.FindString(output)
	if token == "" {
		return "", errors.New("no enrollment token in the command output")
	}

	return token, nil
}

// enroll runs the real enroll command inside an Agent container. It runs as root because
// only root may write into /etc/ohmyjob, and hands the files to the service user.
func (h *harness) enroll(ctx context.Context, service, name, token string) (string, error) {
	return h.compose(ctx, "exec", "-T", service, "omj-agent", "enroll",
		"--server", serverURLName,
		"--token", token,
		"--name", name,
		"--user", "ohmyjob",
		"--insecure-http",
	)
}

// enrollInto enrolls with its own configuration and state directories, so a token
// scenario never disturbs the Agent already running in that container.
func (h *harness) enrollInto(ctx context.Context, service, directory, name, token string) (string, error) {
	return h.compose(ctx, "exec", "-T",
		"-e", "OMJ_CONFIG_DIR="+directory,
		"-e", "OMJ_STATE_DIR="+directory,
		service, "omj-agent", "enroll",
		"--server", serverURLName,
		"--token", token,
		"--name", name,
		"--insecure-http",
	)
}

func exitCode(err error) int {
	var exit *exec.ExitError

	if errors.As(err, &exit) {
		return exit.ExitCode()
	}

	return -1
}

func (h *harness) startAgent(ctx context.Context, service string) error {
	_, err := h.compose(ctx, "exec", "-d", "-u", "ohmyjob", service,
		"sh", "-c", "omj-agent run --log-format json >> /tmp/omj-agent.log 2>&1")

	return err
}

func (h *harness) enrolledAgent(ctx context.Context, service, name string) {
	h.t.Helper()

	token, err := h.enrollmentToken(ctx)
	if err != nil {
		h.t.Fatalf("issue a token for %s: %v", name, err)
	}

	if _, err := h.enroll(ctx, service, name, token); err != nil {
		h.t.Fatalf("enroll %s: %v", name, err)
	}

	if err := h.startAgent(ctx, service); err != nil {
		h.t.Fatalf("start the daemon on %s: %v", name, err)
	}
}

func (h *harness) dumpLogs() {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 30*time.Second)
	defer cancel()

	if output, err := h.compose(ctx, "logs", "--tail", "80", "server"); err == nil {
		h.t.Logf("server log:\n%s", output)
	}

	for _, service := range []string{"agent-a", "agent-b"} {
		output, err := h.compose(ctx, "exec", "-T", service, "cat", "/tmp/omj-agent.log")
		if err != nil {
			continue
		}

		h.t.Logf("%s log:\n%s", service, tail(output, 40))
	}
}

func tail(text string, lines int) string {
	all := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(all) <= lines {
		return text
	}

	return strings.Join(all[len(all)-lines:], "\n")
}

func eventually(t *testing.T, within time.Duration, what string, condition func() (bool, string)) {
	t.Helper()

	deadline := time.Now().Add(within)
	reason := "never checked"

	for time.Now().Before(deadline) {
		ok, why := condition()
		if ok {
			return
		}

		reason = why

		time.Sleep(time.Second)
	}

	t.Fatalf("%s did not happen within %s: %s", what, within, reason)
}
