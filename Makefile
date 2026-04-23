# ==============================================================================
# Variables
# ==============================================================================

BINARY_NAME := kcp
MAIN_PATH := .
GOTEST_FLAGS ?= -v

COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
DATE := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION := 0.0.0-localdev
LD_FLAGS := -X github.com/confluentinc/kcp/internal/build_info.Version=$(VERSION) \
            -X github.com/confluentinc/kcp/internal/build_info.Commit=$(COMMIT) \
            -X github.com/confluentinc/kcp/internal/build_info.Date=$(DATE)

# ==============================================================================
# Build
# ==============================================================================

.PHONY: build-frontend build build-linux build-linux-arm64 build-darwin build-darwin-arm64 build-windows build-all

build-frontend: ## Build the frontend application
	@echo "Building frontend..."
	@cd cmd/ui/frontend && yarn install && yarn build

build: build-frontend ## Build the binary for current platform
	@echo "Building $(BINARY_NAME)..."
	go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME) $(MAIN_PATH)

build-linux: ## Build for Linux amd64
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-linux-amd64 $(MAIN_PATH)

build-linux-arm64: ## Build for Linux arm64
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-linux-arm64 $(MAIN_PATH)

build-darwin: ## Build for macOS amd64 (Intel)
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)

build-darwin-arm64: ## Build for macOS arm64 (Apple Silicon)
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-darwin-arm64 $(MAIN_PATH)

build-windows: ## Build for Windows amd64
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)

build-all: build-frontend build-linux build-linux-arm64 build-darwin build-darwin-arm64 build-windows ## Build for all platforms

# ==============================================================================
# Install
# ==============================================================================

.PHONY: install uninstall

install: build ## Build and install to /usr/local/bin (requires sudo)
	sudo mv $(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
	@echo "$(BINARY_NAME) installed to /usr/local/bin"

uninstall: ## Uninstall from /usr/local/bin (requires sudo)
	sudo rm -f /usr/local/bin/$(BINARY_NAME)
	@echo "$(BINARY_NAME) uninstalled from /usr/local/bin"

# ==============================================================================
# Code Quality
# ==============================================================================

.PHONY: fmt lint pre-commit-install

fmt: ## Format Go code
	gofmt -s -w .

lint: ## Run Go linters (golangci-lint)
	golangci-lint run --config .golangci.yml ./...

pre-commit-install: ## Install git pre-commit hooks
	git config --local core.hooksPath .githooks

# ==============================================================================
# Tests
# ==============================================================================

.PHONY: test-go test-playwright test-go-coverage test-go-coverage-ui test-migration test-migration-setup test-migration-teardown test-osk-scan test-schema-registry

test-go: build-frontend ## Run Go unit tests
	go test $(GOTEST_FLAGS) ./...

test-playwright: build ## Run Playwright browser tests
	@cd cmd/ui/frontend && npx playwright test --reporter=list

test-go-coverage: build-frontend ## Run Go tests with coverage report
	@go test -timeout 15m -coverprofile=coverage.out ./...
	@echo ""
	@echo "Coverage Report:"
	@go tool cover -func=coverage.out | column -t
	@echo ""
	@go tool cover -func=coverage.out | grep "total:" | awk '{print "Overall: " $$3}'

test-go-coverage-ui: build-frontend ## Run Go tests with coverage and open HTML report
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

test-migration: test-migration-setup ## Run full migration E2E lifecycle (setup, test, teardown)
	@trap 'echo ""; echo "Tearing down migration E2E infrastructure..."; bash integration-tests/migration/testdata/teardown.sh' EXIT; \
	echo "Running migration E2E tests..."; \
	go test -v -tags=e2e -timeout 15m ./integration-tests/migration/...

test-migration-setup: ## Set up Minikube + CFK infrastructure for migration E2E
	@bash integration-tests/migration/testdata/setup.sh

test-migration-teardown: ## Tear down migration E2E infrastructure
	@bash integration-tests/migration/testdata/teardown.sh

test-osk-scan: build ## Run OSK scan tests (all auth methods, JMX, Prometheus)
	@bash integration-tests/osk-scan/setup.sh
	@bash integration-tests/osk-scan/run.sh || (bash integration-tests/osk-scan/teardown.sh; exit 1)
	@bash integration-tests/osk-scan/teardown.sh

test-schema-registry: build ## Run Schema Registry scan tests (unauthenticated, basic auth)
	@bash integration-tests/schema-registry/setup.sh
	@bash integration-tests/schema-registry/run.sh || (bash integration-tests/schema-registry/teardown.sh; exit 1)
	@bash integration-tests/schema-registry/teardown.sh

# ==============================================================================
# Documentation (MkDocs Material + mike)
# ==============================================================================

.PHONY: docs-install docs-gen docs-serve docs-build

docs-install: ## Install MkDocs and plugins (pip)
	pip install -r requirements-docs.txt

docs-gen: ## Regenerate per-command docs from Cobra into docs/command-reference/
	go run ./cmd/gen-docs --out docs/command-reference

docs-serve: docs-gen ## Serve docs locally with live reload on http://localhost:8000
	mkdocs serve

docs-build: docs-gen ## Build the docs site into ./site
	mkdocs build --strict

# ==============================================================================
# Utilities
# ==============================================================================

.PHONY: clean help

clean: ## Clean build artifacts
	rm -rf $(BINARY_NAME) coverage.out site/

help: ## Show available commands
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | \
		awk -F ':.*## ' '{printf "  %-30s %s\n", $$1, $$2}' | \
		sort
