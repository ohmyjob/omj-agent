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

	// DefaultMaxTimeout is the max_timeout_seconds default of agent.conf; a
	// Run is never allowed to be unlimited.
	DefaultMaxTimeout = 72 * time.Hour

	// DefaultGrace is how long a terminated group gets to act on SIGTERM
	// before SIGKILL follows.
	DefaultGrace = 10 * time.Second

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
	Reason     string
	Err        error
}

// Runner holds the local limits every Run is held to, whatever the lease says.
type Runner struct {
	MaxTimeout time.Duration
	Grace      time.Duration
}

type Process struct {
	cmd   *exec.Cmd
	pgid  int
	grace time.Duration
	timer *time.Timer
	done  chan struct{}

	mu           sync.Mutex
	exited       bool
	terminating  bool
	timedOut     bool
	cancelled    bool
	reason       string
	terminateErr error

	result Result
}

func Start(ctx context.Context, spec Spec, sink Sink) (*Process, error) {
	return Runner{}.Start(ctx, spec, sink)
}

func (r Runner) Start(ctx context.Context, spec Spec, sink Sink) (*Process, error) {
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

	p := &Process{
		cmd:    cmd,
		pgid:   processGroupID(cmd.Process.Pid),
		grace:  r.grace(),
		done:   make(chan struct{}),
		result: Result{StartedAt: startedAt},
	}

	p.mu.Lock()
	p.timer = time.AfterFunc(r.timeout(spec.Timeout), p.timeOut)
	p.mu.Unlock()

	go p.wait()

	return p, nil
}

func (r Runner) timeout(requested time.Duration) time.Duration {
	limit := r.MaxTimeout
	if limit <= 0 {
		limit = DefaultMaxTimeout
	}

	if requested <= 0 || requested > limit {
		return limit
	}

	return requested
}

func (r Runner) grace() time.Duration {
	if r.Grace <= 0 {
		return DefaultGrace
	}

	return r.Grace
}

func (p *Process) PID() int {
	return p.cmd.Process.Pid
}

func (p *Process) PGID() int {
	return p.pgid
}

// Cancel terminates the process group. The reason travels into the Result so
// the caller can tell a requested cancellation from an Agent shutdown.
func (p *Process) Cancel(reason string) {
	p.terminate(false, reason)
}

func (p *Process) Wait() Result {
	<-p.done

	return p.result
}

func (p *Process) timeOut() {
	p.terminate(true, "")
}

// The first termination wins and a process that has already exited is left
// alone, so the flags always say why the group was signalled.
func (p *Process) terminate(timedOut bool, reason string) {
	p.mu.Lock()
	if p.exited || p.terminating {
		p.mu.Unlock()

		return
	}

	p.terminating = true
	p.timedOut = timedOut
	p.cancelled = !timedOut
	p.reason = reason
	p.timer.Stop()
	p.mu.Unlock()

	go p.terminateGroup()
}

func (p *Process) wait() {
	err := p.cmd.Wait()

	p.mu.Lock()
	p.timer.Stop()
	p.exited = true
	p.result.TimedOut = p.timedOut
	p.result.Cancelled = p.cancelled
	p.result.Reason = p.reason
	terminateErr := p.terminateErr
	p.mu.Unlock()

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

	if p.result.Err == nil {
		p.result.Err = terminateErr
	}

	close(p.done)
}

type streamWriter struct {
	sink   Sink
	stream Stream
}

func (w streamWriter) Write(p []byte) (int, error) {
	w.sink.Write(w.stream, p)

	return len(p), nil
}
