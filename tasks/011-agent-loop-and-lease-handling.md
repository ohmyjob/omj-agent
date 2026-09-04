# 011 · Agent loop and lease handling

Status: done
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

## Outcome (2026-09-04)

- `agent.New(Options)` composes the loaded `config.Config`, the `client.Client`, the `sysinfo.Info`, the `state.Store`, a `runner.Runner`, one shared `output.Buffer`, a logger, a clock, a `client.Backoff` and a `client.Sleeper`; the last four are the test injection points and every sleep the loop takes goes through the one sleeper. `Run(ctx)` returns nil once the context ends because a requested stop is not a failure; Runs in progress keep their goroutines and `Wait()` collects them. Task 013 adds the shutdown that cancels processes before waiting.
- Every lease goes through one order of checks: a run id this process already owns is acknowledged with `start` again and never executed twice; a run id with a stored outcome goes to the `Resender` hook; then `machine_id`, a run id still listed active by a previous agent process (task 013 reconciles those), expiry with 5 s of clock tolerance, command, timeout and output limit are verified, timeout and output are clamped to the local maxima; a lease that is also in `cancel_run_ids` is handed to the reporter as `CancelledBeforeStart` without `start`; a lease with no free slot is refused; then `start`, then spawn. Refusals are logged once per run id through a set capped at 1024 entries, since leases expire within a minute.
- `start` is retried with its own `client.Backoff` (sharing the Agent's random source and sleeper) until the lease expiry plus the tolerance; the four 409 codes and 404 drop the lease at info level, anything else warns. A spawn failure keeps the Run: its error text is written to the chunker as stderr and the chunker is closed at once so the text is already buffered when the reporter sees `Run.SpawnErr`.
- `slots` is `max_concurrent_runs` minus the owned Runs, capped at 16 and allowed to be 0: an Agent with every slot busy must keep polling because `work` is the Machine heartbeat and the cancellation channel. The protocol document says 1 to 16; server task 024 should accept 0 (reported to the coordinator). `active_runs` lists the Runs whose `start` was accepted and whose finish has not been, as a non-nil slice.
- Work errors: 401 logs "credential rejected; run omj-agent enroll again" and 426 logs the supported versions and minimum agent version, both sleeping `client.AuthRetryInterval`; retryable errors sleep the backoff or `Retry-After`, whichever is longer; any other rejection (a 422 would be a bug in the request) also backs off so the loop never spins. A successful poll resets the backoff and applies the `config` block: positive values only, `poll_wait_seconds` capped at the protocol's 25.
- `Reporter.Report(ctx, *Run)` and `Resender.Resend(ctx, lease, outcome)` are the hooks task 012 implements; the defaults wait for the process, record the outcome in the state file, close the chunker and forget the buffer. Reporters receive `context.Background()` because they outlive the polling context: an Agent that is stopping still has to tell the Server how its Runs ended. `OutcomeOf(runner.Result)` maps a result onto status, exit code and finish reason for both the reporter and the state file.
- `fakeserver_test.go` is an in-memory protocol Server for the package's tests: leases are queued one batch per `work` call, cancellations are delivered on the next call, failures are queued per endpoint, `RefuseStart` answers a Run's `start` with a conflict, `OnWork` observes each poll (before it is served) and is how tests stop the loop, and every `start`, `output`, `heartbeat` and `finish` is recorded for tasks 012 and 013.

