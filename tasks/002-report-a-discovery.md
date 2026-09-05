# 002 · Report a discovery

Status: todo
Repo: ohmyjob-agent
Depends on: 001, Server 010
PRD: §14.2, §14.3 (additive), §16.6

## Goal

Send what was found to the Server, on request, over the protocol that
already exists.

## Scope

- The work response may ask for a discovery; the Agent collects one and
  posts it to the new endpoint. Nothing is scheduled by the Agent and nothing
  is cached beyond the request.
- Additive to protocol 1: an older Server never asks, so an upgraded Agent
  keeps working unchanged (§14.3).
- Bounded like every other payload: a machine with thousands of crontab
  lines truncates with a flag rather than posting without limit.
- The subcommand `omj-agent discover` prints what would be sent, so an
  operator can see exactly what leaves the machine before enabling anything.

## Files

- `internal/agent/discovery.go`, `internal/client/client.go`,
  `internal/protocol/*`, `internal/cli/discover.go`, `*_test.go`

## Acceptance criteria

- [ ] Asked for a discovery, the Agent posts one and continues its loop.
- [ ] A Server that never asks sees no change in behaviour.
- [ ] `omj-agent discover` prints the payload and sends nothing.
- [ ] An oversized discovery is truncated and says so.

## Tests

- Against the `httptest` fake Server, then the e2e harness.
