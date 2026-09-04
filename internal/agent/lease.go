package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ohmyjob/omj-agent/internal/client"
	"github.com/ohmyjob/omj-agent/internal/output"
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/runner"
	"github.com/ohmyjob/omj-agent/internal/state"
)

// Run is a lease the Agent accepted, from start until the reporter releases
// it. Process is nil when the process never existed: the spawn failed, or
// the Server cancelled the Run before it was started.
type Run struct {
	Lease                protocol.RunLease
	Timeout              time.Duration
	MaxOutput            int64
	StartedAt            time.Time
	StartAccepted        bool
	Process              *runner.Process
	Chunker              *output.Chunker
	SpawnErr             error
	CancelledBeforeStart bool

	cancelOnce sync.Once
}

type verifiedLease struct {
	lease     protocol.RunLease
	timeout   time.Duration
	maxOutput int64
}

func (a *Agent) handleLease(ctx context.Context, lease protocol.RunLease, cancelled bool) {
	runID := lease.RunID

	if run, ok := a.registry.get(runID); ok {
		a.acknowledge(ctx, run)

		return
	}

	if outcome, ok := a.state.RecentOutcome(runID); ok {
		a.logger.Info("lease for a finished run; sending its outcome again", "run_id", runID, "status", outcome.Status)

		if err := a.resender.Resend(ctx, lease, outcome); err != nil {
			a.logger.Warn("outcome not re-sent", "run_id", runID, "error", err)
		}

		return
	}

	verified, err := a.verify(lease)
	if err != nil {
		if a.rejected.first(runID) {
			a.logger.Warn("lease ignored", "run_id", runID, "job", lease.JobName, "reason", err)
		}

		return
	}

	if cancelled {
		a.logger.Info("run cancelled before it started", "run_id", runID)
		a.launch(&Run{Lease: lease, Timeout: verified.timeout, MaxOutput: verified.maxOutput, Chunker: a.chunker(verified), CancelledBeforeStart: true})

		return
	}

	if a.registry.count() >= a.cfg.MaxConcurrentRuns {
		if a.rejected.first(runID) {
			a.logger.Warn("lease ignored", "run_id", runID, "job", lease.JobName, "reason", "no free slot")
		}

		return
	}

	if !a.start(ctx, verified) {
		return
	}

	a.launch(a.spawn(ctx, verified))
}

// acknowledge answers a repeated lease for a Run this process already owns:
// the Server treats start as idempotent and the process is never started twice.
func (a *Agent) acknowledge(ctx context.Context, run *Run) {
	if !run.StartAccepted {
		return
	}

	a.logger.Debug("lease repeated for an active run; acknowledging again", "run_id", run.Lease.RunID)

	if _, err := a.client.StartRun(ctx, run.Lease.RunID, a.startRequest(run.Timeout, run.MaxOutput)); err != nil {
		a.logger.Warn("repeated start not acknowledged", "run_id", run.Lease.RunID, "error", err)
	}
}

func (a *Agent) verify(lease protocol.RunLease) (verifiedLease, error) {
	switch {
	case lease.RunID == "":
		return verifiedLease{}, errors.New("lease has no run id")
	case lease.MachineID != a.cfg.MachineID:
		return verifiedLease{}, fmt.Errorf("lease is for machine %q, not this one", lease.MachineID)
	case a.state.IsActive(lease.RunID):
		return verifiedLease{}, errors.New("run is still listed as active by a previous agent process")
	case !lease.LeaseExpiresAt.Add(clockTolerance).After(a.now()):
		return verifiedLease{}, fmt.Errorf("lease expired at %s", lease.LeaseExpiresAt.UTC().Format(time.RFC3339))
	case lease.Command == "":
		return verifiedLease{}, errors.New("lease has no command")
	case lease.TimeoutSeconds <= 0:
		return verifiedLease{}, fmt.Errorf("lease timeout %d is not positive", lease.TimeoutSeconds)
	case lease.MaxOutputBytes <= 0:
		return verifiedLease{}, fmt.Errorf("lease output limit %d is not positive", lease.MaxOutputBytes)
	}

	return verifiedLease{
		lease:     lease,
		timeout:   time.Duration(min(lease.TimeoutSeconds, a.cfg.MaxTimeoutSeconds)) * time.Second,
		maxOutput: min(lease.MaxOutputBytes, a.cfg.MaxOutputBytes),
	}, nil
}

func (a *Agent) startRequest(timeout time.Duration, maxOutput int64) protocol.StartRequest {
	return protocol.StartRequest{
		EffectiveTimeoutSeconds: int(timeout / time.Second),
		EffectiveMaxOutputBytes: maxOutput,
	}
}

// start confirms the lease, retrying transient failures until the lease
// itself expires. It reports whether the process may be spawned.
func (a *Agent) start(ctx context.Context, verified verifiedLease) bool {
	runID := verified.lease.RunID

	ctx, cancel := context.WithTimeout(ctx, verified.lease.LeaseExpiresAt.Add(clockTolerance).Sub(a.now()))
	defer cancel()

	backoff := &client.Backoff{Rand: a.backoff.Rand, Sleep: a.sleep}

	err := client.Retry(ctx, backoff, func(ctx context.Context) error {
		_, err := a.client.StartRun(ctx, runID, a.startRequest(verified.timeout, verified.maxOutput))

		return err
	})

	switch {
	case err == nil:
		return true
	case client.IsConflict(err, protocol.ErrLeaseExpired),
		client.IsConflict(err, protocol.ErrRunCancelled),
		client.IsConflict(err, protocol.ErrRunFinished),
		client.IsConflict(err, protocol.ErrNotLeased),
		client.IsNotFound(err):
		a.logger.Info("lease dropped", "run_id", runID, "error", err)
	default:
		a.logger.Warn("run not started", "run_id", runID, "error", err)
	}

	return false
}

func (a *Agent) spawn(ctx context.Context, verified verifiedLease) *Run {
	lease := verified.lease

	run := &Run{
		Lease:         lease,
		Timeout:       verified.timeout,
		MaxOutput:     verified.maxOutput,
		StartedAt:     a.now(),
		StartAccepted: true,
		Chunker:       a.chunker(verified),
	}

	spec := runner.Spec{
		RunID:      lease.RunID,
		JobName:    lease.JobName,
		MachineID:  a.cfg.MachineID,
		Command:    lease.Command,
		Shell:      deref(lease.Shell),
		WorkingDir: deref(lease.WorkingDirectory),
		Env:        lease.Environment,
		Timeout:    verified.timeout,
		MaxOutput:  verified.maxOutput,
	}

	process, err := a.runner.Start(ctx, spec, run.Chunker)
	if err != nil {
		a.logger.Error("process not started", "run_id", lease.RunID, "job", lease.JobName, "error", err)
		run.SpawnErr = err
		// The error text is the whole log of a Run that never started, so it
		// is flushed at once instead of waiting for a tick.
		run.Chunker.Write(runner.Stderr, []byte(err.Error()+"\n"))
		run.Chunker.Close()

		return run
	}

	run.Process = process

	a.logger.Info("run started", "run_id", lease.RunID, "job", lease.JobName, "pid", process.PID(), "timeout", verified.timeout)

	if err := a.state.MarkActive(state.ActiveRun{RunID: lease.RunID, PID: process.PID(), PGID: process.PGID(), StartedAt: run.StartedAt}); err != nil {
		a.logger.Error("state not saved", "run_id", lease.RunID, "error", err)
	}

	return run
}

func (a *Agent) chunker(verified verifiedLease) *output.Chunker {
	settings := a.Settings()

	return output.NewChunker(verified.lease.RunID, a.buffer.Add, output.ChunkerOptions{
		ChunkBytes:    settings.ChunkBytes,
		FlushInterval: settings.FlushInterval,
		MaxOutput:     verified.maxOutput,
		Now:           a.now,
	})
}

func deref(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
