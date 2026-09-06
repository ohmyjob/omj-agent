# 005 · Keep the reason a Run ended

Status: todo
Repo: ohmyjob-agent
Depends on: 004
PRD: §16.6, §21

## Goal

A Run's outcome survives an Agent restart intact, so the answer the Server
finally records is the one the Agent actually reached.

## Scope

- `state.Outcome` carries the finish reason alongside the status, exit code
  and start time, and `MarkFinished` persists it.
- `Resend` rebuilds the finish request from what was stored rather than
  inferring. Today it sets `agent_restarted` when the stored status is
  `lost` and otherwise sends no reason at all, so every other reason is lost
  on the way through disk.
- The `lost` inference stays correct where it is the truth: a Run the Agent
  found active at startup and gave up on is `agent_restarted`, and
  `startup.go` should record that reason when it writes the outcome instead
  of leaving `Resend` to guess it later.
- Existing state files are missing the field. A file written by an older
  Agent must still load, with no reason, rather than failing to parse — the
  state file is read on every start and an unparseable one costs the Agent
  every Run it was tracking.

## Why this matters more than it used to

`Resend` exists because the Server repeats a lease whose answer never
arrived, and it is the only path by which a finished Run reaches a Server
that missed the first attempt. Until task 004 the only reason travelling
that path was `spawn_failed`, and a Run coming back as a bare `failed`
instead lost little.

Task 004 added `run_as_not_permitted`, which is not a failure report. The
Server validates `run_as` when a Job is saved and again at hand-off against
the list this Agent reported, so a lease this Agent still refuses means the
Server's copy of the allowlist and the Machine's actual one have parted
ways. That is the single event which says so. Degrading it to `failed`
throws away the diagnosis and leaves an operator reading "it failed" about a
Run that was never allowed to start.

The window is real rather than theoretical: refuse a lease, restart the
Agent before the Server acknowledges the finish, and the Server records the
wrong thing.

## Design

`state.Outcome` and `state.RecentRun` are the pair to change together —
`MarkFinished` writes one and `RecentOutcome` returns the other, and
`Resend` reads the second. Keep the reason a `*protocol.FinishReason` rather
than a bare string so an absent reason and an empty one cannot be confused
on the way back out.

`internal/agent/reporter.go` builds the outcome at the end of `deliver()`
from the request it just sent. It already holds `request.Reason`, so
recording it is the same line doing one more thing rather than new
plumbing.

Do not widen this into a general "resend anything" mechanism. The reason is
one nullable field on a struct that already exists; the rest of the outcome
is already correct.

## Files

- `internal/state/state.go`, `internal/agent/reporter.go`,
  `internal/agent/startup.go`, `*_test.go`

## Acceptance criteria

- [ ] A Run refused for its execution user, resent after a restart, still
      reaches the Server as `run_as_not_permitted`.
- [ ] A Run whose command could not start still resends as `spawn_failed`.
- [ ] A Run the Agent gave up on at startup still resends as
      `agent_restarted`.
- [ ] A state file written before this change loads without error.

## Tests

- Unit: the state round trip, including a fixture of the older file shape.
- Against the `httptest` fake Server: finish a Run, drop the response,
  restart, and assert the reason the Server receives the second time.
