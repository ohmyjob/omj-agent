# 017 · End-to-end harness and core scenarios

Status: todo
Repo: ohmyjob-agent
Depends on: 014
PRD: §8 (harness location), §28 (end-to-end), §30 items 3–13

## Goal

A reproducible environment with the real Server image and two real Agents, plus the first scenarios.

## Scope

- `test/e2e/docker-compose.yml`: service `server` from `ghcr.io/ohmyjob/server:${OMJ_SERVER_TAG:-main}` with a tmpfs `/data`, `APP_URL=http://server:8080`, and short reconcile settings via environment (`OMJ_RUN_LOST_AFTER_SECONDS=60`, `OMJ_MACHINE_OFFLINE_AFTER_SECONDS=30`); services `agent-a` and `agent-b` built from a small Debian image with the binary from the current checkout, running `omj-agent run` as user `ohmyjob` with `--insecure-http` enrollment (plain HTTP is acceptable inside the harness network only). Network `omj` so tests can disconnect a container.
- Go harness in `test/e2e` behind `//go:build e2e`: helpers to wait for `/up`, run `php artisan omj:install` and `omj:enrollment-token` through `docker exec`, and an Inertia-aware HTTP client (login with CSRF cookie, POST forms, GET pages with `X-Inertia: true` to read props) to create Jobs, trigger Run now, read Run state and log text, and cancel. Everything the tests observe goes through the product's real endpoints.
- Scenarios: both Agents enroll and show online within 30 s with correct metadata (`agent_user = ohmyjob`); create a Job on A, Run now → `success`, exit 0, expected output; failing command → `failed`, exit code 2, stderr text visible; an every-minute Job dispatches automatically within 70 s of creation with `trigger = scheduled`; expired token rejected; reused token rejected.
- `make e2e` target; CI job that builds the Agent, pulls the Server image and runs the suite (allowed to be slower, separate workflow).

## Files

- `test/e2e/docker-compose.yml`, `test/e2e/agent.Dockerfile`, `test/e2e/harness.go`, `test/e2e/inertia_client.go`, `test/e2e/core_test.go`, `Makefile`, `.github/workflows/e2e.yml`

## Acceptance criteria

- [ ] `make e2e` passes locally with Docker and in CI.
- [ ] Every assertion reads state through the Server's UI or Agent API, never the database.

## Tests

- The scenarios above.
