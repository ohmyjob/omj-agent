# 003 · Protocol types and fixtures

Status: todo
Repo: ohmyjob-agent
Depends on: 001
PRD: §8 (contract), §14 (all), §12 (states)

## Goal

Typed Go representations of every protocol message, verified against the Server's fixtures.

## Scope

- `internal/protocol`: structs with JSON tags for `EnrollRequest`, `EnrollResponse`, `PingResponse`, `WorkRequest`, `WorkResponse`, `RunLease`, `AgentConfig`, `StartRequest`, `StartResponse`, `OutputRequest`, `OutputChunk`, `OutputResponse`, `HeartbeatResponse`, `FinishRequest`, `FinishResponse`, `ErrorResponse`; typed string constants for `RunStatus`, `Stream`, `FinishReason`, `ActiveRunStatus`; `ProtocolVersion` constant; header name constants.
- Times are `time.Time` in RFC 3339; `data` in chunks is `[]byte` with base64 handled by `encoding/json`.
- Copy the Server fixtures into `internal/protocol/testdata/agent-protocol-v1/` and add `make sync-fixtures SERVER_DIR=…` to refresh them (fails if the directory is missing).
- Tests: each fixture unmarshals into its struct without unknown-field errors on required fields, marshals back to a JSON document semantically equal to the fixture (compare via `map[string]any`), and unknown extra fields are tolerated (additive rule from §14.3).

## Files

- `internal/protocol/types.go`, `enums.go`, `headers.go`, `types_test.go`, `testdata/agent-protocol-v1/*.json`, `Makefile`

## Acceptance criteria

- [ ] Every endpoint in the spec has request and response types.
- [ ] Round-trip tests pass for every fixture.

## Tests

- Fixture round-trips; enum validity helpers (`RunStatus.Terminal()`).
