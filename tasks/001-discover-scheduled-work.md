# 001 · Discover scheduled work

Status: todo
Repo: ohmyjob-agent
Depends on: —
PRD: §33 (import, read-only first), §16.2, §16.8

## Goal

Read what a Machine already schedules — crontabs and systemd timers — and
report it faithfully, changing nothing.

## Scope

- Read-only collection, run on demand rather than on a timer: the system
  crontab, `/etc/cron.d`, per-user crontabs readable by the Agent's user, and
  `systemctl list-timers` with the unit each timer triggers.
- Each entry carries source, raw text, the schedule as written, the command
  and the user it runs as. Parsing failures are reported verbatim and marked
  unparseable — never guessed at, never dropped.
- The Agent never writes, edits, comments out or removes anything it finds.
  There is no code path in this task that opens a crontab for writing.
- Entries whose command is OMJ's own Agent are marked, so an import cannot
  create a loop.
- Portability rules apply (§16.8): Linux specifics behind the existing build
  tags, no assumption that `systemctl` exists.

## Files

- `internal/discovery/crontab.go`, `internal/discovery/timers.go`,
  `internal/discovery/discovery.go`, `*_test.go`

## Acceptance criteria

- [ ] A machine with system, `cron.d` and user crontabs reports every entry
      with its source.
- [ ] A malformed line is reported unparseable with its raw text.
- [ ] No file on the machine is modified, and no write path exists.
- [ ] A machine with no systemd degrades to crontabs alone.

## Tests

- Table-driven parsing against fixture crontabs and `list-timers` output,
  including malformed input.
