# 013 · Shutdown and restart handling

Status: todo
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
