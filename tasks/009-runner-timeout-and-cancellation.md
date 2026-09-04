# 009 · Runner: timeout and cancellation

Status: done
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

## Outcome (2026-09-04)

- The local limits live on a `Runner{MaxTimeout, Grace}` value whose `Start` replaces the package function (`runner.Start` still works and uses the defaults): `MaxTimeout` defaults to `DefaultMaxTimeout` (72 h, the `max_timeout_seconds` default of `agent.conf`) and every lease timeout of zero or above it is clamped to it; `Grace` defaults to `DefaultGrace` (10 s) and is the knob the tests shorten. Task 011 builds the `Runner` from the loaded configuration.
- Reaping moved into a goroutine started by `Start`, so `Wait()` only waits on a channel; that is what lets the termination goroutine notice the exit during the grace period without anyone calling `Wait`. `Wait` stays idempotent and returns the same `Result`.
- The first termination wins: `Cancel(reason)` and the timer both go through one guarded path, a second call is ignored, and a call after the process has exited changes nothing, so `TimedOut` and `Cancelled` always say why the group was signalled and are both false for a natural exit. A process that exits on its own during the grace keeps its real exit code; one that ignores `SIGTERM` is killed after the grace and reports 137 (`128 + SIGKILL`).
- The cancel reason is surfaced as `Result.Reason` (empty for a timeout) so the reporter can send `cancel_requested` versus `agent_stopped` without a second channel.
- `terminate_unix.go` is the second and last file of the package allowed to import `syscall`: `SIGTERM` to `-PGID`, wait for the exit or the grace, then `SIGKILL` to `-PGID`; `ESRCH` means the group is already gone and is not an error, any other signalling error lands in `Result.Err`. The `groupAlive(pgid)` helper (`kill(-pgid, 0)`) is what the tests use to prove nothing survives.
- The cancellation tests wait for a `ready` marker printed after the script's `trap` line, because a signal that arrives while the shell is still starting kills it before the trap exists.

