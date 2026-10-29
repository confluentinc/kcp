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

.PHONY: build-frontend build build-linux build-linux-arm64 build-darwin build-darwin-arm64 build-windows build-all build-lambda-scanner

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

LAMBDA_ZIP := dist/kcp-lambda-scanner-linux-arm64.zip
LAMBDA_BUILD_DIR := dist/lambda-scanner

build-lambda-scanner: build-frontend ## Build the Lambda scanner zip (bootstrap + kcp, linux/arm64) in dist/
	@rm -rf $(LAMBDA_BUILD_DIR) $(LAMBDA_ZIP)
	@mkdir -p $(LAMBDA_BUILD_DIR)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w $(LD_FLAGS)" -o $(LAMBDA_BUILD_DIR)/kcp $(MAIN_PATH)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o $(LAMBDA_BUILD_DIR)/bootstrap ./cmd/lambda-scanner
	@cd $(LAMBDA_BUILD_DIR) && zip -q -9 ../$(notdir $(LAMBDA_ZIP)) bootstrap kcp
	@rm -rf $(LAMBDA_BUILD_DIR)
	@echo "built $(LAMBDA_ZIP) ($$(du -h $(LAMBDA_ZIP) | cut -f1))"
	@if command -v shasum >/dev/null 2>&1; then shasum -a 256 $(LAMBDA_ZIP); else sha256sum $(LAMBDA_ZIP); fi

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

.PHONY: fmt lint trivy trivy-binary pre-commit-install

fmt: ## Format Go code
	gofmt -s -w .

lint: ## Run Go linters (golangci-lint)
	golangci-lint run --config .golangci.yml ./...

trivy: ## Run Trivy vulnerability scan
	trivy fs --scanners vuln --show-suppressed --severity HIGH,CRITICAL --exit-code 1 .

# Scans the compiled linux binary. Unlike `trivy fs` it sees the Go stdlib the binary
# is built with. Same gate as the Docker Hub promote (fixable HIGH/CRITICAL only).
# One arch is enough: the stdlib and module versions are identical across arches.
# Needs the frontend built first (make build-frontend). TRIVY_FLAGS adds extra flags.
trivy-binary: ## Build the linux binary and run the Trivy gate on it
	@mkdir -p trivy-scan
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o trivy-scan/kcp .
	go version trivy-scan/kcp
	trivy rootfs --scanners vuln --show-suppressed --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 $(TRIVY_FLAGS) trivy-scan/

pre-commit-install: ## Install git pre-commit hooks
	git config --local core.hooksPath .githooks

# ==============================================================================
# Tests
# ==============================================================================

.PHONY: test-go test-tf-validation examples test-playwright test-go-coverage test-go-coverage-ui test-integration test-integration-no-migration test-migration test-migration-setup test-migration-teardown test-migration-hot-reload test-migration-hot-reload-setup test-migration-hot-reload-run test-migration-hot-reload-teardown test-migration-tbm test-migration-tbm-setup test-migration-tbm-run test-migration-tbm-teardown test-idempotent-fsm test-idempotent-fsm-setup test-idempotent-fsm-run test-idempotent-fsm-teardown test-route-conversion test-route-conversion-setup test-route-conversion-run test-route-conversion-teardown test-retries test-retries-setup test-retries-run test-retries-teardown otel-up otel-down test-tracing test-tracing-setup test-tracing-run test-tracing-teardown test-osk-scan test-consumer-group-scan test-kafka-connect test-schema-registry test-env-up-migrate test-env-down-migrate test-migrate test-migrate-report test-migrate-cloud test-migrate-cloud-report test-migrate-acls test-migrate-acls-live test-env-up-migplan test-env-down-migplan test-migplan

test-go: build-frontend ## Run Go unit tests (excludes Terraform validation; see test-tf-validation)
	go test $(GOTEST_FLAGS) ./...

test-tf-validation: build-frontend ## Run Terraform validation tests (requires terraform on PATH)
	go test -tags=terraform_validation -timeout 15m $(GOTEST_FLAGS) ./internal/services/hcl/...

examples: ## Regenerate the committed docs/assets/report-plan-examples/** reference outputs from the demo-scan.json fixture
	UPDATE_EXAMPLES=1 go test ./internal/services/plan/ -run TestReportPlanExamples_UpToDate

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

test-integration-no-migration: ## Run all integration suites except the heavy migration/Minikube one
	@bash integration-tests/run-all.sh --no-migration

test-migration: test-migration-setup ## Run full migration E2E lifecycle (setup, test, teardown)
	@trap 'echo ""; echo "Tearing down migration E2E infrastructure..."; bash integration-tests/migration/testdata/teardown.sh' EXIT; \
	echo "Running migration E2E tests..."; \
	go test -v -tags=e2e -timeout 40m ./integration-tests/migration/...

test-migration-setup: ## Set up Minikube + CFK infrastructure for migration E2E
	@bash integration-tests/migration/testdata/setup.sh

test-migration-teardown: ## Tear down migration E2E infrastructure
	@bash integration-tests/migration/testdata/teardown.sh

test-migration-hot-reload: test-migration-hot-reload-setup ## Run gateway hot-reload E2E (own cluster; needs a CP Enterprise licence)
	@trap 'echo ""; echo "Tearing down hot-reload E2E infrastructure..."; bash integration-tests/migration-hot-reload/teardown.sh' EXIT; \
	bash integration-tests/migration-hot-reload/run.sh

test-migration-hot-reload-setup: ## Set up the licensed hot-reload gateway cluster (separate Minikube profile)
	@bash integration-tests/migration-hot-reload/setup.sh

test-migration-hot-reload-run: ## Run the hot-reload E2E against an already-provisioned cluster (no teardown)
	@bash integration-tests/migration-hot-reload/run.sh

test-migration-hot-reload-teardown: ## Tear down the hot-reload E2E cluster
	@bash integration-tests/migration-hot-reload/teardown.sh

test-migration-tbm: test-migration-tbm-setup ## Run TBM hot-reload E2E (own cluster; needs a CP Enterprise licence)
	@trap 'echo ""; echo "Tearing down TBM E2E infrastructure..."; bash integration-tests/migration-tbm/teardown.sh' EXIT; \
	bash integration-tests/migration-tbm/run.sh

test-migration-tbm-setup: ## Set up the licensed dynamic-gateway TBM cluster (separate Minikube profile)
	@bash integration-tests/migration-tbm/setup.sh

test-migration-tbm-run: ## Run the TBM E2E against an already-provisioned cluster (no teardown)
	@bash integration-tests/migration-tbm/run.sh

test-migration-tbm-teardown: ## Tear down the TBM E2E cluster
	@bash integration-tests/migration-tbm/teardown.sh

test-idempotent-fsm: test-idempotent-fsm-setup ## Run the idempotent-FSM live resume E2E (own cluster; needs a CP Enterprise licence + AWS creds). GATEWAY_MODE=dynamic|static
	@trap 'echo ""; echo "Tearing down idempotent-fsm E2E infrastructure..."; bash integration-tests/idempotent-fsm/teardown.sh' EXIT; \
	bash integration-tests/idempotent-fsm/run.sh

test-idempotent-fsm-setup: ## Set up the idempotent-fsm resume cluster (GATEWAY_MODE=dynamic default, or static for AAO)
	@bash integration-tests/idempotent-fsm/setup.sh

test-idempotent-fsm-run: ## Run the idempotent-fsm resume E2E against an already-provisioned cluster (no teardown). GATEWAY_MODE=dynamic|static picks it. Arg: a -test.run selector
	@bash integration-tests/idempotent-fsm/run.sh "$(RUN)"

test-idempotent-fsm-teardown: ## Tear down one mode's idempotent-fsm E2E cluster (GATEWAY_MODE=dynamic default, or static)
	@bash integration-tests/idempotent-fsm/teardown.sh

test-route-conversion: test-route-conversion-setup ## Run the d2s route-conversion live E2E (own cluster; needs a CP Enterprise licence + AWS creds)
	@trap 'echo ""; echo "Tearing down route-conversion E2E infrastructure..."; bash integration-tests/route-conversion/teardown.sh' EXIT; \
	bash integration-tests/route-conversion/run.sh

test-route-conversion-setup: ## Set up the route-conversion cluster (Minikube profile kcp-e2e-route-conversion)
	@bash integration-tests/route-conversion/setup.sh

test-route-conversion-run: ## Run the route-conversion E2E against an already-provisioned cluster (no teardown). Arg: a -test.run selector
	@bash integration-tests/route-conversion/run.sh "$(RUN)"

test-route-conversion-teardown: ## Tear down the route-conversion E2E cluster
	@bash integration-tests/route-conversion/teardown.sh

test-retries: test-retries-setup ## Run the retries live E2E: kcp migration execute through clusters that disappear mid-run (own cluster; needs a CP Enterprise licence + AWS creds)
	@trap 'echo ""; echo "Tearing down retries E2E infrastructure..."; bash integration-tests/retries/teardown.sh' EXIT; \
	bash integration-tests/retries/run.sh

test-retries-setup: ## Set up the retries cluster (Minikube profile kcp-e2e-retries)
	@bash integration-tests/retries/setup.sh

test-retries-run: ## Run the retries E2E against an already-provisioned cluster (no teardown). Arg: a -test.run selector. One run uses up the cluster's topics
	@bash integration-tests/retries/run.sh "$(RUN)"

test-retries-teardown: ## Tear down the retries E2E cluster
	@bash integration-tests/retries/teardown.sh

OTEL_LGTM_IMAGE ?= grafana/otel-lgtm:0.34.0
OTEL_LGTM_NAME  ?= kcp-otel-lgtm

otel-up: ## Start a local OTel backend with the kcp tracing dashboard (Grafana on :3000, OTLP on :4317/:4318)
	docker run -d --rm --name $(OTEL_LGTM_NAME) -p 3000:3000 -p 4317:4317 -p 4318:4318 \
	  -v $(CURDIR)/internal/tracing/grafana:/otel-lgtm/kcp-dashboards:ro \
	  -v $(CURDIR)/internal/tracing/grafana/dashboards.yaml:/otel-lgtm/grafana/conf/provisioning/dashboards/kcp.yaml:ro \
	  -v $(CURDIR)/internal/tracing/grafana/tempo-config.yaml:/otel-lgtm/tempo-config.yaml:ro \
	  $(OTEL_LGTM_IMAGE)
	@echo "kcp tracing dashboard: http://localhost:3000/d/kcp-tracing (ready in ~30s). Send traces with:"
	@echo "  KCP_TRACING_ENABLED=true OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 kcp ..."

otel-down: ## Stop the local OTel backend
	docker stop $(OTEL_LGTM_NAME)

# GATEWAY_MODE picks the migration kind the tracing suite runs: dynamic (the
# default) or static from integration-tests/tracing, convert (dynamic to static)
# from integration-tests/tracing/convert. Each has its own Minikube profile.
TRACING_SUITE = $(if $(filter convert,$(GATEWAY_MODE)),integration-tests/tracing/convert,integration-tests/tracing)

test-tracing: test-tracing-setup ## Run the tracing smoke E2E (own cluster; needs a CP Enterprise licence + AWS creds). GATEWAY_MODE=dynamic|static|convert
	@trap 'echo ""; echo "Tearing down tracing E2E infrastructure..."; bash $(TRACING_SUITE)/teardown.sh' EXIT; \
	bash $(TRACING_SUITE)/run.sh

test-tracing-setup: ## Set up a tracing smoke cluster (GATEWAY_MODE=dynamic default, static, or convert)
	@bash $(TRACING_SUITE)/setup.sh

test-tracing-run: ## Run the tracing smoke E2E against an already-provisioned cluster (no teardown). GATEWAY_MODE=dynamic|static|convert
	@bash $(TRACING_SUITE)/run.sh "$(RUN)"

test-tracing-teardown: ## Tear down a tracing smoke cluster (GATEWAY_MODE=dynamic|static|convert)
	@bash $(TRACING_SUITE)/teardown.sh

test-osk-scan: build ## Run OSK scan tests (all auth methods, JMX, Prometheus)
	@bash integration-tests/osk-scan/setup.sh
	cd integration-tests/osk-scan && go test -tags integration -v ./... ; \
	  status=$$? ; cd ../.. ; bash integration-tests/osk-scan/teardown.sh ; exit $$status

test-consumer-group-scan: ## Run consumer-group discovery integration suite (Docker; AK 3.7/3.8/4.0/4.1/4.2, all group types)
	@bash integration-tests/consumer-group-scan/setup.sh
	cd integration-tests/consumer-group-scan && go test -tags integration -timeout 20m -v ./... ; \
	  status=$$? ; cd ../.. ; bash integration-tests/consumer-group-scan/teardown.sh ; exit $$status

test-kafka-connect: build ## Run Kafka Connect self-managed connector scan tests
	@cd integration-tests/connect-scan && \
	  if bash setup.sh; then \
	    go test -tags integration -timeout 8m -v ./... ; status=$$? ; \
	  else \
	    echo "ERROR: connect-scan setup.sh failed" ; status=1 ; \
	  fi ; \
	  if [ $$status -ne 0 ]; then \
	    echo "=== docker compose logs (connect-scan, on failure) ===" ; \
	    docker compose logs --no-color --tail=400 || true ; \
	  fi ; \
	  bash teardown.sh || true ; \
	  exit $$status

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

test-migrate: build ## Run the `kcp migration resources create` E2E tests (cluster link + topics, all source auth methods)
	$(MAKE) test-env-up-migrate
	cd integration-tests/migrate && go test -tags integration -v ./... ; \
	  status=$$? ; cd ../.. ; $(MAKE) test-env-down-migrate ; exit $$status

test-migrate-report: build ## Run the `kcp migration resources create` E2E tests and write a markdown evidence report to integration-tests/migrate/migrate-report.md (gitignored)
	$(MAKE) test-env-up-migrate
	cd integration-tests/migrate && KCP_MATRIX_REPORT=migrate-report.md go test -tags integration -v ./... ; \
	  status=$$? ; cd ../.. ; $(MAKE) test-env-down-migrate ; exit $$status

test-migrate-cloud: build ## Run the live MSK→CC cloud tests (env-gated; needs CC_*/MSK_* creds; no docker)
	cd integration-tests/migrate && go test -tags integration -run Cloud -v ./...

test-migrate-cloud-report: build ## Run the live cloud tests and write migrate-cloud-report.md (gitignored)
	cd integration-tests/migrate && KCP_MATRIX_REPORT=migrate-cloud-report.md go test -tags integration -run Cloud -v ./...

test-migrate-acls: build-frontend ## Run the hermetic native-ACL migration tests (unit + reconciler; CI, no creds, no docker)
	go test $(GOTEST_FLAGS) ./internal/migrate/acls/... ./internal/migrate/serviceaccounts/...

test-migrate-acls-live: build ## Run the live native-ACL + SA-naming integration matrix (env-gated; needs CC_*/MSK_* creds; no docker)
	cd integration-tests/migrate && go test -tags integration -run ACLsLive -v ./...

test-env-up-migplan: ## Start the migplan reconciliation-engine test env (source + dest cp-server + cluster link + ACTIVE mirrors)
	bash integration-tests/migplan/setup.sh

test-env-down-migplan: ## Stop the migplan reconciliation-engine test env
	bash integration-tests/migplan/teardown.sh

test-migplan: build-frontend ## Run the migplan reconciliation-engine E2E tests (live source + dest + cluster link; no gateway — TBR gateway apply deferred)
	$(MAKE) test-env-up-migplan
	go test -tags e2e -v -count=1 ./integration-tests/migplan/... ; \
	  status=$$? ; $(MAKE) test-env-down-migplan ; exit $$status

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
