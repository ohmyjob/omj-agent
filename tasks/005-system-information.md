# 005 · System information

Status: todo
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
