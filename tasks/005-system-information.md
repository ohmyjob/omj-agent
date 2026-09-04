# 005 · System information

Status: done
Repo: ohmyjob-agent
Depends on: 001
PRD: §14.2 (enroll and work metadata), §9.6, §16.8

## Goal

Collect the Machine metadata the Server displays, in an OS-neutral interface with Linux behind a build tag.

## Scope

- `internal/sysinfo.Collect() (Info, error)` returning `Hostname`, `OS` (`runtime.GOOS`), `OSVersion` (`PRETTY_NAME` from `/etc/os-release`, fallback `uname -sr` equivalent via `syscall.Uname`), `Arch` (`runtime.GOARCH`), `KernelVersion` (`uname` release), `ReportedIPs` (non-loopback unicast addresses of up interfaces, IPv4 first, max 16), `AgentUser` and `AgentUID` (from `os/user.Current()`).
- Files `sysinfo.go` (interface and shared code), `sysinfo_linux.go` (`//go:build linux`), with the os-release path injectable for tests.
- Conversion helper `Info.EnrollRequest(token, name, insecure)` and `Info.WorkMetadata()` to fill protocol structs.

## Files

- `internal/sysinfo/sysinfo.go`, `sysinfo_linux.go`, `sysinfo_test.go`, `testdata/os-release-*`

## Acceptance criteria

- [ ] Parses Debian, Ubuntu, Fedora, Alpine and Arch `os-release` samples.
- [ ] Never fails hard when a field is unavailable (empty string with a debug log).

## Tests

- Table-driven os-release parsing, IP filtering with fake interfaces, user lookup.

## Outcome (2026-09-04)

- `Collect(ctx)` is a method on `Collector`, whose `OSReleasePath` and `Logger` are injectable for tests; the package-level `Collect(ctx)` reads the host. The only error it returns is a finished context. Every other missing field is a debug log entry and an empty value, so a Machine always enrolls.
- `ReportedIPs` keeps global unicast addresses only: loopback, link-local and multicast are dropped (a literal "non-loopback unicast" list would be mostly `fe80::` addresses), IPv4 comes before IPv6, duplicates across interfaces appear once, the list is capped at 16 and is never nil so it encodes as `[]`.
- `OSVersion` falls back to `<sysname> <release>` (`uname -sr`) when os-release is missing or has no `PRETTY_NAME`. The parser handles unquoted, single-quoted and double-quoted values with the shell escapes os-release(5) allows.
- `AgentUID` is `UnknownUID` (-1) when the platform has no numeric user id, because 0 would read as root.
- `sysinfo_other.go` (`!linux`) stubs `uname()` so the module builds and tests on macOS; kernel and OS version are empty there, which §16.8 allows until another platform is supported. `Utsname` fields are `int8` on amd64 and `uint8` on arm64, hence a small generic helper.
- `Info.EnrollRequest()` and `Info.WorkMetadata()` are not here: `internal/protocol` arrives with task 003 (blocked on the Server's spec). Add them next to the protocol types, or in task 006 where they are first used.

