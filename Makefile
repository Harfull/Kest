.DEFAULT_GOAL := help

BINARY   := kest
PKG      := github.com/Harfull/Kest
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
LDFLAGS  := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT)

.PHONY: help
help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the kest binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/kest

.PHONY: run
run: ## Run kest from source
	go run ./cmd/kest

.PHONY: fmt
fmt: ## Format code
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file is not formatted
	@test -z "$$(gofmt -l .)" || (echo "unformatted files:"; gofmt -l .; exit 1)

.PHONY: egg
egg: ## Regenerate the Pterodactyl egg from install.sh
	go run ./scripts/sync-egg

.PHONY: egg-check
egg-check: ## Fail if the committed egg is stale
	go run ./scripts/sync-egg -check

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: test
test: ## Run unit tests with the race detector
	go test -race -count=1 ./...

.PHONY: cover
cover: ## Run tests and write coverage.html
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

.PHONY: tidy
tidy: ## Tidy go.mod and go.sum
	go mod tidy

.PHONY: check
check: fmt-check egg-check vet lint test ## Everything CI runs; run before every commit

.PHONY: clean
clean: ## Remove build output
	rm -rf bin dist coverage.out coverage.html
