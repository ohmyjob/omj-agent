package agent

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"time"
)

const (
	// AgentStopped is the cancel reason handed to every running process when
	// the Agent itself is asked to stop.
	AgentStopped = "agent_stopped"

	// DefaultStopBudget is how long a stopping Agent waits for its reporters
	// to deliver the outcomes; it fits inside the unit's TimeoutStopSec=30.
	DefaultStopBudget = 20 * time.Second
)

// ErrForcedStop is returned by Run when a second signal arrived before the
// running Runs were reported; the outcomes stay in the state file.
var ErrForcedStop = errors.New("agent: stopped by a second signal before every run was reported")

// stopper watches the signal channel for the lifetime of one Run call.
type stopper struct {
	stopped chan struct{}
	forced  chan struct{}
}

func (a *Agent) watchSignals(done <-chan struct{}, signals <-chan os.Signal, stopPolling context.CancelFunc) *stopper {
	s := &stopper{stopped: make(chan struct{}), forced: make(chan struct{})}

	go func() {
		select {
		case <-done:
			return
		case sig := <-signals:
			a.logger.Info("stop requested; terminating running processes", "signal", sig.String(), "active_runs", a.registry.count())
		}

		a.cancelAll()
		close(s.stopped)
		stopPolling()

		select {
		case <-done:
		case sig := <-signals:
			a.logger.Warn("second signal; exiting without waiting for the runs to be reported", "signal", sig.String())
			close(s.forced)
		}
	}()

	return s
}

func (a *Agent) cancelAll() {
	for _, run := range a.registry.all() {
		if run.Process != nil {
			run.Process.Cancel(AgentStopped)
		}
	}
}

// notify subscribes to the stop signals unless a channel was injected.
func (a *Agent) notify() (<-chan os.Signal, func()) {
	if a.signals != nil {
		return a.signals, func() {}
	}

	ch := make(chan os.Signal, 2)
	signal.Notify(ch, StopSignals()...)

	return ch, func() { signal.Stop(ch) }
}

// drain gives the reporters the stop budget to deliver their outcomes, then
// persists the state. A second signal ends the wait at once.
func (a *Agent) drain(s *stopper) error {
	// A lease accepted while the signal was being handled has its process by
	// now; cancelling again is harmless for the ones already signalled.
	a.cancelAll()

	finished := make(chan struct{})

	go func() {
		a.runs.Wait()
		close(finished)
	}()

	timer := time.NewTimer(a.stopBudget)
	defer timer.Stop()

	err := error(nil)

	select {
	case <-finished:
		a.logger.Info("every run was reported; stopping")
	case <-timer.C:
		a.logger.Warn("stop budget elapsed; outcomes not yet delivered stay in the state file", "budget", a.stopBudget)
		a.stopReporting()
	case <-s.forced:
		a.stopReporting()
		err = ErrForcedStop
	}

	if saveErr := a.state.Save(); saveErr != nil {
		a.logger.Error("state not saved", "error", saveErr)
	}

	return err
}
