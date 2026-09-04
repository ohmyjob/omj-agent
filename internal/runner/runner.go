// Package runner starts a Job's command in a clean environment, in its own
// process group, and reports how it ended.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"sync"
	"time"
)

type Stream string

const (
	Stdout Stream = "stdout"
	Stderr Stream = "stderr"

	defaultShell = "/bin/sh"

	// A child that exits while a grandchild still holds the output pipes must
	// not keep the Run open forever; after this delay the pipes are closed and
	// whatever the grandchild writes later is lost.
	waitDelay = 2 * time.Second
)

// Sink receives output as it arrives. The bytes are only valid during the
// call because the copying goroutines reuse their buffer.
type Sink interface {
	Write(stream Stream, data []byte)
}

type Spec struct {
	RunID      string
	JobName    string
	MachineID  string
	Command    string
	Shell      string
	WorkingDir string
	Env        map[string]string
	Timeout    time.Duration
	MaxOutput  int64
}

type Result struct {
	ExitCode   int
	Signal     os.Signal
	StartedAt  time.Time
	FinishedAt time.Time
	TimedOut   bool
	Cancelled  bool
	Err        error
}

type Process struct {
	cmd    *exec.Cmd
	pgid   int
	once   sync.Once
	result Result
}

func Start(ctx context.Context, spec Spec, sink Sink) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if spec.Command == "" {
		return nil, errors.New("command is required")
	}

	serviceUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("look up the service user: %w", err)
	}

	dir := spec.WorkingDir
	if dir == "" {
		dir = serviceUser.HomeDir
	}

	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("working directory %q: %w", dir, err)
	}

	shell := spec.Shell
	if shell == "" {
		shell = defaultShell
	}

	cmd := exec.Command(shell, "-c", spec.Command)
	cmd.Dir = dir
	cmd.Env = environment(spec, serviceUser)
	cmd.Stdout = streamWriter{sink: sink, stream: Stdout}
	cmd.Stderr = streamWriter{sink: sink, stream: Stderr}
	cmd.WaitDelay = waitDelay
	configureProcess(cmd)

	startedAt := time.Now()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", shell, err)
	}

	return &Process{
		cmd:    cmd,
		pgid:   processGroupID(cmd.Process.Pid),
		result: Result{StartedAt: startedAt},
	}, nil
}

func (p *Process) PID() int {
	return p.cmd.Process.Pid
}

func (p *Process) PGID() int {
	return p.pgid
}

func (p *Process) Wait() Result {
	p.once.Do(func() {
		err := p.cmd.Wait()

		p.result.FinishedAt = time.Now()
		p.result.ExitCode, p.result.Signal = exitStatus(p.cmd.ProcessState)

		var exitErr *exec.ExitError

		switch {
		case err == nil, errors.As(err, &exitErr):
		case errors.Is(err, exec.ErrWaitDelay):
			// The child itself has exited and its status is recorded above;
			// only a grandchild kept the pipes open past the delay.
		default:
			p.result.Err = fmt.Errorf("wait for %s: %w", p.cmd.Path, err)
		}
	})

	return p.result
}

type streamWriter struct {
	sink   Sink
	stream Stream
}

func (w streamWriter) Write(p []byte) (int, error) {
	w.sink.Write(w.stream, p)

	return len(p), nil
}
