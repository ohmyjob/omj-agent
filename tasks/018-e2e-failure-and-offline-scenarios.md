# 018 · End-to-end failure and offline scenarios

Status: todo
Repo: ohmyjob-agent
Depends on: 017
PRD: §28 (end-to-end list), §30 items 12, 14–20, §17, §19

## Goal

Prove the reliability promises with real network partitions, restarts and clocks.

## Scope

Each scenario is its own test with a clear name and uses the harness from task 017:

- **Timeout**: `timeout_seconds = 5`, command `sleep 60` → `timed_out` within 20 s; `pgrep` inside the container shows no `sleep`.
- **Cancellation with children**: `sh -c 'sleep 300 & sleep 300 & wait'`, cancel from the UI → `cancelled`; no `sleep` survives.
- **Network interruption during a Run**: start a 40 s command that prints every second, `docker network disconnect` the Agent for 20 s, reconnect → Run ends `success` with all 40 lines present in order and no duplicates.
- **Offline within grace (`run_late`)**: disconnect B, create a Job on B due next minute with `grace_seconds = 300`, wait past the due minute, reconnect → Run starts late, `scheduled_for` is the due minute, the Run page says it started late because B was offline.
- **Offline beyond grace**: same with `grace_seconds = 60` and a two-minute disconnect → `missed` with `grace_period_elapsed`.
- **Skip policy**: `missed_policy = skip`, Machine offline at due time → `missed` with `machine_offline` immediately.
- **Coalescing**: every-minute Job, disconnect for 3 minutes, reconnect → exactly one Run with `coalesced_count >= 2`.
- **Protocol rejection**: run an Agent with the hidden test-only environment variable `OMJ_TEST_PROTOCOL_VERSION=99` (add it to `internal/version`, honoured only when set) → Machine shows Incompatible, Agent logs the supported versions, no leases.
- **Agent restart mid-Run**: `docker restart agent-a` during a 60 s command → Run becomes `lost` with `agent_restarted`; a subsequent Run now executes once (no duplicate for the old `run_id`).
- **Duplicate lease refusal**: with `OMJ_RUN_LOST_AFTER_SECONDS=60`, block heartbeats for 70 s (disconnect) so the Server marks the Run lost, reconnect → Run returns to `running` via heartbeat and ends `success`; the Server never re-leases it and the Agent never starts it twice.

## Files

- `test/e2e/failure_test.go`, `offline_test.go`, `compat_test.go`, `internal/version/version.go` (test-only override)

## Acceptance criteria

- [ ] All scenarios pass in CI three times in a row (no flakes).
- [ ] Each scenario asserts both the Server state and the absence of stray processes where relevant.

## Tests

- The scenarios above.
