# 016 · Release pipeline

Status: todo
Repo: ohmyjob-agent
Depends on: 015
PRD: §16.1 (GoReleaser, checksums), §26, §29

## Goal

Tagged releases produce reproducible static binaries with checksums that the installer and users verify.

## Scope

- `.goreleaser.yaml`: builds for `linux/amd64` and `linux/arm64`, `CGO_ENABLED=0`, `-trimpath`, ldflags for `version`, `commit`, `date`; archives named `omj-agent_{{.Version}}_linux_{{.Arch}}.tar.gz` containing the binary, `LICENSE`, `README.md`, `packaging/systemd/omj-agent.service`; `SHA256SUMS`; changelog grouped by conventional prefixes; snapshot builds for pull requests.
- `.github/workflows/release.yml`: on `v*` tags run tests, then GoReleaser with the GitHub token; upload artifacts to GitHub Releases. `make release-snapshot` for local dry runs.
- Version policy in `docs/releasing.md`: semver, protocol version changes documented in the release notes, tag → release checklist.

## Files

- `.goreleaser.yaml`, `.github/workflows/release.yml`, `Makefile` (`release-snapshot`), `docs/releasing.md`

## Acceptance criteria

- [ ] `make release-snapshot` produces both archives and a `SHA256SUMS` that verifies.
- [ ] `omj-agent version` from a snapshot shows the snapshot version and commit.
- [ ] The installer (task 015) works against a snapshot served locally.

## Tests

- CI dry-run on pull requests (`goreleaser release --snapshot --clean`).
