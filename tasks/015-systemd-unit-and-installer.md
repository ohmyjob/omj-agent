# 015 · systemd unit and installer

Status: done
Repo: ohmyjob-agent
Depends on: 006, 014
PRD: §15 (enrollment command), §16.1, §16.3 (files, user, unit, hardening), §29

## Goal

The unit file and the one-line installer that ohmyjob.sh serves.

## Scope

- `packaging/systemd/omj-agent.service`: `[Unit]` after `network-online.target`; `[Service]` `User=ohmyjob`, `Group=ohmyjob`, `ExecStart=/usr/local/bin/omj-agent run`, `Restart=always`, `RestartSec=5`, `KillMode=control-group`, `TimeoutStopSec=30`, `WorkingDirectory=/var/lib/ohmyjob`; a commented, documented hardening block (`NoNewPrivileges=true`, `PrivateTmp=true`, `ProtectSystem=full`, `ProtectHome=read-only`) with one comment line per directive stating what it breaks (§16.3); `[Install]` `WantedBy=multi-user.target`.
- `packaging/install.sh` (POSIX `sh`, passes `shellcheck`): flags `--server`, `--token`, `--name`, `--user` (default `ohmyjob`), `--version` (default latest), `--insecure-http`, `--no-enroll`, `--uninstall`. Steps: require root; detect `linux` and `amd64`/`arm64` (else a clear error mentioning supported platforms); download `omj-agent_<version>_linux_<arch>.tar.gz` and `SHA256SUMS` from GitHub Releases of `ohmyjob/ohmyjob-agent`, verify with `sha256sum -c`, install the binary to `/usr/local/bin` 0755; create the system user/group with home `/var/lib/ohmyjob` and shell `/usr/sbin/nologin` (works with `useradd` and `adduser` variants), or verify an existing `--user`; create `/etc/ohmyjob` (0750) and `/var/lib/ohmyjob` (0700) owned by that user; install the unit with `User=`/`Group=` substituted; run `omj-agent enroll --user …` when `--token` is given; `systemctl daemon-reload && systemctl enable --now omj-agent`; print `omj-agent doctor` output. Idempotent on re-run; `--uninstall` stops and removes the unit and binary but keeps `/etc/ohmyjob` unless `--purge`.
- Root warning when `--user root`.
- CI job runs the script inside Debian and Fedora containers against a local file server standing in for GitHub Releases (`--base-url` hidden flag) with `--no-enroll`, then asserts files, ownership, modes and unit contents.

## Files

- `packaging/systemd/omj-agent.service`, `packaging/install.sh`, `.github/workflows/test.yml` (install job), `packaging/README.md`

## Acceptance criteria

- [ ] Fresh Debian container: script installs, creates the user with a non-login shell, and `systemctl cat omj-agent` shows the substituted user.
- [ ] Checksum mismatch aborts before anything is installed.
- [ ] Re-running is a no-op apart from version upgrades.

## Tests

- `shellcheck`; container-based install tests in CI.

## Outcome (2026-09-04)

- The unit comes out of the release archive (`packaging/systemd/omj-agent.service` inside the tarball, task 016 packages it there); on a re-run at the same version the installed unit file is the source instead, so `--user` can change without a download. `User=` and `Group=` are substituted with `sed`; the unit directory is created when missing so containers without systemd still install cleanly.
- Idempotence compares the requested version with what `omj-agent version` reports: equal means no download and no binary write, only the unit refresh and the service state; a different version replaces the binary and `try-restart`s the service. `latest` resolves through the GitHub releases API; `--base-url` (hidden, for tests) expects a flat directory holding the tarball and `SHA256SUMS` and requires `--version`.
- The checksum is verified with `sha256sum -c` on the asset's own line before anything is touched; a mismatch or a missing entry aborts with nothing installed, which the container test proves.
- The service user is created with `useradd` (busybox `adduser` as the fallback), a non-login shell picked from `/usr/sbin/nologin`, `/sbin/nologin` or `/bin/false`, and home `/var/lib/ohmyjob`. `--user` must name an existing account (refused otherwise, before any change); `--user root` is accepted with the warning §16.3 asks for. Both directories are owned by that user, `/etc/ohmyjob` 0750 and `/var/lib/ohmyjob` 0700.
- Enrollment runs `omj-agent enroll --user <user>` and passes its exit code through with a hint per code; the binary and unit stay installed, so the operator only needs a fresh token. The service is enabled and started only once `agent.conf` carries a `machine_id`, so an install with `--no-enroll` (or a failed enrollment) never leaves a crash-looping unit; without systemd the script says how to run the Agent by hand. `omj-agent doctor` is printed when the machine is configured.
- `--uninstall` disables the unit, removes the unit and binary and keeps both directories unless `--purge`; the account is never removed.
- Tests: `make test-install` builds the binary for the Docker architecture, packages the archive the way task 016 will, writes `SHA256SUMS` and a deliberately wrong copy, and runs `packaging/test/install-test.sh` as root in `debian:bookworm-slim` and `fedora:41` with a `file://` directory standing in for GitHub Releases (curl fetches it through the same code path as https). The CI job `install` runs the same target; `shellcheck` is part of `make lint`.

