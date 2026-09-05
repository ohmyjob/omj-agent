package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/agent"
	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/state"
	"github.com/ohmyjob/omj-agent/internal/sysinfo"
)

type stubDaemon struct {
	err  error
	runs int
}

func (s *stubDaemon) Run(context.Context) error {
	s.runs++

	return s.err
}

func (s *stubDaemon) Wait() {}

func newRunCommand(paths config.Paths, stub *stubDaemon, captured *agent.Options) runCommand {
	return runCommand{
		paths:          paths,
		loadConfig:     config.Load,
		loadCredential: config.LoadCredential,
		collect: func(context.Context) (sysinfo.Info, error) {
			return sysinfo.Info{Hostname: "nas01", OS: "linux", AgentUser: "ohmyjob", AgentUID: 998}, nil
		},
		loadState: state.Load,
		newAgent: func(opts agent.Options) (daemon, error) {
			if captured != nil {
				*captured = opts
			}

			return stub, nil
		},
	}
}

func TestRunFlags(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{name: "help", args: []string{"--help"}, wantCode: ExitOK, wantStderr: "-log-level"},
		{name: "unknown flag", args: []string{"--bogus"}, wantCode: ExitUsage, wantStderr: "flag provided but not defined"},
		{name: "bad level", args: []string{"--log-level", "loud"}, wantCode: ExitUsage, wantStderr: "--log-level must be debug, info, warn or error"},
		{name: "bad format", args: []string{"--log-format", "xml"}, wantCode: ExitUsage, wantStderr: "--log-format must be text or json"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubDaemon{}
			var stdout, stderr bytes.Buffer

			got := newRunCommand(testPaths(t), stub, nil).run(tt.args, &stdout, &stderr)

			if got != tt.wantCode || !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("run(%v) = %d, stderr %q; want %d containing %q", tt.args, got, stderr.String(), tt.wantCode, tt.wantStderr)
			}

			if stub.runs != 0 {
				t.Error("the agent ran despite the usage error")
			}
		})
	}
}

func TestRunNamesTheMissingFile(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, paths config.Paths)
		want  func(paths config.Paths) string
	}{
		{name: "configuration", setup: func(*testing.T, config.Paths) {}, want: func(paths config.Paths) string { return paths.ConfigFile }},
		{name: "credential", setup: func(t *testing.T, paths config.Paths) { writeConfig(t, paths, enrolledConfig()) }, want: func(paths config.Paths) string { return paths.CredentialFile }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := testPaths(t)
			tt.setup(t, paths)

			var stdout, stderr bytes.Buffer

			if got := newRunCommand(paths, &stubDaemon{}, nil).run(nil, &stdout, &stderr); got != ExitError {
				t.Fatalf("run() = %d, want %d", got, ExitError)
			}

			if !strings.Contains(stderr.String(), tt.want(paths)) {
				t.Errorf("stderr = %q, want it to name %s", stderr.String(), tt.want(paths))
			}
		})
	}
}

func TestRunWiresTheAgent(t *testing.T) {
	paths := testPaths(t)
	writeConfig(t, paths, enrolledConfig())
	writeCredential(t, paths)

	stub := &stubDaemon{}
	var (
		captured       agent.Options
		stdout, stderr bytes.Buffer
	)

	if got := newRunCommand(paths, stub, &captured).run([]string{"--log-format", "json", "--log-level", "debug"}, &stdout, &stderr); got != ExitOK {
		t.Fatalf("run() = %d, want %d (stderr %q)", got, ExitOK, stderr.String())
	}

	if stub.runs != 1 {
		t.Errorf("the agent ran %d times, want once", stub.runs)
	}

	if captured.Config.MachineID != testMachineID || captured.Client == nil || captured.State == nil || captured.Buffer == nil || captured.Logger == nil {
		t.Errorf("options = %+v, want the config, client, state, buffer and logger wired", captured)
	}

	if captured.Runner.MaxTimeout != 259200*time.Second || captured.Info.Hostname != "nas01" {
		t.Errorf("runner max timeout = %s, hostname = %q", captured.Runner.MaxTimeout, captured.Info.Hostname)
	}

	if !captured.Logger.Enabled(t.Context(), -4) {
		t.Error("--log-level debug was not applied")
	}

	captured.Logger.Debug("probe")

	if !strings.HasPrefix(stdout.String(), `{"time"`) {
		t.Errorf("stdout = %q, want JSON log lines", stdout.String())
	}

	if strings.Contains(stdout.String()+stderr.String(), testCredential) {
		t.Error("the credential was printed")
	}
}

func TestRunResolvesTheExecutionUserAllowlist(t *testing.T) {
	tests := []struct {
		name       string
		allowed    []string
		want       []string
		wantCode   int
		wantStderr string
	}{
		{name: "no allowlist is the agent's own user", want: []string{"ohmyjob"}, wantCode: ExitOK},
		{name: "an allowlist naming the agent's own user", allowed: []string{"ohmyjob"}, want: []string{"ohmyjob"}, wantCode: ExitOK},
		{
			name:       "a user who does not exist stops the agent",
			allowed:    []string{"backup"},
			wantCode:   ExitError,
			wantStderr: `run_as_allowed lists "backup", which is not a user on this machine`,
		},
		{
			name:       "a user this agent cannot become stops the agent",
			allowed:    []string{"deploy"},
			wantCode:   ExitError,
			wantStderr: "can only run work as itself",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := testPaths(t)
			cfg := enrolledConfig()
			cfg.RunAsAllowed = tt.allowed
			writeConfig(t, paths, cfg)
			writeCredential(t, paths)

			stub := &stubDaemon{}
			var (
				captured       agent.Options
				stdout, stderr bytes.Buffer
			)

			command := newRunCommand(paths, stub, &captured)
			command.lookupUser = func(name string) (int, error) {
				if name != "deploy" {
					return 0, errors.New("unknown user " + name)
				}

				return 1001, nil
			}

			if got := command.run(nil, &stdout, &stderr); got != tt.wantCode {
				t.Fatalf("run() = %d, want %d (stderr %q)", got, tt.wantCode, stderr.String())
			}

			if tt.wantCode != ExitOK {
				if !strings.Contains(stderr.String(), tt.wantStderr) || !strings.Contains(stderr.String(), paths.ConfigFile) {
					t.Errorf("stderr = %q, want it to name %s and contain %q", stderr.String(), paths.ConfigFile, tt.wantStderr)
				}

				if stub.runs != 0 {
					t.Error("the agent ran despite an allowlist it cannot honour")
				}

				return
			}

			if !reflect.DeepEqual(captured.RunAsAllowed, tt.want) {
				t.Errorf("RunAsAllowed = %#v, want %#v", captured.RunAsAllowed, tt.want)
			}
		})
	}
}

func TestRunUsesTheConfiguredLogLevel(t *testing.T) {
	paths := testPaths(t)
	cfg := enrolledConfig()
	cfg.LogLevel = "error"
	writeConfig(t, paths, cfg)
	writeCredential(t, paths)

	var captured agent.Options
	var stdout, stderr bytes.Buffer

	newRunCommand(paths, &stubDaemon{}, &captured).run(nil, &stdout, &stderr)

	if captured.Logger.Enabled(t.Context(), 4) || captured.Logger.Enabled(t.Context(), 8) != true {
		t.Error("log_level = error from agent.conf was not applied")
	}

	captured.Logger.Error("probe")

	if !strings.HasPrefix(stdout.String(), "time=") {
		t.Errorf("stdout = %q, want text log lines", stdout.String())
	}
}

func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   int
		wantStderr string
	}{
		{name: "clean stop", err: nil, wantCode: ExitOK},
		{name: "forced stop", err: agent.ErrForcedStop, wantCode: ExitForcedStop, wantStderr: "second signal"},
		{name: "other error", err: errors.New("boom"), wantCode: ExitError, wantStderr: "boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := testPaths(t)
			writeConfig(t, paths, enrolledConfig())
			writeCredential(t, paths)

			var stdout, stderr bytes.Buffer

			got := newRunCommand(paths, &stubDaemon{err: tt.err}, nil).run(nil, &stdout, &stderr)

			if got != tt.wantCode || !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("run() = %d, stderr %q; want %d containing %q", got, stderr.String(), tt.wantCode, tt.wantStderr)
			}
		})
	}
}

func TestRunRefusesAConfigurationWithoutAMachineID(t *testing.T) {
	paths := testPaths(t)
	cfg := enrolledConfig()
	cfg.MachineID = ""
	writeConfig(t, paths, cfg)
	writeCredential(t, paths)

	cmd := newRunCommand(paths, nil, nil)
	cmd.newAgent = newDaemon

	var stdout, stderr bytes.Buffer

	if got := cmd.run(nil, &stdout, &stderr); got != ExitError || !strings.Contains(stderr.String(), "run omj-agent enroll first") {
		t.Errorf("run() = %d, stderr %q; want %d and the enroll hint", got, stderr.String(), ExitError)
	}
}

func TestRunReportsFailuresWhileBuilding(t *testing.T) {
	paths := testPaths(t)
	writeConfig(t, paths, enrolledConfig())
	writeCredential(t, paths)

	tests := []struct {
		name  string
		setup func(cmd *runCommand)
		want  string
	}{
		{name: "machine information", setup: func(cmd *runCommand) {
			cmd.collect = func(context.Context) (sysinfo.Info, error) { return sysinfo.Info{}, errors.New("interrupted") }
		}, want: "collect machine information: interrupted"},
		{name: "state file", setup: func(cmd *runCommand) {
			cmd.loadState = func(string) (*state.Store, error) { return nil, errors.New("read state: permission denied") }
		}, want: "read state: permission denied"},
		{name: "agent", setup: func(cmd *runCommand) {
			cmd.newAgent = func(agent.Options) (daemon, error) { return nil, errors.New("no buffer") }
		}, want: "no buffer"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newRunCommand(paths, &stubDaemon{}, nil)
			tt.setup(&cmd)

			var stdout, stderr bytes.Buffer

			if got := cmd.run(nil, &stdout, &stderr); got != ExitError || !strings.Contains(stderr.String(), tt.want) {
				t.Errorf("run() = %d, stderr %q; want %d containing %q", got, stderr.String(), ExitError, tt.want)
			}
		})
	}
}

func TestRunRefusesAPlainHTTPServerURLWithoutTheFlag(t *testing.T) {
	paths := testPaths(t)
	cfg := enrolledConfig()
	cfg.ServerURL = "http://omj.example.com"
	cfg.InsecureHTTP = true
	writeConfig(t, paths, cfg)
	writeCredential(t, paths)

	var captured agent.Options
	var stdout, stderr bytes.Buffer

	if got := newRunCommand(paths, &stubDaemon{}, &captured).run(nil, &stdout, &stderr); got != ExitOK || captured.Client == nil {
		t.Errorf("run() = %d (stderr %q), want %d with a client built for plain HTTP", got, stderr.String(), ExitOK)
	}
}
