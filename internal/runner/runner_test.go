package runner

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

type recordingSink struct {
	mu      sync.Mutex
	streams map[Stream][]byte
}

func newRecordingSink() *recordingSink {
	return &recordingSink{streams: map[Stream][]byte{}}
}

func (s *recordingSink) Write(stream Stream, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.streams[stream] = append(s.streams[stream], data...)
}

func (s *recordingSink) String(stream Stream) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return string(s.streams[stream])
}

func (s *recordingSink) Len(stream Stream) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.streams[stream])
}

func run(t *testing.T, spec Spec) (Result, *recordingSink) {
	t.Helper()

	sink := newRecordingSink()

	process, err := Start(context.Background(), spec, sink)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	result := process.Wait()
	if result.Err != nil {
		t.Fatalf("Wait: %v", result.Err)
	}

	return result, sink
}

func TestStartReportsTheExitStatus(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		exitCode int
		signal   string
	}{
		{name: "success is zero", command: "exit 0", exitCode: 0},
		{name: "a normal exit reports its code", command: "exit 3", exitCode: 3},
		{name: "death by signal is 128 plus the signal", command: "kill -TERM $$", exitCode: 143, signal: "terminated"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := run(t, Spec{Command: tt.command})

			if result.ExitCode != tt.exitCode {
				t.Errorf("exit code = %d, want %d", result.ExitCode, tt.exitCode)
			}

			signal := ""
			if result.Signal != nil {
				signal = result.Signal.String()
			}

			if signal != tt.signal {
				t.Errorf("signal = %q, want %q", signal, tt.signal)
			}

			if result.StartedAt.IsZero() || !result.FinishedAt.After(result.StartedAt) {
				t.Errorf("timing = %v to %v, want finished after started", result.StartedAt, result.FinishedAt)
			}
		})
	}
}

func TestStartTagsEachStream(t *testing.T) {
	_, sink := run(t, Spec{Command: "printf out; printf err >&2"})

	if got := sink.String(Stdout); got != "out" {
		t.Errorf("stdout = %q, want %q", got, "out")
	}

	if got := sink.String(Stderr); got != "err" {
		t.Errorf("stderr = %q, want %q", got, "err")
	}
}

func TestStartDrainsLargeOutputOnBothStreams(t *testing.T) {
	const size = 50 * 1024 * 1024

	result, sink := run(t, Spec{Command: "head -c 52428800 /dev/zero & head -c 52428800 /dev/zero >&2; wait"})

	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}

	if got := sink.Len(Stdout); got != size {
		t.Errorf("stdout bytes = %d, want %d", got, size)
	}

	if got := sink.Len(Stderr); got != size {
		t.Errorf("stderr bytes = %d, want %d", got, size)
	}
}

func TestStartBuildsTheEnvironmentFromScratch(t *testing.T) {
	t.Setenv("OMJ_TEST_CANARY", "leaked from the daemon")

	serviceUser, err := user.Current()
	if err != nil {
		t.Fatalf("current user: %v", err)
	}

	_, sink := run(t, Spec{
		RunID:     "run-1",
		JobName:   "Nightly backup",
		MachineID: "machine-1",
		Command:   "/usr/bin/env",
		Env:       map[string]string{"GREETING": "hello", "LANG": "en_US.UTF-8"},
	})

	got := map[string]string{}

	for line := range strings.Lines(sink.String(Stdout)) {
		key, value, _ := strings.Cut(strings.TrimSuffix(line, "\n"), "=")
		got[key] = value
	}

	want := map[string]string{
		"HOME":           serviceUser.HomeDir,
		"USER":           serviceUser.Username,
		"LOGNAME":        serviceUser.Username,
		"SHELL":          defaultShell,
		"PATH":           defaultPath,
		"LANG":           "en_US.UTF-8",
		"OMJ_RUN_ID":     "run-1",
		"OMJ_JOB_NAME":   "Nightly backup",
		"OMJ_MACHINE_ID": "machine-1",
		"GREETING":       "hello",
	}

	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}

	// Shells maintain a few variables of their own; everything else must come from the Spec.
	shellManaged := []string{"PWD", "OLDPWD", "SHLVL", "_"}

	for key := range got {
		if _, expected := want[key]; !expected && !slices.Contains(shellManaged, key) {
			t.Errorf("unexpected variable %s=%q", key, got[key])
		}
	}
}

func TestEnvironment(t *testing.T) {
	executionUser := &user.User{Username: "ohmyjob", HomeDir: "/var/lib/ohmyjob"}

	tests := []struct {
		name  string
		spec  Spec
		shell string
		want  []string
	}{
		{
			name:  "defaults",
			spec:  Spec{RunID: "run-1", JobName: "Nightly backup", MachineID: "machine-1"},
			shell: defaultShell,
			want: []string{
				"HOME=/var/lib/ohmyjob",
				"LANG=C.UTF-8",
				"LOGNAME=ohmyjob",
				"OMJ_JOB_NAME=Nightly backup",
				"OMJ_MACHINE_ID=machine-1",
				"OMJ_RUN_ID=run-1",
				"PATH=" + defaultPath,
				"SHELL=" + defaultShell,
				"USER=ohmyjob",
			},
		},
		{
			name:  "the Job's variables override any default",
			spec:  Spec{RunID: "run-1", Env: map[string]string{"PATH": "/opt/bin", "TZ": "UTC"}},
			shell: "/bin/bash",
			want: []string{
				"HOME=/var/lib/ohmyjob",
				"LANG=C.UTF-8",
				"LOGNAME=ohmyjob",
				"OMJ_JOB_NAME=",
				"OMJ_MACHINE_ID=",
				"OMJ_RUN_ID=run-1",
				"PATH=/opt/bin",
				"SHELL=/bin/bash",
				"TZ=UTC",
				"USER=ohmyjob",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := environment(tt.spec, executionUser, tt.shell); !slices.Equal(got, tt.want) {
				t.Errorf("environment = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStartWorkingDirectory(t *testing.T) {
	serviceUser, err := user.Current()
	if err != nil {
		t.Fatalf("current user: %v", err)
	}

	tests := []struct {
		name string
		dir  string
		want string
	}{
		{name: "defaults to the execution user's home", dir: "", want: serviceUser.HomeDir},
		{name: "uses the directory given", dir: t.TempDir(), want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.want
			if want == "" {
				want = tt.dir
			}

			want, err := filepath.EvalSymlinks(want)
			if err != nil {
				t.Fatalf("resolve %q: %v", tt.want, err)
			}

			_, sink := run(t, Spec{Command: "pwd -P", WorkingDir: tt.dir})

			if got := strings.TrimSpace(sink.String(Stdout)); got != want {
				t.Errorf("pwd = %q, want %q", got, want)
			}
		})
	}
}

func TestStartRefusesAMissingWorkingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")

	_, err := Start(context.Background(), Spec{Command: "true", WorkingDir: missing}, newRecordingSink())

	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("error = %v, want one naming %q", err, missing)
	}

	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error = %v, want it to wrap os.ErrNotExist", err)
	}
}

func TestStartRunsTheCommandThroughTheShell(t *testing.T) {
	shell, err := filepath.Abs("testdata/fake-shell.sh")
	if err != nil {
		t.Fatalf("locate fake shell: %v", err)
	}

	_, sink := run(t, Spec{Command: "echo hi", Shell: shell})

	if got := sink.String(Stdout); got != "shell:-c:echo hi\n" {
		t.Errorf("stdout = %q, want %q", got, "shell:-c:echo hi\n")
	}
}

func TestStartPutsTheCommandInItsOwnProcessGroup(t *testing.T) {
	process, err := Start(context.Background(), Spec{Command: "sleep 1"}, newRecordingSink())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	if process.PID() <= 0 {
		t.Errorf("pid = %d, want a positive number", process.PID())
	}

	if process.PGID() != process.PID() {
		t.Errorf("pgid = %d, want the pid %d", process.PGID(), process.PID())
	}

	if result := process.Wait(); result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
}

func TestWaitReturnsTheSameResultTwice(t *testing.T) {
	process, err := Start(context.Background(), Spec{Command: "exit 5"}, newRecordingSink())
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	first := process.Wait()
	second := process.Wait()

	if first.ExitCode != 5 || second != first {
		t.Errorf("results = %+v and %+v, want the same result with exit code 5", first, second)
	}
}

func TestStartRefusesToStart(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		ctx     context.Context
		spec    Spec
		wantErr error
		wantMsg string
	}{
		{name: "a done context", ctx: cancelled, spec: Spec{Command: "true"}, wantErr: context.Canceled},
		{name: "an empty command", ctx: context.Background(), spec: Spec{}, wantMsg: "command is required"},
		{name: "a missing shell", ctx: context.Background(), spec: Spec{Command: "true", Shell: "/nonexistent/sh"}, wantMsg: "start /nonexistent/sh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			process, err := Start(tt.ctx, tt.spec, newRecordingSink())

			if process != nil {
				t.Errorf("process = %v, want nil", process)
			}

			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}

			if tt.wantMsg != "" && (err == nil || !strings.Contains(err.Error(), tt.wantMsg)) {
				t.Errorf("error = %v, want one containing %q", err, tt.wantMsg)
			}
		})
	}
}
