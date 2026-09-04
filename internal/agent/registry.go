package agent

import (
	"sync"

	"github.com/ohmyjob/omj-agent/internal/protocol"
)

// registry is the in-memory list of Runs this process owns, in the order
// they were accepted; the state file is its durable counterpart.
type registry struct {
	mu   sync.Mutex
	runs []*Run
}

func newRegistry() registry {
	return registry{}
}

func (r *registry) add(run *Run) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.runs = append(r.runs, run)
}

func (r *registry) remove(runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, run := range r.runs {
		if run.Lease.RunID == runID {
			r.runs = append(r.runs[:i], r.runs[i+1:]...)

			return
		}
	}
}

func (r *registry) get(runID string) (*Run, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, run := range r.runs {
		if run.Lease.RunID == runID {
			return run, true
		}
	}

	return nil, false
}

func (r *registry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.runs)
}

func (r *registry) all() []*Run {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]*Run(nil), r.runs...)
}

// active lists the Runs the Server must keep as running: start was accepted
// and finish has not been, whether or not the process still exists. The
// slice is never nil because the Server rejects null.
func (r *registry) active() []protocol.ActiveRun {
	r.mu.Lock()
	defer r.mu.Unlock()

	active := make([]protocol.ActiveRun, 0, len(r.runs))

	for _, run := range r.runs {
		if run.StartAccepted {
			active = append(active, protocol.ActiveRun{RunID: run.Lease.RunID, Status: protocol.ActiveRunRunning})
		}
	}

	return active
}

// rejected remembers the leases already warned about so a Server that keeps
// handing out the same bad lease produces one line, not one per poll.
type rejected struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

const maxRejected = 1024

func newRejected() rejected {
	return rejected{ids: map[string]struct{}{}}
}

// first reports whether this is the first time the id is rejected.
func (r *rejected) first(runID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, seen := r.ids[runID]; seen {
		return false
	}

	// Leases expire within a minute, so the set stays small; the cap only
	// bounds a misbehaving Server.
	if len(r.ids) >= maxRejected {
		clear(r.ids)
	}

	r.ids[runID] = struct{}{}

	return true
}
