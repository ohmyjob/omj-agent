# 008 · Runner: process execution

Status: done
Repo: ohmyjob-agent
Depends on: 002
PRD: §16.5 (environment, shell, working directory, exit codes), §16.8, §21

## Goal

Start a command exactly as specified, in a clean environment, in its own process group, and report its exit status faithfully.

## Scope

- `internal/runner.Spec{RunID, JobName, MachineID, Command, Shell, WorkingDir, Env map[string]string, Timeout, MaxOutput}` and `runner.Start(ctx, spec, sink) (*Process, error)`; `Process.Wait() Result{ExitCode, Signal, StartedAt, FinishedAt, TimedOut, Cancelled, Err}`; `Process.PID()`, `Process.PGID()`.
- Command line: `<shell> -c <command>`, shell defaults to `/bin/sh`. Working directory defaults to the service user's home (`os/user`), error if the directory does not exist (spawn failure).
- Environment built from scratch per §16.5: `HOME`, `USER`, `LOGNAME` (from the current user), `PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`, `LANG=C.UTF-8`, `OMJ_RUN_ID`, `OMJ_JOB_NAME`, `OMJ_MACHINE_ID`, then the Job's variables, which override. Nothing from `os.Environ()`.
- `process_unix.go` (`//go:build unix`): `SysProcAttr{Setpgid: true}`, `cmd.WaitDelay` set so `Wait` returns even if grandchildren hold the pipes.
- stdout and stderr each read by a goroutine into `sink.Write(stream, bytes)`; the sink interface is defined here and implemented by task 010. Reading never blocks the process (no deadlock on large output).
- Exit status: normal exit → code; signal death → `128 + signal` with `Signal` set; spawn failure → `Err` set and no `Result` timing beyond `StartedAt`.

## Files

- `internal/runner/runner.go`, `process_unix.go`, `env.go`, `runner_test.go`, `testdata/*.sh`

## Acceptance criteria

- [ ] `env` inside a Job prints exactly the expected variables and nothing from the daemon (test sets a canary in the test process environment and asserts its absence).
- [ ] 50 MiB on stdout and 50 MiB on stderr simultaneously complete without deadlock (`head -c` from `/dev/zero`).
- [ ] `exit 3` → 3; `kill -TERM $$` → 143; missing working directory → spawn failure with the path in the message.

## Tests

- Shell-script based table tests; run with `-race`.

## Outcome (2026-09-04)

- A spawn failure is `Start`'s error, because there is no process to wait on: a working directory that does not exist (the message names it and wraps `os.ErrNotExist`), a shell that cannot be executed, an empty command, or a context that is already done. `Result.Err` is reserved for failures observed while waiting; a non-zero exit is not an error.
- Output reaches the `Sink` through `exec`'s own copying goroutines, one per stream, behind small `io.Writer` adapters. The slice passed to `Write` is only valid during the call, and a `Write` that blocks blocks the child, so task 010's sink must copy and never block.
- `cmd.WaitDelay` is two seconds: once the shell has exited, a grandchild holding the pipes can delay `Wait` by at most that long, after which the pipes are closed and its later output is lost. `exec.ErrWaitDelay` is swallowed because the child's exit status is already known.
- `Timeout` and `MaxOutput` travel in the `Spec` but nothing enforces them here: the timeout, cancellation and `TimedOut`/`Cancelled` belong to task 009, the output cap to task 010. The context only gates the start; task 009 wires cancellation through the process group.
- `Result.Signal` is an `os.Signal` so callers outside `process_unix.go` never import `syscall`; death by signal reports `128 + signal`. `PGID()` comes from `Getpgid` right after the start, falling back to the PID that `Setpgid` made the group leader.
- The environment is emitted in sorted key order for determinism, and every default including `LANG` and `PATH` may be overridden by the Job. The shell itself adds `PWD`, `OLDPWD`, `SHLVL` or `_`, which is why the environment test tolerates those four and nothing else.
- Only `process_unix.go` imports `syscall`; there is no `!unix` stub because Windows is not a v1 target and the module still builds on macOS and Linux.

