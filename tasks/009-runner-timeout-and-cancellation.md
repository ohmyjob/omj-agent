# 009 · Runner: timeout and cancellation

Status: todo
Repo: ohmyjob-agent
Depends on: 008
PRD: §16.5 (process group termination), §19, §12 (`timed_out`, `cancelled`)

## Goal

Stop a Run and everything it spawned, on timeout or on request, and report which one happened.

## Scope

- `Process.Cancel(reason)` and the internal timeout timer both call `terminateGroup()`: `SIGTERM` to `-PGID`, wait up to 10 s, then `SIGKILL` to `-PGID`; wait for `Wait` to return.
- `Result.TimedOut` when the timer fired; `Result.Cancelled` when `Cancel` was called first; both false for natural exits. Exit code reporting unchanged (`128 + signal`).
- Timeout is measured from process start; `Timeout` zero means the local maximum (never unlimited).
- Descendant verification helper for tests: after termination, no process in the group remains (`kill(-pgid, 0)` returns `ESRCH`).

## Files

- `internal/runner/terminate_unix.go`, `runner.go` (timer wiring), `terminate_test.go`

## Acceptance criteria

- [ ] `sh -c 'sleep 300 & sleep 300 & wait'` cancelled → all three processes gone within 11 s, `Cancelled` true.
- [ ] A script that traps and ignores `SIGTERM` is killed by `SIGKILL` after the grace period, `TimedOut` true when it was a timeout.
- [ ] A process that exits during the grace period is reported with its real exit code.

## Tests

- Process-tree tests with pgid probing; timing tests with a real but short grace (inject the grace duration).
