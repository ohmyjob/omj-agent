# Tasks — Oh My Job Agent

Source of truth is the PRD (`PRD.md`, one level above both repositories during planning). Every `§` reference in a task points there. The wire contract is `ohmyjob-server/docs/agent-protocol-v1.md` and its fixtures (Server task 013).

## How to use this folder

- One task per file, numbered in dependency order. Work top to bottom unless a task's `Depends on` line says otherwise.
- Each task is sized to one focused working session. Split rather than half-finish.
- Mark progress by editing the `Status:` line (`todo` → `in progress` → `done`).

## Definition of done (applies to every task)

- Follows PRD §16.2 and §16.8: standard library only, `context.Context` on every blocking call, errors wrapped with `%w`, `log/slog` for logging, one goroutine owns each Run, no global mutable state, OS-specific code only behind build tags in `internal/runner`, `internal/config` paths, `internal/sysinfo` and `packaging/`.
- `make test lint` passes: `gofmt`, `go vet`, `golangci-lint`, `go test -race ./...`.
- New behaviour has table-driven tests. Anything that talks to the Server is tested against an `httptest` fake first, then in the e2e harness (tasks 017–018).
- Credentials never appear in logs, error messages or test output.
- No third-party example application is referenced in code, comments, commits or docs.

## Task index

The v1 backlog (001–019) is done and was removed when this one was written;
it is in the git history, and the numbering restarted here. A task number in
a commit message from before that point means a v1 task, not one of these.

| #   | Task                        | Area      |
| --- | --------------------------- | --------- |
| 001 | Discover scheduled work     | Import    |
| 002 | Report a discovery          | Import    |
| 003 | Execution user allowlist    | Execution |
| 004 | Run as a permitted user     | Execution |
| 005 | Keep the reason a Run ended | Execution |

001 and 002 are read-only by design: the Agent reports what a Machine
already schedules and never edits it. 003 and 004 add the rule that the
operator decides which users may run work and the Server may only choose
from that list (PRD §21, §37).

Both pairs have a Server counterpart: 002 needs Server 010, and 004 needs
Server 012. 005 follows 004 and needs nothing from the Server — it keeps the
reason 004 introduced from being thrown away by a restart.
