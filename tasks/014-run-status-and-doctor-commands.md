# 014 · run, status and doctor commands

Status: done
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

## Outcome (2026-09-04)

- `internal/doctor.Host` is the one place that reads the machine (paths, uid and user, clock, config and credential loaders, state loader, a `Dial` that builds the protocol client, and a `Systemctl` runner); `status` and `doctor` share it, and tests replace the parts that would need root, a Server or systemd. `DefaultSystemctl()` shells out only when `systemctl` is on the PATH, so macOS and containers report "systemd not present" instead of failing.
- `doctor.Run` performs ten checks in the order an operator reads them: configuration, credential (mode 0600 and owner, the fix printed next to the failure), state directory writable (a temp file is created and removed), server, protocol, clock, service user, service, hardening, privileges. One ping feeds the server, protocol and clock checks; it is skipped, and those three say so, while the configuration or credential is broken. A 426 counts as reachable for the server check and fails the protocol check with the supported versions and the minimum agent version. Clock skew is measured against `server_time` at the moment the answer arrives and fails above 30 s.
- Statuses stay `PASS`, `WARN`, `FAIL`; anything systemd-related without systemd is a `WARN`, so the command is useful on a developer machine, and only `FAIL` sets the exit code to 1. A service user that differs from the doctor's user is a `WARN` rather than a `FAIL`, because operators run `sudo omj-agent doctor` while the service runs as `ohmyjob`. `NoNewPrivileges=yes` warns that `sudo` inside Jobs will fail; other active directives are listed.
- `status` is a report: it exits 0 whatever it finds, marks the lines it cannot fill with `FAIL` and a plain sentence (no stack trace), reads active Runs from the state file directly, and shows the ping result with the server version, server time and signed clock skew. Both commands bound the ping with a 15 s context.
- `run` wires `config.Load`, `config.LoadCredential`, `client.New`, `sysinfo.Collect`, `state.Load`, `runner.Runner{MaxTimeout}`, `output.NewBuffer` and `agent.New`, logs to stdout with `slog` (text or `--log-format json`, level from `log_level` unless `--log-level` overrides it), installs no signal handler of its own, and maps a clean stop to 0, a forced stop (`agent.ErrForcedStop`) to the run-specific exit code 3, a configuration, credential or wiring error to 1 with the file named, and bad flags to 2.
- No command prints the credential: the loaders return `config.Credential`, which redacts itself, and the output snapshots assert the secret is absent.

