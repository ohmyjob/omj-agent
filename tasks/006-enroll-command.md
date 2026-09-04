# 006 · Enroll command

Status: todo
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
