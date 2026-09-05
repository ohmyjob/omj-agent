# Oh My Job Agent

[![Licence: MIT](https://img.shields.io/badge/licence-MIT-blue.svg)](LICENSE)

> Run this command, on this machine, at this time, and know what happened.

Oh My Job is a self-hosted job scheduler. This repository is the Agent: the
part that lives on each machine you want to schedule work on. It enrolls the
machine with your Server, asks it for work, runs the commands it is given as
its own unprivileged user, and reports the output, the exit code and the
timing back. The Server, where Jobs and schedules are defined, is
[`omj-server`](https://github.com/ohmyjob/omj-server).

It is one static binary with no dependencies, running under systemd. It opens
no ports and listens for nothing: every connection is outbound, so a machine
behind NAT or a firewall needs no holes punched in it.

## Install

Create the machine in your Server first — **Machines → Add Machine** shows the
command, with the token already filled in:

```sh
curl -fsSL https://ohmyjob.com/install.sh | sudo sh -s -- \
  --server https://jobs.home.example --token omj_enroll_xxxxx
```

That downloads the release for this platform, verifies it against
`SHA256SUMS`, creates the `ohmyjob` service user, installs the systemd unit,
enrolls the machine and starts the service. The Add Machine page notices when
it connects, within a few seconds.

Then check it:

```sh
omj-agent doctor
```

### Manual install

If you would rather not pipe a script into a shell, do the same thing by hand.
Download the archive and the checksums from
[Releases](https://github.com/ohmyjob/omj-agent/releases), then:

```sh
sha256sum -c SHA256SUMS --ignore-missing
tar -xzf omj-agent_<version>_linux_amd64.tar.gz

sudo install -m 0755 omj-agent /usr/local/bin/omj-agent
sudo useradd --system --user-group --home-dir /var/lib/ohmyjob \
  --no-create-home --shell /usr/sbin/nologin ohmyjob
sudo install -d -m 0750 -o ohmyjob -g ohmyjob /etc/ohmyjob
sudo install -d -m 0700 -o ohmyjob -g ohmyjob /var/lib/ohmyjob
sudo install -m 0644 packaging/systemd/omj-agent.service /etc/systemd/system/

sudo omj-agent enroll --server https://jobs.home.example --token omj_enroll_xxxxx
sudo systemctl daemon-reload && sudo systemctl enable --now omj-agent
omj-agent doctor
```

Verify the checksum before you unpack, not after. To run as an account that
already exists instead of `ohmyjob`, edit `User=` and `Group=` in the unit and
pass `--user` to `enroll`.

### Upgrading

Re-run the installer. It keeps the enrollment and replaces only the binary:

```sh
curl -fsSL https://ohmyjob.com/install.sh | sudo sh -s -- --no-enroll
```

### Removing

```sh
curl -fsSL https://ohmyjob.com/install.sh | sudo sh -s -- --uninstall
```

Add `--purge` to remove `/etc/ohmyjob` and `/var/lib/ohmyjob` as well. Remove
the machine in the Server too, or it will sit there showing as offline.

## Supported platforms

Linux on `amd64` and `arm64`, with systemd. The binary is static and
CGO-free, so the distribution does not matter; the installer is tested on
Debian and Fedora. There are no macOS or Windows builds.

systemd is not strictly required — `omj-agent run` works anywhere — but
without it nothing restarts the Agent after a reboot, so it is what the
installer sets up and what the documentation assumes.

## Documentation

| Document                                     | Covers                                                                 |
| -------------------------------------------- | ---------------------------------------------------------------------- |
| [Configuration](docs/configuration.md)       | Every `agent.conf` key, file locations and permissions, local safeguards |
| [Command line](docs/cli.md)                  | Every subcommand, flag and exit code, with sample output                |
| [Security](docs/security.md)                 | The trust model, the service user, privileged Jobs, hardening          |
| [Troubleshooting](docs/troubleshooting.md)   | Credential and protocol rejections, offline machines, lost Runs, clocks |
| [Releasing](docs/releasing.md)               | Tagging, GoReleaser, the release checklist                             |
| [Packaging](packaging/README.md)             | What the installer does, its options, testing it                       |

The wire contract between the two halves is
[the agent protocol](https://github.com/ohmyjob/omj-server/blob/main/docs/agent-protocol-v1.md),
in the Server repository.

## Security

The Agent runs commands defined elsewhere, so it is worth being clear about
what that costs. An administrator of your Server can run arbitrary commands on
every enrolled machine, as that machine's Agent user, with no approval step in
between. The boundary is the local operating-system user, and the Agent has no
way to raise it: nothing in the protocol can make a Run execute as anybody
else.

So keep the `ohmyjob` user's reach small, grant privileges with narrow `sudo`
rules rather than by running as root, and read
[docs/security.md](docs/security.md) before putting this on a network.

Vulnerabilities go to `security@ohmyjob.com` — see [SECURITY.md](SECURITY.md).

## Development

Requirements: Go (the version in `go.mod`), `golangci-lint`, `shellcheck`, and
Docker for the installer and end-to-end suites.

```sh
make build
make test lint
```

`make test` runs the unit suites with the race detector; `make lint` checks
formatting, `go vet`, `golangci-lint` and the shell scripts. Both must pass
before a pull request.

The heavier suites are separate because they need Docker:

```sh
make test-install                        # installs a packaged build in Debian and Fedora
make server-image SERVER_DIR=../omj-server
make e2e                                 # two real Agents against a real Server
```

`make e2e` is not part of CI on every push — it runs from the `e2e` workflow on
demand, with a `repetitions` input of 1 or 3. Run it before a release and after
any change to the agent loop, the reporter or the scheduler contract.

## Licence

MIT. See [LICENSE](LICENSE).
