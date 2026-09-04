package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ohmyjob/omj-agent/internal/version"
)

func TestRun(t *testing.T) {
	t.Setenv("OMJ_CONFIG_DIR", t.TempDir())
	t.Setenv("OMJ_STATE_DIR", t.TempDir())

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "no arguments", args: nil, wantCode: ExitUsage, wantStderr: "Usage: omj-agent"},
		{name: "help", args: []string{"help"}, wantCode: ExitOK, wantStdout: "Usage: omj-agent"},
		{name: "help flag", args: []string{"--help"}, wantCode: ExitOK, wantStdout: "Usage: omj-agent"},
		{name: "unknown command", args: []string{"bogus"}, wantCode: ExitUsage, wantStderr: `unknown command "bogus"`},
		{name: "enroll without flags", args: []string{"enroll"}, wantCode: ExitUsage, wantStderr: "--server and --token are required"},
		{name: "run before enrollment", args: []string{"run"}, wantCode: ExitError, wantStderr: "agent.conf"},
		{name: "status before enrollment", args: []string{"status"}, wantCode: ExitOK, wantStdout: "not enrolled"},
		{name: "doctor before enrollment", args: []string{"doctor"}, wantCode: ExitError, wantStdout: "FAIL  configuration"},
		{name: "version", args: []string{"version"}, wantCode: ExitOK, wantStdout: "omj-agent "},
		{name: "version with an unknown flag", args: []string{"version", "--bogus"}, wantCode: ExitUsage, wantStderr: "flag provided but not defined"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			got := Run(tt.args, &stdout, &stderr)

			if got != tt.wantCode {
				t.Errorf("Run(%v) = %d, want %d", tt.args, got, tt.wantCode)
			}

			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tt.wantStdout)
			}

			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func TestVersionOutput(t *testing.T) {
	previous := [3]string{version.Version, version.Commit, version.Date}
	version.Version, version.Commit, version.Date = "1.2.3", "abc1234", "2026-09-04T12:00:00Z"
	t.Cleanup(func() { version.Version, version.Commit, version.Date = previous[0], previous[1], previous[2] })

	var stdout, stderr bytes.Buffer

	if got := Run([]string{"version"}, &stdout, &stderr); got != ExitOK {
		t.Fatalf("Run(version) = %d, want %d (stderr %q)", got, ExitOK, stderr.String())
	}

	if want := "omj-agent 1.2.3 (abc1234, 2026-09-04T12:00:00Z) protocol 1\n"; stdout.String() != want {
		t.Errorf("stdout = %q, want %q", stdout.String(), want)
	}
}
