BINARY  := bin/omj-agent
VERSION ?= $(shell git describe --tags 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PACKAGE := github.com/ohmyjob/omj-agent/internal/version
LDFLAGS := -s -w -X $(PACKAGE).Version=$(VERSION) -X $(PACKAGE).Commit=$(COMMIT) -X $(PACKAGE).Date=$(DATE)

.PHONY: build test lint fmt clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/omj-agent

test:
	go test -race ./...

lint:
	@unformatted="$$(gofmt -l .)"; if [ -n "$$unformatted" ]; then echo "$$unformatted"; echo "gofmt: the files above need formatting"; exit 1; fi
	go vet ./...
	golangci-lint run

fmt:
	gofmt -w .

clean:
	rm -rf bin dist coverage.out
