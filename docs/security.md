# Security

The Agent runs commands that come from somewhere else. This document says what
that means on the machine it is installed on, and what you can do about it. The
Server's side of the same model is in
[the Server's security document](https://github.com/ohmyjob/omj-server/blob/main/docs/security.md).

## The trust model

> Oh My Job Server is trusted to define and dispatch arbitrary
> non-interactive commands. Oh My Job Agent executes those commands only with
> the privileges granted to its locally configured operating-system user. The
> Server must not be able to remotely increase those privileges.

The Agent is the second half of that sentence, and it is the half that holds
the boundary. It accepts commands from the Server and runs them, and it has no
mechanism for running them as anybody but its own user. There is no lease
field, no configuration key and no protocol message that changes the identity a
Run executes under. That absence is the security property.

So the question worth asking about a machine is not whether the Agent is
secure, but what the `ohmyjob` user can reach on it. Keep that small.

## What the service user can and cannot do

The installer creates a system account called `ohmyjob`: a non-login shell,
home `/var/lib/ohmyjob`, no password, and membership of no group but its own.

It can read anything world-readable, write to `/var/lib/ohmyjob` and `/tmp`,
make outbound network connections, and run any binary on the machine as
itself. That is enough to be useful and enough to matter: a compromised Server
can read every world-readable file on the machine and reach anything on the
network the machine can reach.

It cannot read files restricted to other users, write outside its own
directories, or gain privileges — unless you grant them with `sudo`, which is
the next section.

Two things the Agent holds are worth naming. `agent.credential` is `0600` and
owned by the service user, so a Job running as that same user *can* read it;
the credential authenticates this machine to the Server and nothing more. And
the Agent refuses to start when that file's mode or owner is wrong, rather than
carrying on with a credential the rest of the system can read.

## The clean per-Run environment

A Run does not inherit the daemon's environment. The Agent builds one from
scratch for every Run:

| Variable         | Value                                                        |
| ---------------- | ------------------------------------------------------------ |
| `HOME`           | the service user's home directory                            |
| `USER`, `LOGNAME`| the service user's name                                      |
| `PATH`           | `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin` |
| `LANG`           | `C.UTF-8`                                                    |
| `OMJ_RUN_ID`     | the Run's id                                                 |
| `OMJ_JOB_NAME`   | the Job's name                                               |
| `OMJ_MACHINE_ID` | this machine's id                                            |

The Job's own variables are added last and may override any of these. Nothing
else is passed through, so a Job cannot read the Agent's own environment, and
a variable exported into the systemd unit does not silently become available to
every Job.

Two consequences worth knowing. `PATH` is fixed, so a command that works in
your interactive shell because of something in `.profile` will not work in a
Job; use absolute paths. And a Job that needs a login environment has to ask
for one explicitly, with a shell that reads the profile.

## Privileged Jobs

Some work genuinely needs privileges. Grant them locally and narrowly with
`sudo`, never by running the Agent as root.

To let Jobs renew a certificate and reload Nginx, and nothing else, create
`/etc/sudoers.d/ohmyjob-certs` with `visudo -f`:

```sudoers
ohmyjob ALL=(root) NOPASSWD: /usr/bin/certbot renew --quiet
ohmyjob ALL=(root) NOPASSWD: /usr/bin/systemctl reload nginx
```

The Job's command is then:

```sh
sudo /usr/bin/certbot renew --quiet && sudo /usr/bin/systemctl reload nginx
```

Three things matter in those rules. Each command is an absolute path, so `PATH`
cannot be used to substitute a different binary. Each names one action rather
than `systemctl` in general, which would allow restarting anything, including
the Agent itself. And neither takes an argument from the Job, so the Server
cannot choose the target — a rule ending in `systemctl reload` would let a
compromised Server reload, and therefore reconfigure the reach of, any service
on the machine.

Grant one rule per task, and audit what you have granted:

```sh
sudo -l -U ohmyjob
```

Oh My Job never manages these rules and cannot see them. They are the only
thing that widens what a Run can do, so they are worth reviewing on the same
schedule as the Jobs themselves.

## The hardening block

The unit at `/etc/systemd/system/omj-agent.service` ships with four directives
commented out. They are opt-in because each one breaks something an ordinary
Job might rely on:

| Directive               | Breaks                                                     |
| ----------------------- | ---------------------------------------------------------- |
| `NoNewPrivileges=true`  | `sudo` inside Jobs stops working entirely                  |
| `PrivateTmp=true`       | Jobs cannot share `/tmp` with other services               |
| `ProtectSystem=full`    | `/usr`, `/boot` and `/etc` are read-only, even for `sudo` children |
| `ProtectHome=read-only` | installations running as an existing user with a real home |

Uncomment what your Jobs can live with, then:

```sh
sudo systemctl daemon-reload && sudo systemctl restart omj-agent
omj-agent doctor
```

`doctor` reports which directives are active, and warns specifically when
`NoNewPrivileges` is on, because the failure it causes — every `sudo` in every
Job — otherwise looks like a broken command rather than a policy.

`NoNewPrivileges` and the privileged-Jobs section above are mutually exclusive.
Pick one: either this machine's Jobs never need privileges and the directive
costs nothing, or they do and it must stay off.

## Running as another user

`install.sh --user NAME` runs the Agent as an account that already exists, and
sets `User=` and `Group=` in the unit accordingly. The account must exist
first; the installer creates only `ohmyjob`.

This is the right move when Jobs need to act as a service account that already
owns the files they touch — a deploy user, a database account — because it
avoids the `sudo` rules entirely. It also means every Job on the machine
inherits that account's reach, so choose one whose reach you would be content
to hand to the Server.

The Agent, `status` and `doctor` all read files as whoever runs them, so run
`doctor` as the service user to check the right account:

```sh
sudo -u ohmyjob omj-agent doctor
```

`doctor` warns when it notices that mismatch rather than reporting the wrong
account's permissions as though they were the service's.

## Running as root, and why the UI warns

`install.sh --user root` is allowed and warns at install time. The Machine page
marks it, and every Job and Run on that machine is flagged in amber.

The warning is not a formality. Running as root removes the boundary the whole
model rests on: a compromised Server then has root on that machine immediately
and without any further step. Every argument for it — a Job that needs to write
outside its home, a package upgrade, a service restart — is better served by
one `sudo` rule that grants exactly that and nothing else.

If a machine must run as root, treat the Server as a root-equivalent system:
put it on a private network, turn on two-factor authentication, and keep its
administrator accounts to the few people who already have root there anyway.

## What the Agent does to protect itself

- It connects outbound only. Nothing listens on the machine, and no port needs
  opening for Oh My Job.
- It refuses a plain `http://` Server URL unless enrolled with
  `--insecure-http`, and reports that choice for as long as it is set.
- It verifies the Server's TLS certificate. A Server using a private
  certificate authority needs that authority installed on the machine; the
  Agent will not skip verification.
- The credential is read from a `0600` file, never from a flag or an
  environment variable, so it cannot appear in a process listing or a unit
  file. It is redacted in logs and errors, and prints as `omj_agent_…` if
  something formats it by accident.
- Every lease is verified before anything is spawned: that it is addressed to
  this machine, that this machine is not already running it, that it has not
  expired, that it has a command, and that its timeout and output limits are
  positive. Both limits are then reduced to the machine's own ceilings from
  `agent.conf`.
- Each Run gets its own process group, and a timeout or a cancellation kills
  the group, so a command that spawns children cannot leave them behind.
- One goroutine owns each Run, and `state.json` records what was in flight, so
  a restart reports the interrupted Run as `lost` rather than running it twice.

## Reporting a vulnerability

Email `security@ohmyjob.sh`. Please do not open a public issue. See
[SECURITY.md](../SECURITY.md) for what to include and what to expect.
