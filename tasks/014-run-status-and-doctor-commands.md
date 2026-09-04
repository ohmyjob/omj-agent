# 014 · run, status and doctor commands

Status: todo
Repo: ohmyjob-agent
Depends on: 013
PRD: §16.4, §16.3 (hardening detection), §14.2 (`ping`)

## Goal

Wire the daemon command and give operators two honest diagnostic commands.

## Scope

- `omj-agent run`: load config and credential, build client, state and agent, configure `slog` (text handler, level from config, `--log-level` override, `--log-format json`), run until signal. Exit codes: 0 clean stop, 1 configuration/credential error (message says which file), 2 usage.
- `omj-agent status`: prints config path, server URL, Machine id, user/uid, limits, `ping` result (server version, server time, clock skew), active Runs from state with PIDs and start times, and `systemctl is-active omj-agent` when systemd is present. Exit 0 even when the Server is unreachable (it is a report), but the line is marked `FAIL`.
- `omj-agent doctor`: one line per check with `PASS`/`WARN`/`FAIL`: config readable and valid; credential present, mode 0600, owner matches; state directory writable; Server reachable with valid TLS (or `WARN` for insecure HTTP); protocol and version accepted (`ping` not 426); clock skew under 30 s; running user matches the unit's `User=` when systemd is present; unit enabled and active; hardening directives active (`systemctl show omj-agent -p NoNewPrivileges,PrivateTmp,ProtectSystem,ProtectHome`, `WARN` when `NoNewPrivileges=yes` with the note that `sudo` inside Jobs will fail); `WARN` when running as root. Exit 1 if any `FAIL`.
- Checks are small functions returning `Check{Name, Status, Detail}` so they are unit-testable with fakes for the filesystem, client and `systemctl`.

## Files

- `internal/cli/run.go`, `status.go`, `doctor.go`, `internal/doctor/checks.go`, `checks_test.go`

## Acceptance criteria

- [ ] `doctor` exits 1 on a 0644 credential and prints the fix.
- [ ] `status` works offline and reports the Server as unreachable without stack traces.
- [ ] No command ever prints the credential.

## Tests

- Check functions with fakes; CLI output snapshots for a healthy and an unhealthy host.
