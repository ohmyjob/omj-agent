// Package state remembers which Runs are active and which finished recently,
// so a run id is never executed twice and a finished Run can be reported
// again after a restart.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/ohmyjob/omj-agent/internal/atomicfile"
)

const (
	MaxRecentRuns = 1000
	RecentRunTTL  = 7 * 24 * time.Hour

	fileMode os.FileMode = 0o600
	dirMode  os.FileMode = 0o750
)

type ActiveRun struct {
	RunID     string    `json:"run_id"`
	PID       int       `json:"pid"`
	PGID      int       `json:"pgid"`
	StartedAt time.Time `json:"started_at"`
}

type RecentRun struct {
	RunID      string    `json:"run_id"`
	Status     string    `json:"status"`
	ExitCode   *int      `json:"exit_code"`
	FinishedAt time.Time `json:"finished_at"`
}

// Outcome describes how a Run ended; ExitCode is nil when the process never
// reported one, for example after a spawn failure.
type Outcome struct {
	Status   string
	ExitCode *int
}

type contents struct {
	MachineID  string      `json:"machine_id"`
	ActiveRuns []ActiveRun `json:"active_runs"`
	RecentRuns []RecentRun `json:"recent_runs"`
}

type Store struct {
	path   string
	now    func() time.Time
	logger *slog.Logger
	writer atomicfile.Writer

	mu       sync.Mutex
	contents contents
}

// Loader lets tests inject the clock the caps and the corrupt-file name use.
type Loader struct {
	Now    func() time.Time
	Logger *slog.Logger
}

func Load(path string) (*Store, error) {
	return Loader{}.Load(path)
}

func (l Loader) Load(path string) (*Store, error) {
	store := &Store{path: path, now: l.Now, logger: l.Logger}

	if store.now == nil {
		store.now = time.Now
	}

	if store.logger == nil {
		store.logger = slog.Default()
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}

	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}

	if err := json.Unmarshal(data, &store.contents); err != nil {
		store.setAside(err)
		store.contents = contents{}
	}

	return store, nil
}

// A corrupt state file must never keep the agent from starting, so it is
// kept next to the fresh one for inspection instead of being overwritten.
func (s *Store) setAside(cause error) {
	moved := fmt.Sprintf("%s.corrupt-%s", s.path, s.now().UTC().Format("20060102T150405Z"))

	if err := os.Rename(s.path, moved); err != nil {
		s.logger.Warn("state file is corrupt and could not be set aside; starting empty", "path", s.path, "error", cause, "rename_error", err)

		return
	}

	s.logger.Warn("state file is corrupt; starting empty", "path", s.path, "moved_to", moved, "error", cause)
}

func (s *Store) MachineID() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.contents.MachineID
}

func (s *Store) SetMachineID(machineID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.contents.MachineID = machineID

	return s.save()
}

func (s *Store) MarkActive(run ActiveRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if i := slices.IndexFunc(s.contents.ActiveRuns, func(r ActiveRun) bool { return r.RunID == run.RunID }); i >= 0 {
		s.contents.ActiveRuns[i] = run
	} else {
		s.contents.ActiveRuns = append(s.contents.ActiveRuns, run)
	}

	return s.save()
}

func (s *Store) MarkFinished(runID string, outcome Outcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.contents.ActiveRuns = slices.DeleteFunc(s.contents.ActiveRuns, func(r ActiveRun) bool { return r.RunID == runID })

	recent := RecentRun{RunID: runID, Status: outcome.Status, ExitCode: cloneExitCode(outcome.ExitCode), FinishedAt: s.now()}

	if i := slices.IndexFunc(s.contents.RecentRuns, func(r RecentRun) bool { return r.RunID == runID }); i >= 0 {
		s.contents.RecentRuns[i] = recent
	} else {
		s.contents.RecentRuns = append(s.contents.RecentRuns, recent)
	}

	return s.save()
}

func (s *Store) IsActive(runID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.ContainsFunc(s.contents.ActiveRuns, func(r ActiveRun) bool { return r.RunID == runID })
}

func (s *Store) RecentOutcome(runID string) (RecentRun, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	i := slices.IndexFunc(s.contents.RecentRuns, func(r RecentRun) bool { return r.RunID == runID })
	if i < 0 {
		return RecentRun{}, false
	}

	recent := s.contents.RecentRuns[i]
	recent.ExitCode = cloneExitCode(recent.ExitCode)

	return recent, true
}

func (s *Store) Active() []ActiveRun {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.contents.ActiveRuns)
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.save()
}

func (s *Store) save() error {
	s.prune()

	if s.contents.ActiveRuns == nil {
		s.contents.ActiveRuns = []ActiveRun{}
	}

	if s.contents.RecentRuns == nil {
		s.contents.RecentRuns = []RecentRun{}
	}

	data, err := json.MarshalIndent(s.contents, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(s.path), dirMode); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	if err := s.writer.Write(s.path, append(data, '\n'), fileMode); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	return nil
}

func (s *Store) prune() {
	cutoff := s.now().Add(-RecentRunTTL)

	recent := slices.DeleteFunc(s.contents.RecentRuns, func(r RecentRun) bool { return r.FinishedAt.Before(cutoff) })

	if len(recent) > MaxRecentRuns {
		recent = recent[len(recent)-MaxRecentRuns:]
	}

	s.contents.RecentRuns = recent
}

func cloneExitCode(code *int) *int {
	if code == nil {
		return nil
	}

	clone := *code

	return &clone
}
