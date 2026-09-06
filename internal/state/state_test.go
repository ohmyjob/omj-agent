package state

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/atomicfile"
	"github.com/ohmyjob/omj-agent/internal/protocol"
)

var epoch = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

type fixture struct {
	path  string
	now   time.Time
	log   bytes.Buffer
	store *Store
}

func load(t *testing.T) *fixture {
	t.Helper()

	f := &fixture{path: filepath.Join(t.TempDir(), "state.json"), now: epoch}
	f.reload(t)

	return f
}

func (f *fixture) reload(t *testing.T) {
	t.Helper()

	loader := Loader{
		Now:    func() time.Time { return f.now },
		Logger: slog.New(slog.NewTextHandler(&f.log, nil)),
	}

	store, err := loader.Load(f.path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	f.store = store
}

func exitCode(code int) *int { return &code }

func TestLoadStartsEmptyWhenTheFileIsMissing(t *testing.T) {
	f := load(t)

	if got := f.store.Active(); len(got) != 0 {
		t.Errorf("Active() = %v, want none", got)
	}

	if _, ok := f.store.RecentOutcome("run-1"); ok {
		t.Error("RecentOutcome() found a run in an empty store")
	}

	if _, err := os.Stat(f.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load() created %s, want no file until the first save", f.path)
	}
}

func TestRunsSurviveAReload(t *testing.T) {
	f := load(t)

	if err := f.store.SetMachineID("machine-1"); err != nil {
		t.Fatal(err)
	}

	started := ActiveRun{RunID: "run-1", PID: 4242, PGID: 4242, StartedAt: epoch}
	if err := f.store.MarkActive(started); err != nil {
		t.Fatal(err)
	}

	if err := f.store.MarkActive(ActiveRun{RunID: "run-2", PID: 4243, PGID: 4243, StartedAt: epoch}); err != nil {
		t.Fatal(err)
	}

	f.now = epoch.Add(time.Minute)

	startedAt := epoch.Add(-time.Minute)
	if err := f.store.MarkFinished("run-2", Outcome{Status: "failed", ExitCode: exitCode(3), StartedAt: &startedAt}); err != nil {
		t.Fatal(err)
	}

	f.reload(t)

	if got := f.store.MachineID(); got != "machine-1" {
		t.Errorf("MachineID() = %q, want machine-1", got)
	}

	if got := f.store.Active(); len(got) != 1 || got[0] != started {
		t.Errorf("Active() = %v, want only %v", got, started)
	}

	if !f.store.IsActive("run-1") || f.store.IsActive("run-2") {
		t.Error("IsActive() disagrees with Active()")
	}

	recent, ok := f.store.RecentOutcome("run-2")
	if !ok {
		t.Fatal("RecentOutcome() did not find the finished run")
	}

	want := RecentRun{RunID: "run-2", Status: "failed", ExitCode: exitCode(3), StartedAt: &startedAt, FinishedAt: epoch.Add(time.Minute)}
	if recent.RunID != want.RunID || recent.Status != want.Status || *recent.ExitCode != *want.ExitCode || !recent.FinishedAt.Equal(want.FinishedAt) {
		t.Errorf("RecentOutcome() = %+v, want %+v", recent, want)
	}

	if recent.StartedAt == nil || !recent.StartedAt.Equal(startedAt) {
		t.Errorf("RecentOutcome().StartedAt = %v, want %s", recent.StartedAt, startedAt)
	}

	info, err := os.Stat(f.path)
	if err != nil {
		t.Fatal(err)
	}

	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("state file mode = %04o, want 0600", mode)
	}
}

func TestRecentRunsWrittenBeforeTheStartTimeStillLoad(t *testing.T) {
	f := load(t)

	legacy := `{"machine_id":"machine-1","active_runs":[],"recent_runs":[{"run_id":"run-1","status":"success","exit_code":0,"finished_at":"2026-09-04T12:00:00Z"}]}`
	if err := os.WriteFile(f.path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	f.reload(t)

	recent, ok := f.store.RecentOutcome("run-1")
	if !ok || recent.Status != "success" || recent.StartedAt != nil {
		t.Fatalf("RecentOutcome() = %+v, %v; want the legacy success without a start time", recent, ok)
	}
}

func TestRecentRunsWrittenBeforeTheReasonStillLoad(t *testing.T) {
	f := load(t)

	legacy := `{"machine_id":"machine-1","active_runs":[],"recent_runs":[{"run_id":"run-1","status":"failed","exit_code":null,"started_at":null,"finished_at":"2026-09-04T12:00:00Z"}]}`
	if err := os.WriteFile(f.path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	f.reload(t)

	if f.log.Len() != 0 {
		t.Fatalf("loading a file without a reason logged %q; want it read as written", f.log.String())
	}

	recent, ok := f.store.RecentOutcome("run-1")
	if !ok || recent.Status != "failed" || recent.Reason != nil {
		t.Fatalf("RecentOutcome() = %+v, %v; want the failure with no reason", recent, ok)
	}
}

func TestTheReasonARunEndedSurvivesAReload(t *testing.T) {
	tests := []struct {
		name   string
		status string
		reason *protocol.FinishReason
	}{
		{"a refused execution user", "failed", reasonPtr(protocol.ReasonRunAsNotPermitted)},
		{"a command that never started", "failed", reasonPtr(protocol.ReasonSpawnFailed)},
		{"a run the agent gave up on", "lost", reasonPtr(protocol.ReasonAgentRestarted)},
		{"an ending that explains itself", "success", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := load(t)

			if err := f.store.MarkFinished("run-1", Outcome{Status: tt.status, Reason: tt.reason}); err != nil {
				t.Fatal(err)
			}

			f.reload(t)

			recent, ok := f.store.RecentOutcome("run-1")
			if !ok {
				t.Fatal("RecentOutcome() did not find the finished run")
			}

			switch {
			case tt.reason == nil && recent.Reason != nil:
				t.Fatalf("Reason = %q, want none", *recent.Reason)
			case tt.reason != nil && recent.Reason == nil:
				t.Fatalf("Reason = none, want %q", *tt.reason)
			case tt.reason != nil && *recent.Reason != *tt.reason:
				t.Fatalf("Reason = %q, want %q", *recent.Reason, *tt.reason)
			}
		})
	}
}

// The stored reason must not alias the caller's, or a later write through the
// same pointer would rewrite history.
func TestTheStoredReasonIsACopy(t *testing.T) {
	f := load(t)

	reason := protocol.ReasonRunAsNotPermitted
	if err := f.store.MarkFinished("run-1", Outcome{Status: "failed", Reason: &reason}); err != nil {
		t.Fatal(err)
	}

	reason = protocol.ReasonSpawnFailed

	recent, ok := f.store.RecentOutcome("run-1")
	if !ok || recent.Reason == nil || *recent.Reason != protocol.ReasonRunAsNotPermitted {
		t.Fatalf("Reason = %v, want run_as_not_permitted", recent.Reason)
	}

	*recent.Reason = protocol.ReasonAgentStopped

	again, _ := f.store.RecentOutcome("run-1")
	if again.Reason == nil || *again.Reason != protocol.ReasonRunAsNotPermitted {
		t.Fatalf("Reason = %v after the caller wrote to it, want run_as_not_permitted", again.Reason)
	}
}

func reasonPtr(reason protocol.FinishReason) *protocol.FinishReason {
	return &reason
}

func TestMarkingKeepsOneEntryPerRun(t *testing.T) {
	f := load(t)

	for _, pid := range []int{1, 2} {
		if err := f.store.MarkActive(ActiveRun{RunID: "run-1", PID: pid, PGID: pid, StartedAt: epoch}); err != nil {
			t.Fatal(err)
		}
	}

	if got := f.store.Active(); len(got) != 1 || got[0].PID != 2 {
		t.Errorf("Active() = %v, want one run with the latest pid", got)
	}

	for _, status := range []string{"lost", "success"} {
		if err := f.store.MarkFinished("run-1", Outcome{Status: status, ExitCode: nil}); err != nil {
			t.Fatal(err)
		}
	}

	recent, ok := f.store.RecentOutcome("run-1")
	if !ok || recent.Status != "success" || recent.ExitCode != nil {
		t.Errorf("RecentOutcome() = %+v, want the latest outcome without an exit code", recent)
	}

	if f.store.IsActive("run-1") {
		t.Error("IsActive() still reports a finished run")
	}
}

func TestSaveCapsRecentRuns(t *testing.T) {
	tests := []struct {
		name string
		runs []RecentRun
		want []string
	}{
		{
			name: "by count",
			runs: recentRuns(MaxRecentRuns+1, epoch),
			want: runIDs(1, MaxRecentRuns+1),
		},
		{
			name: "by age",
			runs: []RecentRun{
				{RunID: "run-old", Status: "success", FinishedAt: epoch.Add(-RecentRunTTL - time.Second)},
				{RunID: "run-edge", Status: "success", FinishedAt: epoch.Add(-RecentRunTTL)},
				{RunID: "run-new", Status: "success", FinishedAt: epoch.Add(-time.Hour)},
			},
			want: []string{"run-edge", "run-new"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := load(t)
			f.store.contents.RecentRuns = tt.runs

			if err := f.store.Save(); err != nil {
				t.Fatal(err)
			}

			f.reload(t)

			var got []string
			for _, run := range f.store.contents.RecentRuns {
				got = append(got, run.RunID)
			}

			if len(got) != len(tt.want) || got[0] != tt.want[0] || got[len(got)-1] != tt.want[len(tt.want)-1] {
				t.Errorf("recent runs after Save() = %d entries from %q to %q, want %d from %q to %q", len(got), got[0], got[len(got)-1], len(tt.want), tt.want[0], tt.want[len(tt.want)-1])
			}
		})
	}
}

func recentRuns(count int, finishedAt time.Time) []RecentRun {
	runs := make([]RecentRun, 0, count)

	for i := range count {
		runs = append(runs, RecentRun{RunID: fmt.Sprintf("run-%d", i), Status: "success", ExitCode: exitCode(0), FinishedAt: finishedAt})
	}

	return runs
}

func runIDs(from, to int) []string {
	ids := make([]string, 0, to-from)

	for i := from; i < to; i++ {
		ids = append(ids, fmt.Sprintf("run-%d", i))
	}

	return ids
}

func TestLoadSetsAsideACorruptFile(t *testing.T) {
	f := load(t)

	if err := os.WriteFile(f.path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	f.reload(t)

	if got := f.store.Active(); len(got) != 0 {
		t.Errorf("Active() = %v, want an empty store", got)
	}

	moved := f.path + ".corrupt-20260904T120000Z"

	content, err := os.ReadFile(moved)
	if err != nil {
		t.Fatalf("the corrupt file was not set aside: %v", err)
	}

	if string(content) != "{not json" {
		t.Errorf("set-aside content = %q, want the corrupt content", content)
	}

	if !strings.Contains(f.log.String(), "corrupt") || !strings.Contains(f.log.String(), moved) {
		t.Errorf("log = %q, want a warning naming %s", f.log.String(), moved)
	}

	if err := f.store.MarkActive(ActiveRun{RunID: "run-1", PID: 1, PGID: 1, StartedAt: epoch}); err != nil {
		t.Fatalf("MarkActive() after a corrupt load error = %v", err)
	}

	f.reload(t)

	if !f.store.IsActive("run-1") {
		t.Error("the fresh state file was not written after the corrupt one was set aside")
	}
}

func TestSaveKeepsThePreviousStateWhenTheRenameFails(t *testing.T) {
	f := load(t)

	if err := f.store.MarkActive(ActiveRun{RunID: "run-1", PID: 1, PGID: 1, StartedAt: epoch}); err != nil {
		t.Fatal(err)
	}

	f.store.writer = atomicfile.Writer{Rename: func(string, string) error { return errors.New("disk on fire") }}

	err := f.store.MarkActive(ActiveRun{RunID: "run-2", PID: 2, PGID: 2, StartedAt: epoch})
	if err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("MarkActive() error = %v, want the rename failure", err)
	}

	f.reload(t)

	if got := f.store.Active(); len(got) != 1 || got[0].RunID != "run-1" {
		t.Errorf("Active() after the failed save = %v, want only run-1", got)
	}

	entries, err := os.ReadDir(filepath.Dir(f.path))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Errorf("state directory holds %d entries, want the temporary file removed", len(entries))
	}
}

func TestStoreIsSafeForConcurrentUse(t *testing.T) {
	f := load(t)

	const workers = 16

	var wg sync.WaitGroup

	for i := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			runID := fmt.Sprintf("run-%d", i)

			if err := f.store.MarkActive(ActiveRun{RunID: runID, PID: i, PGID: i, StartedAt: epoch}); err != nil {
				t.Error(err)
			}

			f.store.IsActive(runID)
			f.store.Active()

			if i%2 == 0 {
				if err := f.store.MarkFinished(runID, Outcome{Status: "success", ExitCode: exitCode(0)}); err != nil {
					t.Error(err)
				}

				f.store.RecentOutcome(runID)
			}
		}()
	}

	wg.Wait()

	f.reload(t)

	if got := len(f.store.Active()); got != workers/2 {
		t.Errorf("Active() has %d runs, want %d", got, workers/2)
	}

	if got := len(f.store.contents.RecentRuns); got != workers/2 {
		t.Errorf("recent runs = %d, want %d", got, workers/2)
	}
}

func TestResetAdoptsANewMachineAndKeepsThePreviousFile(t *testing.T) {
	f := load(t)

	if err := f.store.SetMachineID("machine-one"); err != nil {
		t.Fatal(err)
	}

	if err := f.store.MarkActive(ActiveRun{RunID: "run-1", PID: 7, PGID: 7, StartedAt: epoch}); err != nil {
		t.Fatal(err)
	}

	if err := f.store.MarkFinished("run-1", Outcome{Status: "success", ExitCode: exitCode(0)}); err != nil {
		t.Fatal(err)
	}

	if err := f.store.Reset("machine-two"); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}

	if got := f.store.MachineID(); got != "machine-two" {
		t.Errorf("MachineID() = %q, want machine-two", got)
	}

	if _, ok := f.store.RecentOutcome("run-1"); ok {
		t.Error("a run of the previous machine survived the reset")
	}

	moved := f.path + ".replaced-20260904T120000Z"
	if _, err := os.Stat(moved); err != nil {
		t.Errorf("the previous file was not kept: %v", err)
	}

	if !strings.Contains(f.log.String(), moved) {
		t.Errorf("log = %q, want a line naming %s", f.log.String(), moved)
	}

	f.reload(t)

	if got := f.store.MachineID(); got != "machine-two" {
		t.Errorf("MachineID() after a reload = %q, want machine-two", got)
	}

	if got := f.store.Active(); len(got) != 0 {
		t.Errorf("Active() after a reload = %v, want none", got)
	}
}

func TestResetWithoutAFileStartsClean(t *testing.T) {
	f := load(t)

	if err := f.store.Reset("machine-one"); err != nil {
		t.Fatalf("Reset() error = %v", err)
	}

	if got := f.store.MachineID(); got != "machine-one" {
		t.Errorf("MachineID() = %q, want machine-one", got)
	}

	if strings.Contains(f.log.String(), "could not be set aside") {
		t.Errorf("log = %q, want no warning when there was no file", f.log.String())
	}
}
