# Relaybox development tasks. Run `make help` for the list.
# Windows users without make can use scripts/dev.ps1 and the commands in
# docs/development.md, which are the same ones these targets run.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG     := github.com/abood-lmao/relaybox/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)
BINARY  := bin/relaybox
GO_DIRS := cmd internal api

export CGO_ENABLED = 0

.DEFAULT_GOAL := help
.PHONY: help deps dev dev-api dev-web build web go-build test test-go test-web lint lint-go lint-web fmt fmt-check clean docker

help: ## Show this list
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-12s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

deps: ## Install Go modules and frontend packages
	go mod download
	cd web && npm ci

dev: ## Run the API on :8787 and the dashboard with hot reload on :5173
	@trap 'kill 0' INT TERM EXIT; \
	go run ./cmd/relaybox serve & \
	cd web && npm run dev

dev-api: ## Run only the Go server
	go run ./cmd/relaybox serve

dev-web: ## Run only the Vite dev server (proxies to :8787)
	cd web && npm run dev

build: web go-build ## Build the dashboard and a single binary in bin/

web: ## Build the dashboard into internal/webui/static/dist
	cd web && npm run build

go-build: ## Build the Go binary with whatever dashboard build exists
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/relaybox

test: test-go test-web ## Run all tests

test-go: ## Run Go tests
	go test ./...

test-web: ## Run frontend tests
	cd web && npm test

lint: lint-go lint-web ## Run linters and format checks

lint-go:
	@test -z "$$(gofmt -l $(GO_DIRS))" || (echo "gofmt needed for:"; gofmt -l $(GO_DIRS); exit 1)
	go vet ./...

lint-web:
	cd web && npm run typecheck && npm run lint && npm run format:check

fmt: ## Format Go and frontend code
	gofmt -w $(GO_DIRS)
	cd web && npm run format

fmt-check: ## Check formatting without changing files
	@test -z "$$(gofmt -l $(GO_DIRS))" || (gofmt -l $(GO_DIRS); exit 1)
	cd web && npm run format:check

clean: ## Remove build output (keeps your data directory)
	rm -rf bin coverage web/coverage internal/webui/static/dist

docker: ## Build the container image
	docker build -t relaybox:local --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) .
