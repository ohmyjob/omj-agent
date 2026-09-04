# 016 · Release pipeline

Status: done
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

## Outcome (2026-09-04)

- GoReleaser v2.18.0 is pinned in both workflows and in the Makefile: `make release-snapshot` uses a `goreleaser` on the PATH or falls back to `go run github.com/goreleaser/goreleaser/v2@v2.18.0`, so nothing is installed system-wide.
- Archives are exactly what the installer (task 015) expects: `omj-agent_<version>_linux_<arch>.tar.gz` with the binary at the root, `LICENSE`, `README.md` and `packaging/systemd/omj-agent.service` at that relative path, plus one `SHA256SUMS` in `hash  name` form. Builds are `CGO_ENABLED=0`, `-trimpath`, stripped, with the commit timestamp as the modification time so a rebuild of the same commit is reproducible.
- Snapshots are versioned `{{ incpatch .Version }}-next.<short commit>` (for example `0.0.1-next.98fbcad` while no tag exists): they can never collide with a real tag, `omj-agent version` shows what was built, and the installer's same-version check works with any such string.
- The changelog is grouped by conventional prefix (features, fixes, build and packaging, other) and drops `docs`, `test` and `chore` commits; `prerelease: auto` keeps release candidates away from `releases/latest`, which is what the installer resolves.
- `release.yml` runs on `v*` tags with `contents: write`, checks out the full history (the changelog needs the previous tag), runs `make test lint` before GoReleaser, and publishes with the built-in token. The existing `test.yml` gained a `release-snapshot` job that builds a snapshot on every push and pull request, verifies `SHA256SUMS`, and installs those archives inside Debian and Fedora through `make test-install RELEASE_DIR=dist`, so the PR gate proves the installer against what GoReleaser produces.
- `packaging/test/run.sh` works under `dist/install-test/` instead of wiping `dist/`, and with `RELEASE_DIR` it takes the archive for the Docker architecture and its checksum line from a GoReleaser output directory rather than packaging an ad hoc build.
- No tag exists yet. The first release is `git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0`; the workflow needs no repository setting because it requests `contents: write` itself. `docs/releasing.md` holds the version policy and the checklist.

