# 002 · Report a discovery

Status: todo
Repo: ohmyjob-agent
Depends on: 001, Server 010
PRD: §14.2, §14.3 (additive), §16.6

## Goal

Send what was found to the Server, on request, over the protocol that
already exists.

## Scope

- `discovery_requested` in the work response is the trigger. The Agent
  collects one and posts it to `POST /discovery`, without holding up the loop
  or the Runs it owns, and answers each request once.
- A Server that does not implement discovery answers `404`; log it once and
  stop, rather than retrying a path that will never exist.
- Additive to protocol 1: an older Server never asks, so an upgraded Agent
  keeps working unchanged (§14.3).
- Bounded like every other payload: stop at 500 entries or 512 KiB encoded,
  whichever comes first, and count the rest in `omitted_entries`.
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
