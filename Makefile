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

.PHONY: test-go test-tf-validation test-playwright test-go-coverage test-go-coverage-ui test-integration test-integration-no-cutover test-cutover test-cutover-setup test-cutover-teardown test-osk-scan test-kafka-connect test-schema-registry test-env-up-migrate test-env-down-migrate test-migrate test-migrate-report test-migrate-cloud test-migrate-cloud-report

test-go: build-frontend ## Run Go unit tests (excludes Terraform validation; see test-tf-validation)
	go test $(GOTEST_FLAGS) ./...

test-tf-validation: build-frontend ## Run Terraform validation tests (requires terraform on PATH)
	go test -tags=terraform_validation -timeout 15m $(GOTEST_FLAGS) ./internal/services/hcl/...

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

test-integration: ## Run ALL integration suites in sequence with one aggregated grand total
	@bash integration-tests/run-all.sh

test-integration-no-cutover: ## Run all integration suites except the heavy cutover/Minikube one
	@bash integration-tests/run-all.sh --no-cutover

test-cutover: test-cutover-setup ## Run full cutover E2E lifecycle (setup, test, teardown)
	@trap 'echo ""; echo "Tearing down cutover E2E infrastructure..."; bash integration-tests/cutover/testdata/teardown.sh' EXIT; \
	echo "Running cutover E2E tests..."; \
	go test -v -tags=e2e -timeout 15m ./integration-tests/cutover/...

test-cutover-setup: ## Set up Minikube + CFK infrastructure for cutover E2E
	@bash integration-tests/cutover/testdata/setup.sh

test-cutover-teardown: ## Tear down cutover E2E infrastructure
	@bash integration-tests/cutover/testdata/teardown.sh

test-osk-scan: build ## Run OSK scan tests (all auth methods, JMX, Prometheus)
	@bash integration-tests/osk-scan/setup.sh
	cd integration-tests/osk-scan && go test -tags integration -v ./... ; \
	  status=$$? ; cd ../.. ; bash integration-tests/osk-scan/teardown.sh ; exit $$status

test-kafka-connect: build ## Run Kafka Connect self-managed connector scan tests
	@bash integration-tests/connect-scan/setup.sh
	cd integration-tests/connect-scan && go test -tags integration -v ./... ; \
	  status=$$? ; cd ../.. ; bash integration-tests/connect-scan/teardown.sh ; exit $$status

test-schema-registry: build ## Run Schema Registry scan tests (unauthenticated, basic auth)
	@bash integration-tests/schema-registry/setup.sh
	cd integration-tests/schema-registry && go test -tags integration -v ./... ; \
	  status=$$? ; cd ../.. ; bash integration-tests/schema-registry/teardown.sh ; exit $$status

test-env-up-migrate: ## Start the migrate test env (source + dest cp-server, all auth listeners)
	bash integration-tests/migrate/generate-certs.sh
	# MDS (dest-bearer) refuses a world-readable user store; git does not preserve
	# a 0600 mode across a fresh checkout, so enforce it before the broker mounts it.
	chmod 600 integration-tests/migrate/rest-auth/mds-users.properties
	docker compose -f integration-tests/migrate/docker-compose.yml up -d
	bash integration-tests/migrate/setup-scram.sh

test-env-down-migrate: ## Stop the migrate test env
	docker compose -f integration-tests/migrate/docker-compose.yml down -v

test-migrate: build ## Run the migrate apply E2E tests (cluster link + topics, all source auth methods)
	$(MAKE) test-env-up-migrate
	cd integration-tests/migrate && go test -tags integration -v ./... ; \
	  status=$$? ; cd ../.. ; $(MAKE) test-env-down-migrate ; exit $$status

test-migrate-report: build ## Run the migrate apply E2E tests and write a markdown evidence report to integration-tests/migrate/migrate-report.md (gitignored)
	$(MAKE) test-env-up-migrate
	cd integration-tests/migrate && KCP_MATRIX_REPORT=migrate-report.md go test -tags integration -v ./... ; \
	  status=$$? ; cd ../.. ; $(MAKE) test-env-down-migrate ; exit $$status

test-migrate-cloud: build ## Run the live MSK→CC cloud tests (env-gated; needs CC_*/MSK_* creds; no docker)
	cd integration-tests/migrate && go test -tags integration -run Cloud -v ./...

test-migrate-cloud-report: build ## Run the live cloud tests and write migrate-cloud-report.md (gitignored)
	cd integration-tests/migrate && KCP_MATRIX_REPORT=migrate-cloud-report.md go test -tags integration -run Cloud -v ./...

# ==============================================================================
# State-file backward-compat archive (real generated kcp-state.json fixtures)
# ==============================================================================

# A versioned, secret-scrubbed archive of real kcp-state.json files generated by
# every release v0.4.0+, used by the opt-in TestStateArchiveLoads regression test.
# Stored in S3 (us-east-1), reachable by local devs only — NOT from CI. When you
# refresh the archive (e.g. add a new release), upload state-archive-vN.tar.gz and
# bump STATE_ARCHIVE_VERSION + STATE_ARCHIVE_SHA256 together in one commit.
STATE_ARCHIVE_VERSION ?= v2
STATE_ARCHIVE_URI ?= s3://confluent-kcp-test-fixtures/state-archive/state-archive-$(STATE_ARCHIVE_VERSION).tar.gz
STATE_ARCHIVE_SHA256 ?= fdf65ae31ac79eebd07f46e745e90a83d093e85353e996e9b3ac4c28a5714aab
STATE_ARCHIVE_CACHE ?= .cache/state-archive

.PHONY: fetch-state-archive test-state-archive

fetch-state-archive: ## Download + verify + extract the real state-file archive from S3 (local only; needs AWS creds)
	@mkdir -p $(STATE_ARCHIVE_CACHE)
	@echo "downloading $(STATE_ARCHIVE_URI)"
	@aws s3 cp $(STATE_ARCHIVE_URI) $(STATE_ARCHIVE_CACHE)/state-archive.tar.gz --region us-east-1
	@CHECK="$(STATE_ARCHIVE_SHA256)  $(STATE_ARCHIVE_CACHE)/state-archive.tar.gz"; \
		if command -v shasum >/dev/null 2>&1; then echo "$$CHECK" | shasum -a 256 -c -; \
		else echo "$$CHECK" | sha256sum -c -; fi
	@tar -xzf $(STATE_ARCHIVE_CACHE)/state-archive.tar.gz -C $(STATE_ARCHIVE_CACHE)
	@echo "state archive ready at $(STATE_ARCHIVE_CACHE) ($$(ls -d $(STATE_ARCHIVE_CACHE)/v* | wc -l | tr -d ' ') versions)"

# No build-frontend dep: internal/types does not embed the frontend dist, so the load test
# runs without it (keeps this S3-backed target fast).
test-state-archive: fetch-state-archive ## Load every archived real state file through the loader (opt-in regression test)
	KCP_STATE_ARCHIVE=$(abspath $(STATE_ARCHIVE_CACHE)) go test ./internal/types/ -run TestStateArchiveLoads -v

# ==============================================================================
# Documentation (MkDocs Material + mike)
# ==============================================================================

# Only prereq is Python 3. An auto-managed venv is created on demand, so there
# is no global install (avoids PEP 668 "externally-managed-environment") and no
# pip/pip3/pipx ambiguity. Prefers uv when present (much faster); otherwise the
# stdlib venv. Neither path hard-codes an index, so pip.conf / CodeArtifact is
# respected as-is — `dev-login` stays the only auth step.
DOCS_VENV  := .venv-docs
DOCS_BIN   := $(DOCS_VENV)/bin
DOCS_STAMP := $(DOCS_VENV)/.stamp

.PHONY: docs-install docs-gen docs-serve docs-build

$(DOCS_STAMP): requirements-docs.txt
	@if command -v uv >/dev/null 2>&1; then \
		echo "🔍 Setting up docs env with uv"; \
		uv venv $(DOCS_VENV); \
		VIRTUAL_ENV=$(DOCS_VENV) uv pip install -r requirements-docs.txt; \
	else \
		echo "🔍 Setting up docs env with python venv"; \
		python3 -m venv $(DOCS_VENV); \
		$(DOCS_BIN)/python -m pip install --quiet --upgrade pip; \
		$(DOCS_BIN)/python -m pip install -r requirements-docs.txt; \
	fi
	@touch $@

docs-install: $(DOCS_STAMP) ## Set up the local docs env (only needs Python 3)

docs-gen: ## Regenerate per-command docs from Cobra into docs/assets/command-reference/
	go run ./cmd/gen-docs --out docs/assets/command-reference

docs-serve: docs-install docs-gen ## Serve docs locally with live reload on http://localhost:8000
	$(DOCS_BIN)/mkdocs serve

docs-build: docs-install docs-gen ## Build the docs site into ./site
	$(DOCS_BIN)/mkdocs build --strict

# ==============================================================================
# Utilities
# ==============================================================================

.PHONY: clean help

clean: ## Clean build artifacts
	rm -rf $(BINARY_NAME) coverage.out site/ $(DOCS_VENV)

help: ## Show available commands
	@grep -E '^[a-zA-Z0-9_-]+:.*## ' $(MAKEFILE_LIST) | \
		awk -F ':.*## ' '{printf "  %-30s %s\n", $$1, $$2}' | \
		sort
