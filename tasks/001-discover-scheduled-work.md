# 001 · Discover scheduled work

Status: done
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

- [x] A machine with system, `cron.d` and user crontabs reports every entry
      with its source.
- [x] A malformed line is reported unparseable with its raw text.
- [x] No file on the machine is modified, and no write path exists.
- [x] A machine with no systemd degrades to crontabs alone.

## Tests

- Table-driven parsing against fixture crontabs and `list-timers` output,
  including malformed input.

## Outcome (2026-09-06)

- `internal/discovery` reads `/etc/crontab`, `/etc/cron.d`, the user crontabs
  under `/var/spool/cron/crontabs` and `/var/spool/cron`, and systemd timers.
  Sources read `crontab:/etc/crontab`, `crontab:/etc/cron.d/certbot`,
  `crontab:root` and `systemd:backup.timer`. A source that is there but cannot
  be read is named with its reason; one that is simply absent is not a failure
  and only reaches the debug log.
- Read-only is enforced, not just intended. The package may import ten standard
  packages and may call exactly `os.ReadFile`, `os.ReadDir`, `os.Executable`,
  `os.ErrNotExist`, `exec.LookPath`, `exec.CommandContext` and
  `exec.ExitError`; `TestPackageOnlyReads` parses the package's own source, on
  every platform's files at once, and fails on anything else. systemd is
  reached through an interface whose only methods are `ListTimers` and `Show`,
  so no caller can ask it to start a unit. `TestCollectChangesNothing` hashes
  the fixture tree before and after a discovery.
- Parsing decisions the task left open: the schedule is the fields as written
  joined by single spaces, so tabs and spaces read alike, while `Raw` keeps the
  line exactly; a field is range-checked, and month and day names are accepted,
  so `70 * * * *` is unparseable rather than silently kept; the sixth field of
  a system crontab must look like a user name, so a `cron.d` line that forgot
  it is unparseable instead of running a command as a user called
  `/usr/bin/foo`; `CRON_TZ` and `TZ` apply to the lines below them; a command
  stops at the first unescaped `%`, which is where cron's standard input
  begins, and `\%` becomes the literal character cron passes on; a leading `-`
  is Debian's "do not log" and not part of the schedule; files whose names cron
  itself ignores (`certbot.dpkg-dist`) are not reported.
- For timers, `Raw` is the `systemctl list-timers` row and the schedule comes
  from `systemctl show`: `TimersCalendar` gives the expression as written and
  its timezone when it carries one, `TimersMonotonic` keeps its anchor
  (`OnBootUSec=15min`) because half the schedule is which event it counts from.
  A timer with two schedules becomes two entries sharing a unit and command. A
  service with several `ExecStart` lines is reported joined in order with a
  note saying so, and one whose command systemd will not name is unparseable,
  because a schedule without a command cannot be imported. A unit with no
  `User=` is reported as `root`, which is who the system manager runs it as.
- Bounds: 500 entries or 512 KiB, whichever comes first, counted in
  `OmittedEntries`. Once one entry is left out every later one is too, so a
  discovery is the beginning of what a Machine schedules rather than whichever
  entries happened to be small. A single entry too large for any discovery is
  the exception: it is counted and skipped. Byte cost is the entry's own text
  plus 128 bytes for the JSON framing task 002 adds around it.
- Not in scope here and worth naming for 002: nothing sends a discovery,
  `omj-agent discover` does not exist, and no protocol type was added. The
  `/etc/cron.hourly` style run-parts directories are not scanned, because the
  `/etc/crontab` lines that run them are reported instead, and `systemctl
  --user` timers are not read, because they only exist inside a user's session.
