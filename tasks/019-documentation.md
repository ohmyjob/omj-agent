# 019 · Documentation

Status: todo
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

- [ ] A reader can install and enroll an Agent using only these documents.
- [ ] Every flag, config key and exit code is documented.
- [ ] No document references any third-party example application.

## Tests

- Link check over `docs/` in CI.
