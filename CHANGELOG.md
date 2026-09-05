# Changelog

Notable changes to the Oh My Job Agent. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[semantic versioning](https://semver.org/spec/v2.0.0.html).

Each entry names the protocol version it speaks and the Server versions that
accept it. A change to the protocol version is a major release and says so
here; see [docs/releasing.md](docs/releasing.md).

## [Unreleased]

The first release is not tagged yet. Everything below is what `v0.1.0` will
contain. Protocol version 1, for Server 0.1.0 or newer.

### Added

- **Enrollment.** `omj-agent enroll` exchanges a single-use token for a
  machine identity and credential, writing `agent.conf` (0640) and
  `agent.credential` (0600) owned by the service user. Distinct exit codes for
  an invalid token, an expired token, an unsupported platform, a rejected
  version, throttling, an unreachable Server and a permission problem, so the
  installer can explain what went wrong. `--force` re-enrolls, `--name` sets
  the name shown in the UI, `--user` chooses the owner.
- **The agent loop.** Long-polling for work, honouring the concurrency limit,
  verifying every lease before it spawns anything, and reporting output,
  heartbeats and outcomes back. Retries with exponential backoff and jitter on
  network errors, `429` and `5xx`; a rejected credential or protocol is retried
  every five minutes so a re-enrolled machine recovers without a restart.
- **Execution.** Each Run gets its own process group and a clean environment
  built from scratch, with `HOME`, `USER`, `LOGNAME`, a fixed `PATH`, `LANG`
  and the Run's `OMJ_*` variables. Timeouts and cancellation kill the whole
  group, so a command that spawns children leaves nothing behind. Output is
  chunked, ordered and resent after an interruption without duplicates.
- **Local safeguards.** `max_concurrent_runs`, `max_timeout_seconds` and
  `max_output_bytes` in `agent.conf` cap what the Server can ask of a machine.
  Each Run is held to the smaller of the Job's limit and the machine's, and the
  effective values are reported to the Server.
- **Reliability.** A state file records Runs in flight, so a restart reports an
  interrupted Run as `lost` with `agent_restarted` rather than running it
  twice. A first signal drains and reports; a second exits at once. Runs the
  Server marked lost recover on the next heartbeat and are never leased twice.
- **Diagnostics.** `omj-agent status` reports the configuration, machine,
  limits, Server reachability, clock skew and active Runs, and always exits 0.
  `omj-agent doctor` runs ten checks with the fix for each and exits 1 on any
  failure. `omj-agent version` prints the version, commit, build time and
  protocol version.
- **Packaging.** Static `linux/amd64` and `linux/arm64` binaries, a systemd
  unit with an opt-in hardening block, and an installer that verifies the
  release against `SHA256SUMS`, creates the service user, installs the unit and
  enrolls the machine. `--uninstall` and `--purge` remove it again.
- **Security.** TLS is required unless `--insecure-http` is chosen explicitly,
  and the choice is visible on the Machine page for as long as it is set. The
  credential is read only from a `0600` file, never from a flag or the
  environment, and is redacted everywhere it could be printed.

[unreleased]: https://github.com/ohmyjob/omj-agent/commits/main
