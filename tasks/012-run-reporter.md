# 012 · Run reporter

Status: todo
Repo: ohmyjob-agent
Depends on: 011
PRD: §14.2 (`output`, `heartbeat`, `finish`), §16.6 (replay after reconnect), §13, §19

## Goal

Per-Run goroutine that streams output, keeps the heartbeat, reacts to cancellation and delivers the final result even across disconnects.

## Scope

- `internal/agent.reporter` owns one `Process`, its `Chunker` and `Buffer`, and three duties driven by a `select` loop:
  - **output**: on flush, `client.AppendOutput` with `Buffer.NextBatch()`; on 2xx `AckUpTo(last_output_seq)`; if the Server reports `truncated`, stop sending output; `cancel_requested: true` → `Process.Cancel`; retryable errors → backoff, keep buffering; 409 `run_not_running`/404 → stop output, still attempt `finish`.
  - **heartbeat**: every `heartbeat_interval_seconds` call `client.Heartbeat`; `cancel_requested` → cancel; failures are logged and retried on the next tick (never kill the process for a failed heartbeat).
  - **finish**: when `Wait` returns, drain the chunker, then `client.FinishRun` with status (`success`/`failed`/`timed_out`/`cancelled`), `exit_code`, `started_at`, `finished_at`, `last_output_seq`, `output_truncated`, and `reason` (`spawn_failed` when the process never started, `agent_stopped` from task 013); retry with backoff until accepted or the Agent stops; then `state.MarkFinished`.
- `ResendOutcome(runID)` used by task 011 when a known Run is re-leased: sends `finish` from the recent record.
- Output ordering guarantee: batches are sent one at a time per Run; a failed batch is retried unchanged.

## Files

- `internal/agent/reporter.go`, `reporter_test.go`

## Acceptance criteria

- [ ] Against the fake Server, a Run whose output posts fail for 30 s then succeed delivers every byte exactly once in order.
- [ ] `cancel_requested` on a heartbeat or output response stops the process and finishes with `cancelled`.
- [ ] A `finish` that returns 409 `run_finished` with the same status is treated as success.
- [ ] Spawn failure finishes with `failed` and `reason: spawn_failed` and the error text as the single output chunk.

## Tests

- Fake Server with injectable failures; timing with fake tickers; ordering assertions.
