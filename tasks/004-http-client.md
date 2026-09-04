# 004 · HTTP client

Status: todo
Repo: ohmyjob-agent
Depends on: 002, 003
PRD: §14.1, §14.3, §16.6 (backoff, 401/426 handling), §21 (TLS)

## Goal

One client that speaks the protocol, maps errors to types the Agent can act on, and retries safely.

## Scope

- `internal/client.Client` built from `ServerURL`, `Credential`, `version.UserAgent()`, an `http.Client` with sane timeouts (`Timeout` per call via context; long-poll calls use `wait + 10 s`). Adds `Authorization`, `X-OMJ-Protocol-Version`, `X-OMJ-Agent-Version`, `Content-Type`, `Accept`. Refuses `http://` unless `InsecureHTTP`.
- Methods: `Enroll(ctx, req)` (no auth), `Ping(ctx)`, `Work(ctx, req)`, `StartRun(ctx, runID, req)`, `AppendOutput(ctx, runID, req)`, `Heartbeat(ctx, runID)`, `FinishRun(ctx, runID, req)`.
- Errors: `*APIError{Status, Code, Message, Retryable}` with sentinel helpers `IsUnauthorized`, `IsUnsupportedProtocol` (426), `IsConflict(code)`, `IsNotFound`, `IsPayloadTooLarge`, `IsThrottled`; network errors and 5xx are retryable; 4xx are not (except 429 with `Retry-After`).
- `internal/client.Backoff`: 1 s → 60 s, factor 2, ±20 % jitter from an injectable `rand`; `Reset()`; `Next() time.Duration`. `Retry(ctx, backoff, op)` for idempotent operations that stops on non-retryable errors and honours `ctx`.
- Response bodies capped at 1 MiB; `X-OMJ-Server-Version` captured on the client for `status`.

## Files

- `internal/client/client.go`, `errors.go`, `backoff.go`, `*_test.go`

## Acceptance criteria

- [ ] All headers present on every request in an `httptest` recorder.
- [ ] 401, 409 (each code), 426, 413, 429 map to the right error type and `Retryable` flag.
- [ ] Backoff sequence with a fixed random source is exactly reproducible in a test; jitter never exceeds ±20 %.

## Tests

- `httptest` server per method; error mapping table; backoff and retry with a fake sleeper.
