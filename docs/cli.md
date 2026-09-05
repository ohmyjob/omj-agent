# Command line

One binary, `/usr/local/bin/omj-agent`, with five subcommands. `omj-agent help`
prints the same list:

```text
Usage: omj-agent <command> [options]

Commands:
  enroll   Enroll this machine with a server
  run      Run the agent in the foreground
  status   Show the configuration, machine id and server reachability
  version  Print the agent and protocol versions
  doctor   Check the installation and exit 1 on any problem
```

Under systemd you rarely type any of them except `status` and `doctor`: the
unit runs `omj-agent run`, and the installer runs `enroll` for you.

## `omj-agent enroll`

Registers this machine with a Server and writes `agent.conf` and
`agent.credential`. Needs a single-use token from **Machines → Add Machine**,
and needs to write to `/etc/ohmyjob`, so run it as root.

```sh
sudo omj-agent enroll --server https://jobs.home.example --token omj_enroll_xxxxx
```

| Flag              | Default                | Meaning                                                     |
| ----------------- | ---------------------- | ----------------------------------------------------------- |
| `--server URL`    | —                      | Server URL, as the Add Machine page shows it. Required.     |
| `--token TOKEN`   | —                      | Single-use enrollment token. Required.                      |
| `--name NAME`     | the hostname           | Friendly name for this machine in the UI.                   |
| `--user NAME`     | `ohmyjob` as root      | Owner of the files it writes; otherwise the current user.   |
| `--insecure-http` | off                    | Allow a plain `http://` Server URL.                         |
| `--force`         | off                    | Replace an existing enrollment.                             |

On success it prints the machine id, the files it wrote and what to do next:

```text
Enrolled as machine mch_7Kq2vX9nB4.
Wrote /etc/ohmyjob/agent.conf and /etc/ohmyjob/agent.credential, owned by ohmyjob.
Next: systemctl enable --now omj-agent
```

Tokens are single use and expire fifteen minutes after they are shown. If yours
has been used or has expired, generate another from Add Machine; there is no
way to reuse one.

`--force` is for re-enrolling a machine that is already enrolled, which is what
you want after removing it from the Server, or when moving it to a different
Server. It overwrites the credential, so the old identity stops working.

`--insecure-http` prints a warning before it does anything, and the choice
follows the machine: it is stored in `agent.conf`, shown on the Machine page and
reported by `doctor` for as long as it is set.

## `omj-agent run`

Runs the Agent in the foreground until it is signalled. This is what the systemd
unit executes; run it by hand only to watch the log or on a machine without
systemd.

| Flag                | Default            | Meaning                                    |
| ------------------- | ------------------ | ------------------------------------------ |
| `--log-level LEVEL` | `log_level` in `agent.conf` | `debug`, `info`, `warn` or `error` |
| `--log-format FMT`  | `text`             | `text` or `json`                           |

The log goes to standard output, because journald captures it there. To follow
the service instead:

```sh
journalctl -u omj-agent -f
```

`--log-format json` emits one JSON object per line, for a log shipper. To turn
it on permanently, override the unit's `ExecStart`:

```sh
sudo systemctl edit omj-agent
```

```ini
[Service]
ExecStart=
ExecStart=/usr/local/bin/omj-agent run --log-format json
```

### Stopping

The first `SIGTERM` or `SIGINT` is a graceful stop: the Agent takes no new
work, waits for the Runs in flight and reports each one before exiting. A
second signal stops it immediately and exits 3, leaving those Runs to be
reported `lost` when it next starts. `systemctl stop` sends the first signal
and waits 30 seconds (`TimeoutStopSec`) before sending the second.

## `omj-agent status`

A report, never a failure: it always exits 0, so it is safe in a script that
only wants to see the state. On an enrolled machine:

```text
Configuration   /etc/ohmyjob/agent.conf
Server URL      https://jobs.home.example
Machine         mch_7Kq2vX9nB4
User            ohmyjob (uid 987)
Limits          4 concurrent runs, timeout up to 259200 s, output up to 104857600 bytes
Server          PASS https://jobs.home.example is reachable, server version 0.1.0 over TLS; server time 2026-09-05T19:20:45Z; clock skew +0.1s
Active runs     1
  run_4Btq8Rw2Lm  pid 31544  started 2026-09-05T19:19:02Z
Service         active
```

Before enrollment it says so rather than failing:

```text
Configuration   /etc/ohmyjob/agent.conf
Server URL      not enrolled; run omj-agent enroll
Machine         not enrolled
User            ohmyjob (uid 987)
Limits          unknown
Server          not checked; fix the configuration first
Active runs     none
Service         omj-agent is not installed
```

The server line makes one request and gives up after 15 seconds, so `status`
cannot hang on a Server that never answers.

## `omj-agent doctor`

The same information as a list of checks, and the one command that fails. It
exits 1 if any check is `FAIL`, which makes it usable in a health check or after
a configuration change. `WARN` is information, not failure, and does not affect
the exit code.

```text
PASS  configuration    /etc/ohmyjob/agent.conf is valid
PASS  credential       /etc/ohmyjob/agent.credential has mode 0600 and the right owner
PASS  state directory  /var/lib/ohmyjob is writable
PASS  server           https://jobs.home.example is reachable, server version 0.1.0 over TLS
PASS  protocol         the server accepts protocol 1
PASS  clock            within 0.1s of the server
PASS  service user     the service runs as ohmyjob, like doctor
PASS  service          omj-agent is enabled and active
PASS  hardening        no optional hardening directives are active
PASS  privileges       running as ohmyjob (uid 987)
```

| Check             | Fails when                                                        |
| ----------------- | ----------------------------------------------------------------- |
| `configuration`   | `agent.conf` is missing or invalid                                |
| `credential`      | the credential is missing, not `0600`, or owned by another user   |
| `state directory` | `/var/lib/ohmyjob` is missing or not writable by the Agent's user |
| `server`          | the Server is unreachable, its TLS fails, or it rejects the credential |
| `protocol`        | the Server does not accept this Agent's protocol version          |
| `clock`           | the two clocks disagree by more than 30 seconds                   |
| `service`         | the unit is not both enabled and active                           |

Three checks only ever warn. `service user` warns when the service runs as a
different user from the one running `doctor`, because the file checks above it
then describe the wrong account — run `sudo -u ohmyjob omj-agent doctor` to
check the right one. `hardening` lists the directives that are active and warns
that `NoNewPrivileges` breaks `sudo` inside Jobs. `privileges` warns when the
Agent runs as root.

Every failure names its own fix, including the exact `chmod`, `chown` or
`systemctl` to run.

## `omj-agent version`

```text
omj-agent 0.1.0 (a1b2c3d, 2026-09-05T19:19:44Z) protocol 1
```

The version, the commit it was built from, the build time and the protocol
version it speaks. The Server compares the first against its minimum and
recommended versions, and the last against what it supports.

## Exit codes

`run`, `status`, `doctor` and `version` use three codes:

| Code | Meaning                                                     |
| ---- | ----------------------------------------------------------- |
| `0`  | Success. `status` always exits 0.                           |
| `1`  | Failure. For `doctor`, at least one check failed.           |
| `2`  | Usage error: an unknown command, a bad flag, a stray argument. |

`run` adds one:

| Code | Meaning                                                            |
| ---- | ------------------------------------------------------------------ |
| `3`  | Stopped by a second signal before every Run was reported.           |

`enroll` reports why it failed, so the installer can print a useful hint
instead of a stack of output:

| Code | Meaning                                    | What to do                                                |
| ---- | ------------------------------------------ | --------------------------------------------------------- |
| `2`  | Missing or invalid `--server` or `--token` | Check the flags against the Add Machine page              |
| `3`  | Already enrolled                           | Remove the machine on the Server, or use `--force`        |
| `4`  | Token invalid                              | Generate a new token                                      |
| `5`  | Token expired                              | Generate a new token; they last fifteen minutes           |
| `6`  | Operating system not supported             | The Server does not accept this platform                  |
| `7`  | Agent version rejected                     | Install the version the Server requires                   |
| `8`  | Throttled                                  | Wait a minute and try again                               |
| `9`  | Server unreachable                         | Check the URL, the network and the TLS certificate        |
| `10` | Permission denied                          | Run as root, or fix ownership of `/etc/ohmyjob`           |
| `1`  | Anything else                              | Read the message                                          |
