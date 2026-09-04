# 011 · Agent loop and lease handling

Status: todo
Repo: ohmyjob-agent
Depends on: 004, 005, 007, 009, 010
PRD: §14.2 (`work`, `start`), §16.5 (verification, clamping), §16.6 (backoff, 401/426), §13

## Goal

The main loop: ask for work, verify each lease, start it, honour cancellations and survive Server outages.

## Scope

- `internal/agent.Agent` with `Run(ctx)`: loop calling `client.Work` with `wait_seconds` from the last `config` block (default 25), `slots = MaxConcurrentRuns − active`, `active_runs` from the in-memory registry, and `sysinfo` metadata. On success reset backoff and apply the response `config` (heartbeat interval, flush interval, chunk size, poll wait).
- Lease verification per §16.5 before anything else: `machine_id` equals ours, `run_id` not active and not in `state.RecentOutcome`, `lease_expires_at` in the future (with 5 s clock tolerance), non-empty command, positive timeout; clamp `timeout_seconds` to `MaxTimeoutSeconds` and `max_output_bytes` to `MaxOutputBytes`. Failing leases are logged at warn and ignored; a lease for a `run_id` with a recent outcome triggers a re-send of that outcome (task 012 provides the call).
- On a verified lease: `client.StartRun` with the effective values; 409 `lease_expired`/`run_cancelled`/`run_finished` → drop; 2xx → `runner.Start`, `state.MarkActive`, hand the process to a reporter goroutine (task 012).
- `cancel_run_ids` → `Process.Cancel` for matching active Runs.
- Error handling: retryable errors → `Backoff.Next()` sleep; 401 → log "credential rejected; run `omj-agent enroll` again" and retry every 5 minutes; 426 → log the supported versions and retry every 5 minutes; `ctx` cancellation exits cleanly.
- `internal/agent/fakeserver_test.go`: an `httptest` implementation of the protocol with an in-memory state machine, reused by tasks 012–013.

## Files

- `internal/agent/agent.go`, `lease.go`, `registry.go`, `agent_test.go`, `fakeserver_test.go`

## Acceptance criteria

- [ ] A lease with a foreign `machine_id` is never started and is logged once.
- [ ] A lease for a `run_id` already active is acknowledged (`start` again) but not executed twice.
- [ ] `timeout_seconds: 999999` is sent to `start` as `effective_timeout_seconds: 259200`.
- [ ] After three 500s the loop sleeps 1 s, 2 s, 4 s (±20 %) and resets after a 200.

## Tests

- Fake Server scenarios above; slots accounting; cancel routing.
