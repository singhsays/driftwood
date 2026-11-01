VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X driftwood/internal/version.Version=$(VERSION) -X driftwood/internal/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null)
GO      ?= go

# Prefer the locally installed golangci-lint; fall back to a pinned go run.
GOLANGCI_LINT_VERSION := v2.15.0
GOLANGCI_LINT := $(shell command -v golangci-lint 2>/dev/null || echo "$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)")

.PHONY: build build-linux install test fmt vet lint lint-fix check

build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/driftwood ./cmd/driftwood

build-linux:
	GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/driftwood-linux-amd64 ./cmd/driftwood

install:
	$(GO) install -trimpath -ldflags "$(LDFLAGS)" ./cmd/driftwood

test:
	$(GO) test -race ./...

fmt:
	$(GOLANGCI_LINT) fmt

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run

lint-fix:
	$(GOLANGCI_LINT) run --fix

# check is the local mirror of CI: run before committing.
check: vet lint test build
