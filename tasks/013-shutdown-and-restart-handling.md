# 013 · Shutdown and restart handling

Status: done
Repo: ohmyjob-agent
Depends on: 012
PRD: §16.6 (SIGTERM, restart), §16.3 (`KillMode`, `TimeoutStopSec`), §12

## Goal

Stop honestly and start honestly: report what happened to Runs when the Agent goes down or comes back.

## Scope

- Signal handling in `Agent.Run`: on `SIGTERM`/`SIGINT` stop polling, cancel every active `Process` (task 009 termination), wait for reporters to send `finish` with `status: cancelled, reason: agent_stopped` within a 20 s budget (fits `TimeoutStopSec=30`), persist state, exit 0. A second signal exits immediately.
- Start-up reconciliation: for each `state.ActiveRuns` entry, send `finish` with `status: lost, reason: agent_restarted` (retry in the background with backoff; do not block polling), then `state.MarkFinished` with status `lost`. No process reattachment in v1.
- Re-lease handling: if the Server leases a `run_id` present in `RecentRuns`, respond by re-sending the stored outcome (`ResendOutcome`) and never start it.
- Startup log line summarising server URL, Machine id, user, limits and active-run count (no credential).

## Files

- `internal/agent/signals.go`, `startup.go`, `signals_test.go`, `startup_test.go`

## Acceptance criteria

- [ ] `SIGTERM` during a running command → process group gone, Server received `cancelled`/`agent_stopped`, exit code 0 within 20 s.
- [ ] Starting with a stale `ActiveRuns` entry → Server receives `lost`/`agent_restarted` for it, polling starts immediately.
- [ ] A re-leased finished `run_id` produces a `finish` call and no process.

## Tests

- Fake Server; signal delivery to the test process via `syscall.Kill` in a subprocess test, or an injected signal channel.

## Outcome (2026-09-04)

- `Run` now does three things in order: it logs the startup line, reconciles the state file, then polls; a stop signal ends the polling and `Run` gives the reporters the stop budget before returning. `Options.Signals` injects the channel (tests send `os.Interrupt`); when it is nil `Run` subscribes to `StopSignals()`, which lives in `signals_unix.go` because the signal names are the one OS-specific thing in the package, mirroring the build-tag rule of §16.8. `Options.StopBudget` defaults to `DefaultStopBudget` (20 s), inside the unit's `TimeoutStopSec=30`.
- The first signal cancels every running process with the `agent_stopped` reason (`OutcomeOf` turns that into `cancelled`/`agent_stopped`), stops polling, and once the loop has returned cancels again so a lease accepted while the signal was handled is covered too. Then `drain` waits for the reporters, saves the state and returns nil; when the budget elapses it ends the reporters' context and returns nil anyway, leaving the undelivered outcomes in the state file; a second signal ends the wait at once and `Run` returns `ErrForcedStop`, which the `run` command maps to a non-zero exit.
- Reporters run on a context the Agent owns instead of `context.Background()`, so a spent stop budget is what bounds their finish retries; on a normal context end they still outlive `Run` and `Wait` collects them, as before.
- Start-up reconciliation records each stale active entry as `lost` in the state file first and delivers the `finish` (`lost`, `agent_restarted`, `exit_code` null, `started_at` from the entry) on a background goroutine with its own backoff, so polling never waits for the Server and a lease for that run id meanwhile gets the stored outcome through `Resend`. `409 run_finished` counts as delivered, `not_leased` and `404` drop it, anything else is logged and the outcome stays for the next lease.
- `state.RecentRun` and `state.Outcome` gained `StartedAt *time.Time` (`started_at`, additive: older files load with it null), the reporter records it and `Resend` sends it, so a re-sent finish no longer claims the process never started.
- The startup line logs the server URL, Machine id, user and uid, the three local limits and the active-run count; the credential is never in the Agent's hands (the client holds it and redacts it), and a test asserts it does not reach the log.
- The fake Server gained `HoldFinish()` so tests can watch what happens while an outcome is in flight; the harness gained an injectable signal channel and stop budget and reports a user and uid in its `sysinfo.Info`.

