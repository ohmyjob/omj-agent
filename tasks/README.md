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

| #   | Task                                    | Area        |
| --- | --------------------------------------- | ----------- |
| 001 | Scaffold module and CI                  | Foundation  |
| 002 | Config and paths                        | Foundation  |
| 003 | Protocol types and fixtures             | Protocol    |
| 004 | HTTP client                             | Protocol    |
| 005 | System information                      | Foundation  |
| 006 | Enroll command                          | CLI         |
| 007 | State file                              | Reliability |
| 008 | Runner: process execution               | Execution   |
| 009 | Runner: timeout and cancellation        | Execution   |
| 010 | Output chunker and buffer               | Execution   |
| 011 | Agent loop and lease handling           | Core        |
| 012 | Run reporter                            | Core        |
| 013 | Shutdown and restart handling           | Reliability |
| 014 | run, status and doctor commands         | CLI         |
| 015 | systemd unit and installer              | Packaging   |
| 016 | Release pipeline                        | Packaging   |
| 017 | End-to-end harness and core scenarios   | QA          |
| 018 | End-to-end failure and offline scenarios| QA          |
| 019 | Documentation                           | Docs        |
