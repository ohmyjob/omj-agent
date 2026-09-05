# 003 · Execution user allowlist

Status: done
Repo: ohmyjob-agent
Depends on: —
PRD: §21, §33, §37 (the ownership rule)

## Goal

The machine's operator decides which users OMJ may run work as, and the
Server can only choose from that list.

## Scope

- `agent.conf` gains `run_as_allowed`, a list of local users. Empty or absent
  means the Agent's own service user and nothing else, which is today's
  behaviour.
- The Agent validates the list at startup: each user must exist, none may be
  root unless the operator also set the existing explicit root
  acknowledgement, and an invalid entry is a startup error rather than a
  silent drop.
- The list rides in the metadata block `enroll` and `work` already share, so
  it is set at enrollment and refreshed by the 25-second heartbeat. Not
  `ping`: that is a GET with no body, used only by `status` and `doctor`.
- The `ping` **response** echoes back the list the Server holds, so `doctor`
  can report drift — "the Server has deploy, www-data" — without a second
  write path.
- An allowlist is only meaningful on a privileged Agent: one running as
  `ohmyjob` can only ever be `ohmyjob`. Validation should say so rather than
  accept a list it can never honour.
- **The Server can never add to it.** No endpoint, no payload and no lease
  field writes this list; it moves in one direction only.
- `omj-agent doctor` reports the list and whether each user is usable.

## Upgrade order

`config.Parse` rejects unknown keys, so a binary older than this task fails
to start against a config that already has `run_as_allowed`. Upgrade the
Agent first, then add the key, and say so in `docs/configuration.md`.

## Files

- `internal/config/config.go`, `internal/agent/registry.go`,
  `internal/protocol/*`, `internal/doctor/checks.go`, `*_test.go`

## Acceptance criteria

- [x] With no configuration, behaviour is exactly as it is today.
- [x] A list containing an unknown user fails startup with a clear message.
- [x] The list reaches the Server on enroll and on work.
- [x] No code path lets a Server response modify the list.

## Tests

- Table-driven config validation; the reported payload against the fake
  Server.
