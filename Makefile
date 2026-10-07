VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
PKG     := github.com/cocardoso/gh-runners-manager
LDFLAGS := -s -w -X $(PKG)/internal/version.Version=$(VERSION) -X $(PKG)/internal/version.Commit=$(COMMIT)

.PHONY: build build-linux test vet check web web-api web-check

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

web-api:
	go run ./cmd/ghrm openapi > web/openapi.json
	cd web && pnpm api

web-check:
	cd web && pnpm lint && pnpm typecheck && pnpm test

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/ghrm ./cmd/ghrm
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/ghrm-agent ./cmd/ghrm-agent

build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/linux-amd64/ghrm ./cmd/ghrm
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/linux-amd64/ghrm-agent ./cmd/ghrm-agent

test:
	go test -race ./...

vet:
	go vet ./...

check: vet test build
