# 004 · Run as a permitted user

Status: done
Repo: ohmyjob-agent
Depends on: 003, Server 012
PRD: §21, §16.3, §16.5

## Goal

Execute a Run as the user the lease names, provided the operator already
allowed it, without the Agent needing to be root for its own sake.

## Scope

- The lease may carry `run_as`. The Agent refuses the Run — with a reason the
  Server records — when the user is absent from the local allowlist, so a
  Server that lies is simply wrong rather than dangerous.
- Execution drops to that user's uid/gid and supplementary groups before
  `exec`, and the per-Run environment (§16.5) is rebuilt for that user:
  `HOME`, `USER`, `SHELL` and `PATH` from the target, never inherited from
  the daemon.
- Output files and the working directory are checked against that user, so a
  Run cannot write where the target user cannot.
- Process-group handling, timeouts and cancellation behave identically to a
  service-user Run, including for the child's children.
- The privilege change is Unix-only and stays behind the existing build tags.

## Files

- `internal/runner/runner.go`, `internal/runner/credential_unix.go`,
  `internal/agent/lease.go`, `*_test.go`

## Acceptance criteria

- [ ] A lease naming an allowed user runs as that user, proven by `id` in the
      output. Proved for the case where that user is the one the Agent already
      runs as (`TestStartAsTheAgentsOwnUser`). Dropping to a *different* user
      needs root, so the e2e harness owes the rest.
- [x] A lease naming a user not on the list is refused with a recorded reason
      and nothing executes.
- [ ] Cancellation and timeout kill the whole process group as before. The
      process-group code is untouched and its tests still pass, but the
      dropped-privilege case needs root and belongs to the e2e harness.
- [x] The environment belongs to the target user, not the daemon.

## Tests

- Unit with a fixture user where the CI environment allows it; otherwise the
  e2e harness, which already runs as root in containers.

## What landed

The refusal is a spawn failure: the Agent accepts the lease, refuses before
`exec`, and the existing path reports `failed` with reason `spawn_failed` and
the refusal sentence as the Run's only output. No new `FinishReason` was
invented, because the Server's enum is closed and Server 012 does not add one.

`SHELL` is now in every Run's environment, set to the shell the command
actually runs under. §16.5's table does not list it and `os/user` does not
answer a login shell; reading `/etc/passwd` behind `getpwnam`'s back would be
wrong for any machine using a directory service.

`run_as` was added to the work-response fixture here. Server 012 owes the same
field in `docs/agent-protocol-v1.md` and
`docs/fixtures/agent-protocol-v1/work-response.json`, or `make sync-fixtures`
will revert it and `TestFixturesRoundTrip` will fail.
