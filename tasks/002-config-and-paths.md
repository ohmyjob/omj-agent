# 002 · Config and paths

Status: todo
Repo: ohmyjob-agent
Depends on: 001
PRD: §16.3, §16.5 (local limits), §16.8, §21

## Goal

Load and save the Agent's configuration and credential safely, with all filesystem paths behind build tags.

## Scope

- `internal/config`: `Paths` struct (`ConfigDir`, `ConfigFile`, `CredentialFile`, `StateDir`, `StateFile`) from `paths_unix.go` (`//go:build unix`: `/etc/ohmyjob/agent.conf`, `/etc/ohmyjob/agent.credential`, `/var/lib/ohmyjob/state.json`). Environment overrides `OMJ_CONFIG_DIR` and `OMJ_STATE_DIR` for development and tests.
- `Config` fields and defaults from §16.3: `ServerURL`, `MachineID`, `InsecureHTTP` (false), `LogLevel` (`info`), `MaxConcurrentRuns` (4), `MaxTimeoutSeconds` (259200), `MaxOutputBytes` (104857600). Parser for `key = value` lines, `#` comments, blank lines, optional double quotes, unknown keys → error naming the line. `Validate()`: `ServerURL` must be `https://` unless `InsecureHTTP`; `MaxConcurrentRuns` 1–64; limits ≥ 1.
- `Save(cfg)` writes atomically (temp file in the same directory, `fsync`, rename) with mode 0640.
- `LoadCredential(paths)`: reads `agent.credential`, trims whitespace, refuses (with a clear message) unless the file mode is exactly 0600 and the owner is the current user; `SaveCredential` writes 0600 atomically. The credential type is `Credential` with a `String()` that returns `omj_agent_…` redacted to the prefix, so it can never be printed by accident.

## Files

- `internal/config/config.go`, `parse.go`, `paths_unix.go`, `credential.go`, `*_test.go`

## Acceptance criteria

- [ ] A config with `server_url = http://…` and no `insecure_http = true` fails validation with a message that mentions TLS.
- [ ] A 0644 credential file is refused and the message names the file and the expected mode.
- [ ] Saving is atomic (a crash between temp write and rename leaves the old file intact; test by simulating rename failure).

## Tests

- Table-driven parser tests, validation matrix, permission checks with temp dirs, atomic save.
