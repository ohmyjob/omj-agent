# 004 · HTTP client

Status: done
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

## Outcome (2026-09-04)

- `client.New(Options)` takes the server URL, the `config.Credential`, the insecure flag and, for tests, an `*http.Client`, a per-call `Timeout` (30 s) and the `LongPollHeadroom` (10 s). Paths are joined as `<server_url without trailing slash>/api/agent/v1/...`, so a path prefix survives verbatim; run ids are path-escaped. The default transport sets a 10 s dial and TLS handshake timeout and no client-wide timeout, because the long poll needs its own deadline: `Work` uses `wait_seconds + headroom`, every other call the `Timeout`, both as context deadlines that never extend a shorter one the caller set.
- Every call sends `Accept`, `User-Agent` (`version.UserAgent()`), the two version headers and, except `Enroll`, `Authorization: Bearer` from `Credential.Secret()`; `Content-Type` only goes with a body. A call other than `Enroll` on a client without a credential fails with `ErrNoCredential` before any network is touched, so `status` can say "not enrolled" plainly. `X-OMJ-Server-Version` is remembered from every response, errors included, for `status`.
- Any 2xx is success (`enroll` answers 201). A non-2xx body is decoded into `protocol.ErrorResponse`; when it is not JSON (a proxy page, an empty 502) the raw text becomes `Message` and `Code` stays empty. `*APIError` carries status, code, message, `Retryable` (5xx and 429 only), `RetryAfter` (seconds or HTTP date), the 422 `errors` map and the 426 extras; the `Is*` helpers match on status, `IsConflict` on status and code. `IsRetryable` also treats timeouts and transport failures (`net.Error`, unexpected EOF) as retryable and a cancelled context, an oversized response (`ErrResponseTooLarge`, 1 MiB cap) and a missing credential as final.
- `Backoff` is usable as its zero value (1 s to 60 s, factor 2, ±20 % jitter); `Rand` and `Sleep` are the injection points. Jitter is applied to the capped base, so a delay can reach 72 s. `Retry` reruns the operation while `IsRetryable` says so, waits the larger of the backoff and `Retry-After`, resets the backoff after a success, and when the context ends returns `errors.Join(ctx.Err(), lastErr)` so callers can inspect both. `AuthRetryInterval` (5 min) is exported for the 401 and 426 loop the agent implements in task 011; the client does not sleep for those itself.
- gosec G404 is excluded: the jitter uses `math/rand/v2` on purpose.

