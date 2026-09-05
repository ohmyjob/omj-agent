# Troubleshooting

Start with the two commands that answer most questions:

```sh
omj-agent doctor            # every check, with the fix for each failure
journalctl -u omj-agent -n 100 --no-pager
```

Run `doctor` as the account the service runs as, or it reports on the wrong
one — it warns when it notices the mismatch:

```sh
sudo -u ohmyjob omj-agent doctor
```

## The credential was rejected

`doctor` says:

```text
FAIL  server           the server rejected the credential; run omj-agent enroll --force with a new token
```

The Server does not recognise this machine's credential. In practice that means
one of three things.

The machine was removed on the Server. Its credential died with it, and the
Agent will retry forever without ever succeeding. Add the machine again and
enroll with the new token.

The machine was enrolled twice. The second enrollment issued a new credential
and invalidated the first; if two hosts somehow share one `agent.conf`, only
the one that enrolled last works. Give each machine its own enrollment.

`agent.credential` was restored from a backup or copied from another machine.
Credentials are per machine and cannot be shared or moved.

The fix is the same in each case — a fresh token from **Machines → Add
Machine**:

```sh
sudo omj-agent enroll --server https://jobs.home.example --token omj_enroll_xxxxx --force
```

A restart is not required: the Agent keeps retrying a rejected credential every
five minutes, so it picks up the new one on its own within that window.
`sudo systemctl restart omj-agent` makes it immediate.

A related failure is a file-permission problem rather than a rejection:

```text
FAIL  credential       /etc/ohmyjob/agent.credential has mode 0644; it must be 0600 so only the agent can read it
```

The Agent refuses to start rather than use a credential others can read. The
message names the `chmod` and `chown` that fix it.

## The protocol was rejected

`doctor` says:

```text
FAIL  protocol         the server rejected this agent: unsupported protocol version (this agent speaks protocol 2, the server supports 1); the server requires agent 0.4.0 or newer; update the agent
```

The Agent and the Server no longer speak the same protocol, and the Machine
page shows the machine as Incompatible. No leases are issued, so no Jobs run on
it, and nothing is silently dropped: the occurrences are simply not dispatched.

The message names both versions and, when the Server sent one, the minimum
Agent version it wants. Install that version:

```sh
curl -fsSL https://ohmyjob.com/install.sh | sudo sh -s -- --no-enroll --version 0.4.0
sudo systemctl restart omj-agent
omj-agent doctor
```

`--no-enroll` is right here: the machine is already enrolled and keeps its
identity, so only the binary changes.

If the Agent is newer than the Server, upgrade the Server instead. A protocol
change is a major release on both sides, and the release notes say which
versions work together.

## The machine shows as offline

The Server marks a machine offline when it has not heard from it recently.
Work down the chain.

Is the service running?

```sh
systemctl status omj-agent
```

If it is not, `journalctl -u omj-agent -n 50 --no-pager` says why. A
configuration error stops it at start with the file and line, and it will not
restart into a working state until the file is fixed.

Can it reach the Server?

```text
FAIL  server           could not reach https://jobs.home.example: dial tcp: i/o timeout
```

The Agent connects outbound only, so nothing needs opening on this machine —
this is a route, a firewall or a DNS problem between here and the Server. Check
it directly:

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://jobs.home.example/api/agent/v1/ping
```

`401` is a healthy answer to an unauthenticated request: the Server is
reachable and the problem is elsewhere. A timeout is a network problem.

Is it a TLS problem?

```text
FAIL  server           could not verify the TLS certificate of https://jobs.home.example: x509: certificate signed by unknown authority; if the server uses its own certificate authority, install it on this machine
```

The Agent will not skip verification, and there is no flag to make it. Install
the certificate authority in the machine's own trust store — on Debian, drop
the CA certificate into `/usr/local/share/ca-certificates/` and run
`update-ca-certificates`; on Fedora, `/etc/pki/ca-trust/source/anchors/` and
`update-ca-trust`. Then restart the Agent.

If every check passes and the machine still shows offline, the Server is not
seeing what the Agent is sending. Check the clock, below, and the Server's own
log.

## A run was marked lost

`lost` means the Server stopped hearing about a Run that had started. The Run
page names the reason.

`agent_restarted` means the Agent restarted while the Run was executing. On
start it reads `state.json`, finds the Run that was in flight, and reports it
`lost` rather than pretending it succeeded or running it again. That is
deliberate: the Agent cannot know whether a half-finished command is safe to
repeat, so it never decides that for you. The occurrence is not retried; the
next scheduled occurrence runs normally.

This is expected after `systemctl restart` during a Run, after a package
upgrade, and after the machine reboots. If it happens without any of those,
the Agent is being killed — look for the OOM killer in `journalctl -k`, and for
`Restart=always` cycling in `systemctl status omj-agent`.

A Run can also be marked lost by the Server while the Agent is still working on
it, when heartbeats stop arriving for long enough. When the link comes back the
next heartbeat returns the Run to `running` and it finishes normally; the
Server never re-leases it, so it does not run twice.

A Run stopped by the Agent shutting down is reported `cancelled` with
`agent_stopped`, not `lost`. That happens when a second signal arrives before
the Run could finish: `systemctl stop` sends the first, waits 30 seconds
(`TimeoutStopSec`) for Runs in flight, then sends the second. A Job that
regularly outlives that window is cancelled this way on every restart — raise
`TimeoutStopSec` with `systemctl edit omj-agent` if its Runs need longer.

## Output was truncated

The Run page says the output was truncated. The Agent stopped recording at the
effective output limit and let the command finish; the Run's status and exit
code are real, only the log is incomplete.

The effective limit is the smaller of the Job's `max_output_bytes` on the
Server and `max_output_bytes` in this machine's `agent.conf`, which defaults to
100 MiB. `omj-agent status` prints the local ceiling:

```text
Limits          4 concurrent runs, timeout up to 259200 s, output up to 104857600 bytes
```

Raising it is rarely the answer. A Job producing that much output is usually
one that should write to a file and print a summary, or one that should be run
with the quiet flag its command almost certainly has. If a Job genuinely
produces that much and you need all of it, raise both limits — the Job's and
the machine's — since the smaller one wins.

## The clocks disagree

```text
FAIL  clock            this machine is 4m12s behind the server; enable time synchronisation (timedatectl set-ntp true)
```

More than 30 seconds of skew is a failure, because leases, heartbeats and
grace periods are all deadlines. A machine whose clock is behind can decide a
lease is still valid after the Server has expired it; one whose clock is ahead
can abandon work it still holds. Schedules are evaluated on the Server, so this
does not shift when a Job runs, but it does corrupt the bookkeeping around it.

```sh
sudo timedatectl set-ntp true
timedatectl status
omj-agent doctor
```

Containers and virtual machines drift after suspend or migration, so a machine
that was fine yesterday can fail this check today.

## A job runs by hand but not as a job

Almost always the environment. A Run does not inherit a login shell: `PATH` is
fixed at `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`,
`HOME` is the service user's, and nothing from your `.profile` or `.bashrc` is
read. See [the clean per-Run environment](security.md#the-clean-per-run-environment).

Use absolute paths, and check the command as the service user rather than as
yourself:

```sh
sudo -u ohmyjob env -i HOME=/var/lib/ohmyjob PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin sh -c 'your command here'
```

If it fails there and works for you, it is a permission or an environment
difference, not Oh My Job. If the command uses `sudo`, check that a rule exists
(`sudo -l -U ohmyjob`) and that `NoNewPrivileges` is not enabled — `doctor`
warns when it is, because it breaks every `sudo` in every Job.

## A job did not run while the machine was away

That is the missed-work policy doing its job, and the Run page says which
outcome applied.

A Run that started late says so, and names the machine being offline as the
reason: the occurrence fell inside the Job's grace period, and the Agent picked
it up when it reconnected. Several occurrences missed during one outage are
folded into a single late Run rather than a burst.

`missed` with `grace_period_elapsed` means the machine came back after the
grace period had passed, so the occurrence was given up on. `missed` with
`machine_offline` means the Job's policy is to skip rather than catch up, and
the occurrence was recorded as missed the moment it came due.

Change the behaviour on the Job: the missed policy and the grace period are
both Job settings on the Server, not Agent configuration.

## Nothing here matches

Turn up the log and watch a poll cycle:

```sh
sudo systemctl stop omj-agent
sudo -u ohmyjob omj-agent run --log-level debug
```

`debug` logs every poll and every request, which is what you want when the
Agent is running and healthy but taking no work. Stop it with Ctrl-C — the
first signal waits for anything in flight — and start the service again with
`sudo systemctl start omj-agent`.

Credentials never appear in the log at any level; if you see
`omj_agent_…` that is the redaction working, not a truncated secret.

Still stuck: open an issue with the output of `omj-agent doctor` and
`omj-agent version`. Neither prints anything sensitive. For anything that looks
like a vulnerability, email `security@ohmyjob.com` instead — see
[SECURITY.md](../SECURITY.md).
