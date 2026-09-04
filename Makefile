BINARY  := bin/omj-agent
VERSION ?= $(shell git describe --tags 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PACKAGE := github.com/ohmyjob/omj-agent/internal/version
LDFLAGS := -s -w -X $(PACKAGE).Version=$(VERSION) -X $(PACKAGE).Commit=$(COMMIT) -X $(PACKAGE).Date=$(DATE)
GORELEASER_VERSION := 2.18.0
GORELEASER ?= $(shell command -v goreleaser 2>/dev/null || echo go run github.com/goreleaser/goreleaser/v2@v$(GORELEASER_VERSION))
SERVER_DIR ?= ../omj-server
OMJ_SERVER_IMAGE ?= ohmyjob/server:e2e

.PHONY: build test lint fmt clean sync-fixtures test-install release-snapshot e2e server-image

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/omj-agent

test:
	go test -race ./...

lint:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then echo "$$unformatted"; echo "gofmt: the files above need formatting"; exit 1; fi
	go vet ./...
	golangci-lint run
	shellcheck packaging/install.sh packaging/test/*.sh

fmt:
	gofmt -w .

clean:
	rm -rf bin dist coverage.out

# Installs a packaged release inside Debian and Fedora containers (needs Docker). Set
# RELEASE_DIR to a GoReleaser output directory to test real archives instead of an ad hoc build.
test-install:
	RELEASE_DIR="$(RELEASE_DIR)" sh packaging/test/run.sh

# Builds the release archives and SHA256SUMS into dist/ without publishing anything.
release-snapshot:
	$(GORELEASER) release --snapshot --clean

# Runs the end-to-end suite against the real Server image and two real Agents (needs
# Docker). The images are private, so the Server image is built from a server checkout
# unless OMJ_SERVER_IMAGE names one that can be pulled.
e2e:
	go test -tags e2e -count=1 -timeout 20m ./test/e2e/...

# Builds the Server image the harness defaults to: make server-image SERVER_DIR=../omj-server
server-image:
	@test -f "$(SERVER_DIR)/docker/Dockerfile" || { echo "server-image: SERVER_DIR must point at a server checkout ($(SERVER_DIR)/docker/Dockerfile is missing)"; exit 1; }
	cd "$(SERVER_DIR)" && docker build -f docker/Dockerfile --build-arg VERSION=e2e -t $(OMJ_SERVER_IMAGE) .

# Refresh the protocol fixtures from a server checkout: make sync-fixtures SERVER_DIR=../omj-server
sync-fixtures:
	@test -d "$(SERVER_DIR)/docs/fixtures/agent-protocol-v1" || { echo "sync-fixtures: SERVER_DIR must point at a server checkout ($(SERVER_DIR)/docs/fixtures/agent-protocol-v1 is missing)"; exit 1; }
	rm -f internal/protocol/testdata/agent-protocol-v1/*.json
	cp "$(SERVER_DIR)"/docs/fixtures/agent-protocol-v1/*.json internal/protocol/testdata/agent-protocol-v1/
