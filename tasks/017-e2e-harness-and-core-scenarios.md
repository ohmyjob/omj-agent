# 017 · End-to-end harness and core scenarios

Status: done
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

## Outcome (2026-09-04)

- The compose project is `omj-e2e-harness` on port 8210, so it cannot collide with
  the manual fleet that was running on 8150 while this was written. The Server
  image comes from `OMJ_SERVER_IMAGE`, defaulting to the locally built
  `ohmyjob/server:e2e`: both repositories are private, so
  `ghcr.io/ohmyjob/server:main` cannot be pulled. `make server-image
  SERVER_DIR=../omj-server` builds it, and the variable is all that has to change
  once the registry is reachable.
- The Agent image builds from the working tree, not from a release, so the suite
  tests the code in front of it. It must be stamped with a real semantic version:
  the first run was refused with exit 7 because `Version=e2e` is not semver and the
  Server compares it against `min_agent_version`. `AGENT_VERSION` defaults to
  `0.1.0` and is a build argument.
- Every assertion goes through the Server's own pages. Inertia 3 puts the page
  payload in the body of `<script data-page="app" type="application/json">`, not in
  the attribute, which cost the first attempt; the client reads it once to learn the
  asset version, then navigates with `X-Inertia` and posts with the CSRF cookie
  echoed in `X-XSRF-TOKEN`. The one exception is the Run log, which is a plain JSON
  endpoint and is read as one.
- Token scenarios enrol into a scratch `OMJ_CONFIG_DIR` and `OMJ_STATE_DIR` inside
  an Agent container. Without that the Agent refuses locally with
  `ExitAlreadyEnrolled` before the token ever reaches the Server, so the scenario
  would pass for the wrong reason. They assert the Agent's exit codes
  (`ExitTokenInvalid`, `ExitTokenExpired`) rather than message text.
- The expired-token scenario issues a token with a one-second life by clearing the
  cached configuration, running `omj:enrollment-token` with the lifetime overridden
  in that command's environment, then caching the configuration again. The image
  caches configuration at boot, so an environment override on `docker compose exec`
  is otherwise ignored. It beats sleeping out the real fifteen minutes.
- `FRANKENPHP_NUM_THREADS` is 16 for two Agents plus the suite's own requests, since
  each long poll holds a thread for its whole wait. `OMJ_MACHINE_OFFLINE_AFTER_SECONDS`
  is 30 and `OMJ_RUN_LOST_AFTER_SECONDS` is 60 so task 018 can watch a Machine fall
  offline without waiting minutes.
- The suite is behind `//go:build e2e`, so `make test` stays at ten seconds and
  `make e2e` runs the scenarios. A failed test prints the Server log and both Agent
  logs before tearing the project down. `docker info` is checked first and fails with
  an explanation, because Docker Desktop stopped twice unprompted during the manual
  runs that preceded this.
- Six scenarios pass in 31 to 40 seconds on arm64. The variance is the wait for a
  scheduler tick, which is the only scenario that cannot be made faster.
