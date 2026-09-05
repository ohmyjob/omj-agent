# Releasing the Agent

Releases are built by GoReleaser when a GitHub Release is published. The
workflow in `.github/workflows/release.yml` runs the quality gates, builds
static binaries for `linux/amd64` and `linux/arm64`, and attaches the
archives and the `SHA256SUMS` file to that release. Pushing a tag on its own
builds nothing. The installer at `packaging/install.sh` (served from
ohmyjob.com) downloads the latest published release and verifies it against
`SHA256SUMS`.

The release notes are the ones you write: GoReleaser keeps the body it finds
on an existing release rather than generating its own. Write them from the
CHANGELOG entry, and remember the archives arrive a couple of minutes after
you publish, so anyone downloading in that window finds a release with no
files on it yet.

## Versions

- Tags are semantic versions prefixed with `v`: `v0.1.0`, `v1.2.3`,
  `v1.3.0-rc.1`. The version inside the binary (`omj-agent version`) and in
  the asset names is the tag without the `v`.
- Patch releases fix bugs and change no behaviour an operator relies on.
  Minor releases add features and stay compatible with the same protocol
  version. A change to `internal/version.ProtocolVersion` is a major
  release, and the release notes must say which Server versions accept it.
- Whenever the minimum or recommended Agent version on the Server changes,
  say so in the release notes; the Server refuses older Agents with 426 and
  flags outdated ones on the Machine page.
- A pre-release tag (`-rc.1`, `-beta.2`) is published as a GitHub
  pre-release and is not what `releases/latest` resolves to, so the
  installer keeps serving the previous stable version.

## Release notes

Write them from the CHANGELOG entry for the version, and say what the
grouping of commits never could: which Server versions accept this Agent,
and what an operator has to do to upgrade. GoReleaser's own commit grouping
is still configured but does not reach the release, because the release
already exists by the time it runs.

## Checklist

1. `main` is green: the `test` workflow passed on the commit to release,
   including the installer test and the snapshot build.
2. Check the changes since the last tag: `git log --oneline v0.1.0..main`.
3. Dry run locally when in doubt: `make release-snapshot` builds both
   archives and `SHA256SUMS` into `dist/`, and
   `make test-install RELEASE_DIR=dist` installs them inside Debian and
   Fedora containers.
4. Tag the commit, push the tag, and publish a release from it with the
   CHANGELOG entry as its notes:

   ```sh
   git tag -a v0.1.1 -m "v0.1.1"
   git push origin v0.1.1
   gh release create v0.1.1 --verify-tag --title "v0.1.1" \
     --notes-file notes.md
   ```

   Publishing is what starts the build. A pre-release tag (`-rc.1`) needs
   `--prerelease` so the installer keeps serving the previous stable
   version.

5. Watch the `release` workflow. It attaches nothing if a quality gate
   fails; fix the problem on `main`, then delete the release and the tag,
   and start again from a commit that passes.
6. Check the archives and `SHA256SUMS` are on the release.
7. Update `recommended_agent_version` (and `min_agent_version` when an
   older Agent must no longer connect) in the Server's `config/ohmyjob.php`
   so the Machines page and the protocol checks follow the release.

## Snapshots

Every pull request and push to `main` builds a snapshot release in CI
without publishing it. Snapshot versions look like `0.1.1-next.a1b2c3d`
(the next patch version plus the short commit), so a snapshot never
collides with a real release and `omj-agent version` shows exactly what
was built.
