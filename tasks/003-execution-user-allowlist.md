# 003 · Execution user allowlist

Status: todo
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
- The list is reported to the Server on enroll and on every ping, so the
  Server's picker cannot offer a user the machine does not permit.
- **The Server can never add to it.** No endpoint, no payload and no lease
  field writes this list; it moves in one direction only.
- `omj-agent doctor` reports the list and whether each user is usable.

## Files

- `internal/config/config.go`, `internal/agent/registry.go`,
  `internal/protocol/*`, `internal/doctor/checks.go`, `*_test.go`

## Acceptance criteria

- [ ] With no configuration, behaviour is exactly as it is today.
- [ ] A list containing an unknown user fails startup with a clear message.
- [ ] The list reaches the Server on enroll and on ping.
- [ ] No code path lets a Server response modify the list.

## Tests

- Table-driven config validation; the reported payload against the fake
  Server.
