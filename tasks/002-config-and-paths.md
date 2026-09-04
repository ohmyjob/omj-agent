# 002 · Config and paths

Status: done
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

## Outcome (2026-09-04)

- `Load(paths)` reads, parses and validates in one call and prefixes every error with the file path; `Parse(io.Reader)` is exported on its own so `status` and `doctor` can report a broken file without validating it. `Validate()` also checks `log_level` against `debug`, `info`, `warn`, `error`; `machine_id` is not required because the file exists before enrollment fills it in.
- Parser rules the task left open: comments are full-line only (a `#` inside a value is data), `key=value` without spaces is accepted, surrounding double quotes are stripped only when the value starts and ends with one, and a duplicate key is an error rather than last-wins.
- `Save(paths, cfg)` validates first, creates the configuration directory with mode 0750 when it is missing (development runs with `OMJ_CONFIG_DIR`), and writes the keys in a fixed order so the file round-trips through `Parse`. Ownership (`ohmyjob:ohmyjob`) is the installer's job; the agent only sets modes.
- One atomic writer (sibling temp file, fsync, rename) serves both the configuration and the credential; the rename is injectable so the test proves the old file survives a failed rename and the temp file is removed. Task 007 moved it to `internal/atomicfile` so the state store shares it.
- `Credential` is a struct with an unexported value: `NewCredential` enforces the `omj_agent_` prefix, `Secret()` is the only way to read the raw value, and `String()`, `GoString()` and `LogValue()` all print `omj_agent_…`, so `%v`, `%#v` and `slog` cannot leak it. The credential file must have mode exactly 0600 and be owned by the current user; both refusals name the file. The owner check reads `syscall.Stat_t` and therefore lives in `paths_unix.go`, the one file in this package allowed to import `syscall` (PRD §16.8).
- gosec now also excludes G304 (the agent opens files at paths it derives from its own configuration; none comes from a request) and G101 (field and file names contain the word credential; the secret itself is never a literal).

