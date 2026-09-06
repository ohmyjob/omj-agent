package agent

import (
	"context"
	"fmt"

	"github.com/ohmyjob/omj-agent/internal/client"
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/state"
)

// adopt makes the state file this Machine's own. A file left behind by a
// previous enrolment describes another Machine's Runs, which this Server
// would not recognise, so it is set aside rather than reported.
func (a *Agent) adopt() {
	recorded := a.state.MachineID()

	if recorded == a.cfg.MachineID {
		return
	}

	if recorded != "" {
		a.logger.Warn("state file belongs to another machine; starting empty",
			"recorded_machine_id", recorded,
			"machine_id", a.cfg.MachineID)

		if err := a.state.Reset(a.cfg.MachineID); err != nil {
			a.logger.Error("state not saved", "error", err)
		}

		return
	}

	if err := a.state.SetMachineID(a.cfg.MachineID); err != nil {
		a.logger.Error("state not saved", "error", err)
	}
}

func (a *Agent) logStartup() {
	a.logger.Info("agent starting",
		"server_url", a.cfg.ServerURL,
		"machine_id", a.cfg.MachineID,
		"user", a.info.AgentUser,
		"uid", a.info.AgentUID,
		"max_concurrent_runs", a.cfg.MaxConcurrentRuns,
		"max_timeout_seconds", a.cfg.MaxTimeoutSeconds,
		"max_output_bytes", a.cfg.MaxOutputBytes,
		"active_runs", len(a.state.Active()))
}

// reconcile reports every Run the previous process left active as lost.
// Version 1 never reattaches to a process, so the outcome is recorded first
// and delivered in the background: a lease for the same run id then gets the
// stored outcome, and polling starts without waiting for the Server.
func (a *Agent) reconcile() {
	for _, active := range a.state.Active() {
		startedAt := active.StartedAt
		reason := protocol.ReasonAgentRestarted
		a.logger.Warn("run was active when the agent last stopped; reporting it as lost", "run_id", active.RunID, "pid", active.PID)

		outcome := state.Outcome{Status: string(protocol.RunStatusLost), StartedAt: &startedAt, Reason: &reason}
		if err := a.state.MarkFinished(active.RunID, outcome); err != nil {
			a.logger.Error("state not saved", "run_id", active.RunID, "error", err)
		}

		a.runs.Add(1)

		go func() {
			defer a.runs.Done()

			if err := a.reportLost(active); err != nil {
				a.logger.Warn("lost run not reported; its outcome is kept for the next lease", "run_id", active.RunID, "error", err)
			}
		}()
	}
}

func (a *Agent) reportLost(active state.ActiveRun) error {
	reason := protocol.ReasonAgentRestarted
	startedAt := active.StartedAt

	request := protocol.FinishRequest{
		Status:     protocol.RunStatusLost,
		StartedAt:  &startedAt,
		FinishedAt: a.now(),
		Reason:     &reason,
	}

	backoff := &client.Backoff{Rand: a.backoff.Rand, Sleep: a.sleep}

	err := client.Retry(a.reportCtx, backoff, func(ctx context.Context) error {
		_, err := a.client.FinishRun(ctx, active.RunID, request)

		return err
	})

	switch {
	case err == nil, client.IsConflict(err, protocol.ErrRunFinished):
		a.logger.Info("lost run reported", "run_id", active.RunID)

		return nil
	case client.IsConflict(err, protocol.ErrNotLeased), client.IsNotFound(err):
		a.logger.Warn("lost run dropped; the server does not know it", "run_id", active.RunID, "error", err)

		return nil
	default:
		return fmt.Errorf("finish %s as lost: %w", active.RunID, err)
	}
}
