# 019 · Documentation

Status: done
Repo: ohmyjob-agent
Depends on: 016, 018
PRD: §15, §16.3, §16.4, §21, §29

## Goal

Everything an operator needs to install, run, secure and troubleshoot the Agent, written for the repo README and ohmyjob.com.

## Scope

- `README.md`: what the Agent does in one paragraph, the enrollment one-liner from §15, manual install (download, verify `SHA256SUMS`, create user, unit), supported platforms, links.
- `docs/configuration.md`: `agent.conf` reference with defaults and the meaning of `max_timeout_seconds` and `max_output_bytes` as local safeguards; file locations and required permissions; environment overrides.
- `docs/cli.md`: every subcommand, flag and exit code; sample `status` and `doctor` output.
- `docs/security.md`: the trust model from §21, what the service user can and cannot do, the clean per-Run environment, the `sudo` pattern for privileged Jobs with a worked example, the opt-in hardening block and what each directive breaks, running as another user, running as root and why the UI warns, disclosure to `security@ohmyjob.com`.
- `docs/troubleshooting.md`: credential rejected, protocol rejected, Machine shows offline, Run marked lost, output truncated, clock skew.
- `SECURITY.md`, `CHANGELOG.md` with an Unreleased section.

## Files

- `README.md`, `docs/*.md`, `SECURITY.md`, `CHANGELOG.md`

## Acceptance criteria

- [x] A reader can install and enroll an Agent using only these documents.
- [x] Every flag, config key and exit code is documented.
- [x] No document references any third-party example application.

## Tests

- Link check over `docs/` in CI.

## Outcome (2026-09-05)

- Seven files: `README.md`, `docs/configuration.md`, `docs/cli.md`,
  `docs/security.md`, `docs/troubleshooting.md`, `SECURITY.md` and
  `CHANGELOG.md`. `docs/releasing.md` and `packaging/README.md` already covered
  releasing and the installer, so both are linked from the README index rather
  than restated.
- The sample `status` and `doctor` output is real, captured by running the
  built binary against a stub that answers `GET /api/agent/v1/ping`, not
  written by hand. The healthy lines for a systemd host over TLS are the same
  format strings with the values that host produces.
- Every `agent.conf` key, every subcommand flag, every installer flag and all
  eleven exit codes are covered, checked by grep rather than by eye. Every
  relative link and the one heading anchor resolve.
- `CHANGELOG.md` puts the first release under `Unreleased` because no version
  is tagged yet: the repository has no tags, while the Server's
  `min_agent_version` and `recommended_agent_version` are already `0.1.0`.
  Tagging `v0.1.0` is what closes that gap, and the Unreleased section becomes
  its notes.
- The Server repository runs lychee over `*.md` and `docs/**/*.md` in CI; this
  repository has no equivalent job. Worth adding for parity, but it is CI
  rather than documentation, so it was left out of this task.
