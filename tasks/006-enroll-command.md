# 006 · Enroll command

Status: done
Repo: ohmyjob-agent
Depends on: 002, 004, 005
PRD: §15, §16.4, §16.3, §21

## Goal

`omj-agent enroll` turns a one-time token into a configured, credentialed Machine.

## Scope

- Flags: `--server URL` (required), `--token TOKEN` (required), `--name NAME` (optional friendly name), `--insecure-http` (allow `http://`, prints a warning), `--user NAME` (owner of the written files; defaults to `ohmyjob` when run as root and that user exists, otherwise the current user), `--force` (replace an existing enrollment).
- Behaviour: refuse when `agent.conf` already has a `machine_id` unless `--force`, explaining that the old Machine must be removed in the Server UI; collect `sysinfo`; call `Enroll`; write `agent.conf` (`server_url`, `machine_id`, `insecure_http`, keeping other keys if present) and `agent.credential`; `chown` both to `--user` when running as root; print a success line with the Machine id and the next step (`systemctl enable --now omj-agent` or `omj-agent run`).
- Error handling with distinct exit codes and plain messages: token invalid or used (401), expired (410), unsupported OS (422), protocol or version rejected (426), TLS or network failure, permission denied on `/etc/ohmyjob`.
- The token and credential are never logged, even at debug level.

## Files

- `internal/cli/enroll.go`, `internal/cli/enroll_test.go`, `internal/enroll/enroll.go` (pure logic separated from flag parsing)

## Acceptance criteria

- [ ] Against an `httptest` Server, enrolling writes both files with modes 0640/0600 and correct content.
- [ ] Re-running without `--force` exits non-zero with the explanatory message; with `--force` it replaces the files.
- [ ] Each Server error maps to its own message.

## Tests

- Fake Server matrix, filesystem assertions in a temp config dir, redaction check on log output.

## Outcome (2026-09-04)

- `internal/enroll.Enroll(ctx, Options)` holds the whole flow and returns a `Result` (machine id, both file paths, owner, next step); `internal/cli/enroll.go` only parses flags, prints the warning and the outcome, and maps the error to an exit code. Everything the host provides is injectable through `Options`: `Paths`, `Collect`, `HTTPClient`, `Logger` and a `System` value (`UID`, `Username`, `LookupUser`, `Chown`, `ServiceUnit`), so the tests never need root or the network.
- Failures are `*enroll.Error` values with a `Reason`; the CLI exit codes are 2 for bad input (missing flags, a token without the `omj_enroll_` prefix, plain `http://` without `--insecure-http`, an unknown `--user`), 3 already enrolled, 4 token invalid or used (401), 5 token expired (410), 6 unsupported operating system (422 `unsupported_os`), 7 protocol or agent version rejected (426, the message lists the supported protocol versions and the minimum agent version), 8 throttled (429, with the `Retry-After` wait when the server sends one), 9 unreachable (TLS verification failures get their own wording, other network errors and timeouts the generic one), 10 permission denied, and 1 for anything else (5xx, `validation_failed`, an unusable credential). They are exported from `internal/cli` for the installer.
- The token is single-use, so every local check runs before the server is called: the existing `machine_id` (refused without `--force`, naming the old id and the UI step), the owner resolution, and a write probe in the configuration directory (created with mode 0750 when missing). A failed enrollment therefore never leaves a Machine record behind on the server for a permission problem on this side.
- The existing `agent.conf` is read with `config.Parse` (not `Load`, which would reject a file without `server_url`) so `log_level` and the limits survive; only `server_url`, `machine_id` and `insecure_http` are set. An unreadable file is an error unless `--force`, which replaces it with a warning in the log.
- Owner rules: root without `--user` gives both files to `ohmyjob` when that user exists and keeps them otherwise; a plain user keeps the files and may only name itself with `--user`; naming someone else needs root. `chown` runs after both files are written, on the files only (the directory belongs to the installer).
- The next step is `systemctl enable --now omj-agent` when `/etc/systemd/system/omj-agent.service` exists and `omj-agent run` otherwise; the credential file is written with mode 0600 before success is reported, as the protocol document requires.
- Secrets never reach a log: the CLI logs at warn level only, the client logs method, path and status, and the credential is handled as `config.Credential`. A test captures a debug-level log and asserts neither the token nor the credential appears.

