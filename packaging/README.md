# Installing the Agent

The Add Machine page in your Server shows the one-line installer. It downloads the release
that matches your platform, verifies its checksum, creates the `ohmyjob` service user,
installs the systemd unit and enrolls the machine:

```sh
curl -fsSL https://ohmyjob.com/install.sh | sudo sh -s -- --server https://jobs.example --token omj_enroll_...
```

`install.sh` in this directory is that script. It is safe to re-run: an existing
installation at the same version is left alone, a newer `--version` replaces the binary
and restarts the service.

## Options

| Option            | Meaning                                                                   |
| ----------------- | ------------------------------------------------------------------------- |
| `--server URL`    | Server URL, exactly as the Add Machine page shows it                      |
| `--token TOKEN`   | One-time enrollment token from the Add Machine page                       |
| `--name NAME`     | Friendly name for this machine; defaults to the hostname                  |
| `--user NAME`     | Run the Agent as this existing user instead of `ohmyjob`                  |
| `--version V`     | Release to install; defaults to the latest GitHub release                 |
| `--insecure-http` | Allow a plain `http://` Server URL                                        |
| `--no-enroll`     | Install without enrolling; run `omj-agent enroll` afterwards              |
| `--uninstall`     | Stop and remove the service and the binary                                |
| `--purge`         | With `--uninstall`, also remove `/etc/ohmyjob` and `/var/lib/ohmyjob`     |

The script needs root, `curl` or `wget`, `sha256sum` and `tar`. It supports Linux on
amd64 and arm64 and refuses anything else.

## What it does

1. Downloads `omj-agent_<version>_linux_<arch>.tar.gz` and `SHA256SUMS` from the
   GitHub release and checks the archive against the listed hash before touching the
   system.
2. Installs the binary to `/usr/local/bin/omj-agent` (root, 0755).
3. Creates the `ohmyjob` system user and group with home `/var/lib/ohmyjob` and a
   non-login shell, or verifies the user given with `--user`.
4. Creates `/etc/ohmyjob` (0750) and `/var/lib/ohmyjob` (0700) owned by that user.
5. Installs `/etc/systemd/system/omj-agent.service` with `User=` and `Group=` set to
   that user.
6. Runs `omj-agent enroll --user <user>` with the token, which writes
   `/etc/ohmyjob/agent.conf` (0640) and `/etc/ohmyjob/agent.credential` (0600).
7. Runs `systemctl daemon-reload` and `systemctl enable --now omj-agent`, then prints
   `omj-agent doctor`.

Running as root (`--user root`) is allowed but warned about: every Job then runs with
full privileges, and the Server marks the machine as privileged.

## Manual installation

Without the script: unpack the release archive, copy `omj-agent` to `/usr/local/bin`,
create the user and directories as above, copy `systemd/omj-agent.service` to
`/etc/systemd/system/` (edit `User=` and `Group=` if you use another account), then:

```sh
sudo omj-agent enroll --server https://jobs.example --token omj_enroll_...
sudo systemctl enable --now omj-agent
omj-agent doctor
```

## Hardening

The unit ships with a commented hardening block. Each directive breaks something ordinary
Jobs may rely on, which is why none is on by default:

- `NoNewPrivileges=true`: `sudo` inside Jobs stops working.
- `PrivateTmp=true`: Jobs cannot share `/tmp` with other services.
- `ProtectSystem=full`: `/usr`, `/boot` and `/etc` become read-only, even for `sudo`
  children.
- `ProtectHome=read-only`: breaks installations that run as an existing user.

Uncomment what your Jobs can live with and run `systemctl daemon-reload`;
`omj-agent doctor` reports which directives are active.

## Testing the installer

`make test-install` packages the current checkout the way a release is packaged, stands
in for GitHub Releases with a local directory, and runs `test/install-test.sh` as root
inside Debian and Fedora containers: a wrong checksum must abort before anything is
installed, a fresh install must produce the files, users, modes and unit above, a second
run must change nothing, `--user` must substitute the unit, and `--uninstall` must keep
the configuration unless `--purge` is given.
