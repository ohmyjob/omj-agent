# 007 · State file

Status: todo
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
