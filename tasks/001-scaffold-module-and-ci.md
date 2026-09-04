# 001 · Scaffold module and CI

Status: done
Repo: ohmyjob-agent
Depends on: —
PRD: §16.1, §16.2, §26, §29

## Goal

The Go module, directory layout, build tooling, version reporting and CI so every later task adds behaviour.

## Scope

- `go mod init github.com/ohmyjob/omj-agent`; pin the `go` directive to the latest stable release.
- Layout from §16.2 (create packages as they are needed; do not commit empty directories): `cmd/omj-agent/main.go` with a subcommand dispatcher built on `flag` (`enroll`, `run`, `status`, `version`, `doctor`; unknown → usage, exit 2), `internal/version` (`Version`, `Commit`, `Date` set by `-ldflags`, `ProtocolVersion = 1`, `UserAgent()`).
- `omj-agent version` prints `omj-agent <version> (<commit>, <date>) protocol <n>`.
- `Makefile` targets: `build` (static, `CGO_ENABLED=0`, ldflags), `test` (`go test -race ./...`), `lint` (`gofmt -l`, `go vet`, `golangci-lint run`), `fmt`, `clean`. `.golangci.yml` with the default linters plus `errcheck`, `staticcheck`, `gosec` (excluding rules that fight process execution), `misspell`.
- `.github/workflows/test.yml`: `make test lint` on pull requests and pushes to `main`, Go from `go.mod`.
- `LICENSE` (MIT), `README.md` stub with the product promise, `.gitignore`, `.editorconfig`.

## Files

- `go.mod`, `cmd/omj-agent/main.go`, `internal/version/version.go`, `Makefile`, `.golangci.yml`, `.github/workflows/test.yml`, `LICENSE`, `README.md`

## Acceptance criteria

- [ ] `make build` produces a static binary; `file` reports statically linked.
- [ ] `omj-agent version` prints the injected values; `omj-agent` with no args prints usage and exits 2.
- [ ] CI is green on a documentation-only pull request.

## Tests

- Unit: dispatcher routes each subcommand name; `UserAgent()` format.

## Outcome (2026-09-04)

- The dispatcher lives in `internal/cli` as `Run(args, stdout, stderr) int` so `cmd/omj-agent/main.go` is wiring only and the tests drive it without spawning a process. `help`, `-h` and `--help` print the usage to stdout and exit 0; commands that later tasks implement print "is not implemented yet" and exit 1; anything unknown prints the usage to stderr and exits 2. Each subcommand parses its own `flag.FlagSet`, so `version --bogus` exits 2.
- `go.mod` pins `go 1.27.1`, the latest stable release at the time. Older local toolchains download it on demand (`GOTOOLCHAIN=auto`). golangci-lint must be built with the same or a newer Go, so CI pins `v2.13.2` through the action's `install-only` mode and `make test lint` stays the single gate.
- errcheck ignores `fmt.Fprint*`: command output goes to stdout and stderr and a failed write has nowhere better to be reported. gosec excludes G204 because running user-defined commands is the product.
- `make build` takes `VERSION` from `git describe --tags` (falls back to `dev`), the commit from `git rev-parse --short HEAD` and the date in UTC; the release pipeline (task 016) overrides them. The binary is stripped with `-s -w` and built with `-trimpath`.
- `file` on macOS never reports "statically linked"; the criterion was checked with a `GOOS=linux GOARCH=amd64` cross build, which is what the release ships.

