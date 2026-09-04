# 003 · Protocol types and fixtures

Status: done
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

## Outcome (2026-09-04)

- `internal/protocol` depends on the standard library and `internal/version` only, so it stays a plain mirror of `docs/agent-protocol-v1.md` and every other package can import it without a cycle. The conversions therefore live on the domain types: `sysinfo.Info.WorkMetadata(insecureHTTP)`, `sysinfo.Info.EnrollRequest(token, name, insecureHTTP)` (agent version from `internal/version`, an empty name is omitted so the Server defaults it to the hostname) and `output.Chunk.Protocol()`.
- `MachineMetadata` holds the nine Machine fields and is embedded in both `EnrollRequest` and `WorkRequest`, which is what keeps the two payloads identical as the document requires. `WorkMetadata` never yields a nil `reported_ips` because a nil slice encodes as `null` and the Server requires the list; `WorkRequest.ActiveRuns` must likewise be a non-nil slice, which task 011 owns.
- Times are `time.Time`. They marshal as RFC 3339 without trailing zeros (`2026-09-04T02:00:01Z`) while the Server emits microseconds; the document allows any RFC 3339 form from the Agent, and the round-trip tests compare timestamps by value rather than by text.
- Nullable fields are pointers without `omitempty` so they travel as `null`: `scheduled_for`, `shell`, `working_directory`, `exit_code`, `started_at`, `reason`. The optional `name` in `enroll` is a pointer with `omitempty` (omitted, never `null`), and the three optional members of `ErrorResponse` (`errors`, `supported_protocol_versions`, `min_agent_version`) are omitted when empty. `HeartbeatRequest` is an empty struct and marshals to `{}`. Chunk `data` is `[]byte`, so `encoding/json` does the base64.
- `RunStatus` carries all nine states of PRD §12 with `Valid()`, `Terminal()` and `Reportable()` (the five statuses `finish` accepts); `Trigger` (`scheduled`, `manual`) was added for leases; `ErrorCode` lists every code of the document, including the three enroll-only ones the Server adds in its task 014. `BasePath` and the three header names are constants next to `ProtocolVersion`.
- The fixtures are copied verbatim; a test fails when the directory and the round-trip table disagree, and every fixture is also decoded with an extra unknown field to prove the additive rule. `make sync-fixtures SERVER_DIR=<server checkout>` refreshes them and stops with a clear message when the directory is missing.
- No mismatch was found between the document, the fixtures and the Server's `AgentErrorCode`, `PingController` or `EnsureAgentProtocolIsSupported` beyond the enroll-only codes the Server does not define yet.

