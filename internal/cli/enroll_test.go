package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ohmyjob/omj-agent/internal/enroll"
)

const (
	testToken      = "omj_enroll_aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY"
	testCredential = "omj_agent_K7fP2mQ9xR4tW1yZ6bN3vC8hJ5lD0sA2eG4iU7oY9pT1rF3k"
)

func TestEnrollFlags(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no flags", args: nil, wantCode: ExitUsage, wantStderr: "--server and --token are required"},
		{name: "server only", args: []string{"--server", "https://omj.example.com"}, wantCode: ExitUsage, wantStderr: "--server and --token are required"},
		{name: "help", args: []string{"--help"}, wantCode: ExitOK, wantStderr: "-token"},
		{name: "unknown flag", args: []string{"--bogus"}, wantCode: ExitUsage, wantStderr: "flag provided but not defined"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			called := false
			cmd := enrollCommand{enroll: func(context.Context, enroll.Options) (enroll.Result, error) {
				called = true

				return enroll.Result{}, nil
			}}

			got := cmd.run(tt.args, &stdout, &stderr)

			if got != tt.wantCode {
				t.Errorf("run(%v) = %d, want %d", tt.args, got, tt.wantCode)
			}

			if called {
				t.Error("enroll ran without the required flags")
			}

			if !strings.Contains(stdout.String(), tt.wantStdout) || !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stdout = %q, stderr = %q, want %q and %q", stdout.String(), stderr.String(), tt.wantStdout, tt.wantStderr)
			}
		})
	}
}

func TestEnrollPassesTheFlagsAndPrintsTheOutcome(t *testing.T) {
	var (
		stdout, stderr bytes.Buffer
		received       enroll.Options
	)

	cmd := enrollCommand{enroll: func(_ context.Context, opts enroll.Options) (enroll.Result, error) {
		received = opts

		return enroll.Result{
			MachineID:      "0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f11",
			ConfigFile:     "/etc/ohmyjob/agent.conf",
			CredentialFile: "/etc/ohmyjob/agent.credential",
			Owner:          "ohmyjob",
			NextStep:       "systemctl enable --now omj-agent",
		}, nil
	}}

	got := cmd.run([]string{"--server", "http://omj.example.com", "--token", testToken, "--name", "nas01", "--insecure-http", "--user", "svc", "--force"}, &stdout, &stderr)

	if got != ExitOK {
		t.Fatalf("run() = %d, want %d (stderr %q)", got, ExitOK, stderr.String())
	}

	if received.ServerURL != "http://omj.example.com" || received.Token != testToken || received.Name != "nas01" || !received.InsecureHTTP || received.User != "svc" || !received.Force {
		t.Errorf("options = %+v, want every flag passed through", received)
	}

	if received.Logger == nil {
		t.Error("options carry no logger")
	}

	for _, line := range []string{"Enrolled as machine 0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f11.", "owned by ohmyjob", "Next: systemctl enable --now omj-agent"} {
		if !strings.Contains(stdout.String(), line) {
			t.Errorf("stdout = %q, want it to contain %q", stdout.String(), line)
		}
	}

	if !strings.Contains(stderr.String(), "Warning: --insecure-http") {
		t.Errorf("stderr = %q, want the plain-HTTP warning", stderr.String())
	}

	if strings.Contains(stdout.String()+stderr.String(), testToken) {
		t.Error("the token was printed")
	}
}

func TestEnrollExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
	}{
		{name: "invalid input", err: &enroll.Error{Reason: enroll.ReasonInvalidInput, Message: "bad input"}, wantCode: ExitUsage},
		{name: "already enrolled", err: &enroll.Error{Reason: enroll.ReasonAlreadyEnrolled, Message: "already enrolled"}, wantCode: ExitAlreadyEnrolled},
		{name: "token invalid", err: &enroll.Error{Reason: enroll.ReasonTokenInvalid, Message: "token invalid"}, wantCode: ExitTokenInvalid},
		{name: "token expired", err: &enroll.Error{Reason: enroll.ReasonTokenExpired, Message: "token expired"}, wantCode: ExitTokenExpired},
		{name: "unsupported os", err: &enroll.Error{Reason: enroll.ReasonUnsupportedOS, Message: "unsupported os"}, wantCode: ExitUnsupportedOS},
		{name: "version rejected", err: &enroll.Error{Reason: enroll.ReasonVersionRejected, Message: "version rejected"}, wantCode: ExitVersionRejected},
		{name: "throttled", err: &enroll.Error{Reason: enroll.ReasonThrottled, Message: "throttled"}, wantCode: ExitThrottled},
		{name: "unreachable", err: &enroll.Error{Reason: enroll.ReasonUnreachable, Message: "unreachable"}, wantCode: ExitUnreachable},
		{name: "permission", err: &enroll.Error{Reason: enroll.ReasonPermission, Message: "permission"}, wantCode: ExitPermission},
		{name: "unknown reason", err: &enroll.Error{Reason: enroll.ReasonUnknown, Message: "something else"}, wantCode: ExitError},
		{name: "plain error", err: errors.New("boom"), wantCode: ExitError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			cmd := enrollCommand{enroll: func(context.Context, enroll.Options) (enroll.Result, error) {
				return enroll.Result{}, tt.err
			}}

			got := cmd.run([]string{"--server", "https://omj.example.com", "--token", testToken}, &stdout, &stderr)

			if got != tt.wantCode {
				t.Errorf("run() = %d, want %d", got, tt.wantCode)
			}

			if !strings.Contains(stderr.String(), "omj-agent enroll: "+tt.err.Error()) {
				t.Errorf("stderr = %q, want the message %q", stderr.String(), tt.err.Error())
			}

			if strings.Contains(stdout.String()+stderr.String(), testToken) || strings.Contains(stdout.String()+stderr.String(), testCredential) {
				t.Error("a secret was printed")
			}
		})
	}
}
