# Configuration

The Agent reads one file, `/etc/ohmyjob/agent.conf`. `omj-agent enroll` writes
it, and you edit it afterwards only to change the defaults below. There is no
configuration for what to run or when: Jobs and schedules live on the Server.

## Files

| Path                          | Mode   | Written by | Holds                                       |
| ----------------------------- | ------ | ---------- | ------------------------------------------- |
| `/etc/ohmyjob/`               | `0750` | installer  | the configuration directory                 |
| `/etc/ohmyjob/agent.conf`     | `0640` | `enroll`   | the settings on this page                   |
| `/etc/ohmyjob/agent.credential` | `0600` | `enroll`   | the credential for this machine             |
| `/var/lib/ohmyjob/`           | `0700` | installer  | the state directory                         |
| `/var/lib/ohmyjob/state.json` | `0600` | the daemon | Runs in flight and their recent outcomes    |

All four are owned by the user the Agent runs as, `ohmyjob` by default. The
Agent refuses to start if `agent.credential` is not exactly `0600` or is owned
by another user, and says which `chmod` or `chown` fixes it.

`state.json` is how a restarted Agent knows what it was doing: it holds the
process id and start time of every Run in flight, and the outcome of recent
ones so a Run is never reported twice. Deleting it loses that memory, and any
Run that was in flight is reported `lost` on the next start. It is not a
configuration file; do not edit it.

## Format

Plain `key = value` lines. Blank lines and lines beginning with `#` are
ignored, values may be wrapped in double quotes, and a repeated key is an
error rather than a last-one-wins surprise. Unknown keys are rejected with the
line number, so a typo stops the Agent instead of being silently ignored.

A file as `enroll` writes it:

```ini
server_url = https://jobs.home.example
machine_id = mch_7Kq2vX9nB4
insecure_http = false
log_level = info
max_concurrent_runs = 4
max_timeout_seconds = 259200
max_output_bytes = 104857600
```

## Keys

| Key                   | Default     | Meaning                                              |
| --------------------- | ----------- | ---------------------------------------------------- |
| `server_url`          | —           | Base URL of the Server. Required.                    |
| `machine_id`          | —           | Identity the Server issued at enrollment.            |
| `insecure_http`       | `false`     | Allow a plain `http://` `server_url`.                |
| `log_level`           | `info`      | `debug`, `info`, `warn` or `error`.                  |
| `max_concurrent_runs` | `4`         | Runs this machine executes at once, 1 to 64.         |
| `max_timeout_seconds` | `259200`    | Local ceiling on a Job's timeout. Minimum 1.         |
| `max_output_bytes`    | `104857600` | Local ceiling on output kept per Run. Minimum 1.     |

### `server_url`

No trailing path; the Agent appends `/api/agent/v1` itself. It must use
`https`, and the Agent refuses to start on anything else. A plain `http://`
URL needs `insecure_http = true` as well, which is a deliberate second step
because the credential and the output of every Job then travel unencrypted.
The Server shows that choice on the Machine page, and `doctor` reports it as a
warning for as long as it is set.

Changing this after enrollment does not move the machine to a different
Server: the credential belongs to the Server that issued it. To move a
machine, enroll it again with a token from the new Server and
`omj-agent enroll --force`.

### `max_concurrent_runs`

How many Runs this machine executes at the same time. The Agent asks the
Server only for what it has room for, so a machine at its limit stops taking
work rather than queueing it locally. Raise it on a machine that mostly waits
on the network, lower it to one where Jobs contend for the same resource.

### `max_timeout_seconds` and `max_output_bytes` are local safeguards

Both limits are set per Job on the Server. These two keys are the machine's own
ceiling on what it will accept, and they exist because the Server is trusted to
dispatch commands but not to exhaust the machine running them.

Every lease is checked against them before anything starts, and each limit
becomes the smaller of what the Job asked for and what this machine allows. A
Job with a two-week timeout on a machine capped at three days is run with the
three-day limit, so a mistake on the Server cannot leave a process running for
a fortnight. Output is counted as it is produced and cut off at the effective
limit; the Run still finishes normally and is reported with its output marked
truncated, which the Run page shows.

The Agent tells the Server the effective values when it starts a Run, so the
Run page shows the limits the Run was actually held to rather than the ones the
Job asked for. A lease is refused outright only when it is malformed: no
command, a non-positive timeout or output limit, an expired lease, or a lease
addressed to another machine.

Lower them on a machine that matters. The defaults — three days and 100 MiB —
are generous on purpose, so that they never surprise anyone who has not thought
about them; they are not a recommendation.

### `log_level`

`info` reports Runs starting and finishing and anything that went wrong.
`debug` adds every poll and every request, which is loud on an idle machine but
what you want when the Agent is not taking work. The level applies to the
daemon's own log only, never to Job output, which goes to the Server.

`omj-agent run --log-level` overrides this for one invocation without editing
the file.

## Path overrides

The Agent reads two environment variables, both intended for development and
for the end-to-end suite rather than for installations:

| Variable          | Default            | Effect                                    |
| ----------------- | ------------------ | ----------------------------------------- |
| `OMJ_CONFIG_DIR`  | `/etc/ohmyjob`     | Directory holding `agent.conf` and the credential |
| `OMJ_STATE_DIR`   | `/var/lib/ohmyjob` | Directory holding `state.json`            |

Nothing else is configurable through the environment. In particular, no
setting in this file can be overridden by an environment variable, and the
credential cannot be supplied through one: it is read from its file, so that
it cannot leak into a process listing or a unit file.

## Applying a change

The daemon reads `agent.conf` once at start, so a change needs a restart:

```sh
sudoedit /etc/ohmyjob/agent.conf
sudo systemctl restart omj-agent
omj-agent doctor
```

`restart` waits up to 30 seconds for Runs in flight to be reported before the
process exits. A Run still executing when that elapses is reported `lost` with
`agent_restarted` and is not run again for that occurrence.
