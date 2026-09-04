# 007 · State file

Status: done
Repo: ohmyjob-agent
Depends on: 002
PRD: §16.7, §13, §16.6

## Goal

Durable memory of which Runs are active and which finished recently, so a `run_id` is never executed twice and outcomes can be re-reported.

## Scope

- `internal/state.Store` backed by `state.json`: `MachineID`, `ActiveRuns []ActiveRun{RunID, PID, PGID, StartedAt}`, `RecentRuns []RecentRun{RunID, Status, ExitCode, FinishedAt}`.
- Methods: `Load(path)` (missing file → empty store; corrupt file → rename to `state.json.corrupt-<timestamp>`, log a warning, start empty), `Save()` atomic (temp + fsync + rename, mode 0600), `MarkActive(run)`, `MarkFinished(runID, outcome)` (moves from active to recent), `IsActive(runID)`, `RecentOutcome(runID) (RecentRun, bool)`, `Active() []ActiveRun`.
- Caps applied on `Save`: keep at most 1000 recent entries and none older than 7 days.
- Concurrency-safe (mutex); all writes go through `Save`.

## Files

- `internal/state/state.go`, `state_test.go`

## Acceptance criteria

- [ ] Kill-safe: a simulated crash between temp write and rename leaves the previous state readable.
- [ ] Caps enforced by count and by age with a fake clock.
- [ ] Corrupt files never prevent start-up.

## Tests

- Round-trip, caps, corruption handling, concurrent access with `-race`.

## Outcome (2026-09-04)

- `MarkActive`, `MarkFinished` and `SetMachineID` persist immediately (they return the save error) rather than waiting for a separate `Save()` call: the store exists to survive a crash, so an in-memory-only mark would defeat it. `Save()` stays public for the periodic prune and for callers that want to force a write; every file write still goes through the one `save` path, which applies the caps first.
- The atomic writer moved from `internal/config` to `internal/atomicfile` (`Write(path, data, mode)` and a `Writer{Rename}` whose zero value is production), shared by the configuration, the credential and the state file; `internal/config/paths_unix.go` remains the only file importing `syscall`.
- `Loader{Now, Logger}` injects the clock and the logger the way `sysinfo.Collector` does; `Load(path)` uses the wall clock and `slog.Default()`. A missing file is an empty store and creates nothing until the first save (which also creates the state directory with mode 0750 for development runs under `OMJ_STATE_DIR`). A corrupt file is renamed to `state.json.corrupt-<UTC timestamp>` with a warning; if even that rename fails the agent still starts empty, because the next save overwrites the corrupt file anyway.
- `RecentRun.ExitCode` is `*int` (JSON `null`) because a lost or spawn-failed Run has no exit code, matching the nullable `exit_code` of the finish request. `Status` is a plain string until task 003 brings the protocol constants. Marking the same run twice keeps one entry with the latest values, so a repeated lease or a late finish is idempotent.
- Caps on save: entries with `finished_at` older than seven days are dropped first, then the newest 1000 are kept in insertion order (`MarkFinished` appends in finish order). `FinishedAt` is the clock at `MarkFinished`, which is what the stored `finish` replays after a restart.
- The file is written indented so an operator can read `/var/lib/ohmyjob/state.json`; empty lists encode as `[]`, never `null`.

