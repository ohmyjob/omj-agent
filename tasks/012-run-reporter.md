# 012 · Run reporter

Status: done
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

## Outcome (2026-09-04)

- `agent.reporter` is both the `Reporter` and the `Resender` and is what `agent.New` wires when none is injected. `Options.Ticker` is the one new injection point: it hands out the flush and heartbeat channels, so tests drive them by hand while production uses `time.Ticker`.
- One goroutine per Run runs a `select` over the process exit, the flush tick, the heartbeat tick and a retry signal. A retryable output failure never blocks that loop: the backoff sleep runs on its own goroutine (through the Agent's sleeper) and wakes the loop with the retry signal, so heartbeats and the exit are noticed during an outage. While `waiting` no tick sends output; after a failure the reporter is `disconnected` and the next output attempt sends a heartbeat first, because a Run the Server marked lost accepts output only once a heartbeat moved it back to running.
- Output stops for good on `409 run_not_running`, `404`, a `truncated` answer or any other client-side rejection (a 413 or 422 would be a bug, and retrying the same batch forever would spin); heartbeats stop on `409 run_not_running` or `404`. Neither ever touches the process: a failed heartbeat is logged and retried on its next tick, and the only thing that stops a process is `cancel_requested` from an output or heartbeat answer, handed to `Process.Cancel` with the `cancel_requested` reason.
- Finish waits for the process, closes the chunker, drains what is still buffered through `client.Retry` (batches unchanged, in order, at most one in flight per Run), then sends `finish` through `client.Retry` until the Server takes it. `409 run_finished` counts as delivered (the Server's record stands); `409 not_leased` and `404` drop the outcome; the context ending leaves it undelivered. In every case the outcome is then recorded in the state file and the buffer forgets the Run, so a lost finish is sent again through `Resend` when the Server re-leases the run id.
- The finish payload follows the document's table: a spawn failure is `failed` with `spawn_failed`, `exit_code` and `started_at` null, and its error text as the only chunk; a lease cancelled before it started is `cancelled` with both null and no `start`; everything else comes from `OutcomeOf`, `started_at` is the Agent's clock at spawn and `finished_at` the clock when the outcome was determined; `output_truncated` is true when the chunker, the buffer or the Server cut the output.
- `Resend` makes one attempt (the polling loop calls it, and the Server asks again if the answer is lost), treats `409 run_finished` as delivered, sends `agent_restarted` as the reason when the stored status is `lost`, and sends `started_at` null because the recent record keeps no start time; task 013 may extend `RecentRun` if the Server wants it.
- Reporters still run on the background context task 011 gave them; every request honours the context so task 013's shutdown can end them, but nothing ends them yet.
- The fake Server gained `RequestCancel(runID)` and `TruncateOutput(runID)`; the harness gained a `fakeTicker` keyed by interval and `tickUntil`, and passes `Resender` only when a test wants the recording one.

