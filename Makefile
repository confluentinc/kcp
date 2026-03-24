# Simple Makefile for kcp
BINARY_NAME := kcp
MAIN_PATH := .

COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
DATE := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION := 0.0.0-localdev
LD_FLAGS :=	-X github.com/confluentinc/kcp/internal/build_info.Version=$(VERSION) \
			-X github.com/confluentinc/kcp/internal/build_info.Commit=$(COMMIT) \
			-X github.com/confluentinc/kcp/internal/build_info.Date=$(DATE)

.PHONY: build clean help install fmt test test-go test-e2e test-cov test-cov-ui build-linux build-linux-arm64 build-darwin build-darwin-arm64 build-windows build-all build-frontend test-env-up-plaintext test-env-up-kraft test-env-up-sasl test-env-up-tls test-env-up-schema-registry test-env-up-jmx test-env-up-jmx-auth test-env-up-jmx-tls test-env-up-prometheus test-env-up-prometheus-auth test-env-up-prometheus-tls test-env-down test-integration-osk test-all-envs test-certs-generate

# Build the frontend
build-frontend:
	@echo "🌐 Building frontend..."
	@cd cmd/ui/frontend && yarn install && yarn build
	@echo "✅ Frontend build complete"

# Build the binary (depends on frontend)
build: build-frontend
	@echo "🔨 Building $(BINARY_NAME)..."
	go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME) $(MAIN_PATH)
	@echo "✅ Build complete: $(BINARY_NAME)"

# Individual platform builds
build-linux: build-frontend
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-linux-amd64 $(MAIN_PATH)

build-linux-arm64: build-frontend
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-linux-arm64 $(MAIN_PATH)

build-darwin: build-frontend
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)

build-darwin-arm64: build-frontend
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-darwin-arm64 $(MAIN_PATH)

build-windows: build-frontend
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)

# Build for all platforms and architectures
build-all: build-frontend
	@echo "🔨 Building for all platforms..."
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-linux-amd64 $(MAIN_PATH); \
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-linux-arm64 $(MAIN_PATH); \
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-darwin-amd64 $(MAIN_PATH); \
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-darwin-arm64 $(MAIN_PATH); \
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LD_FLAGS)" -o $(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)
	@echo "✅ All platform builds complete!"

# Clean build artifacts
clean:
	rm -f $(BINARY_NAME) coverage.out

# Show available commands
help:
	@echo "🛠️  KCP - Available Commands:"
	@echo "=========================================="
	@echo ""
	@echo "📦 build              - Build the binary for current platform"
	@echo "📦 build-frontend     - Build the frontend application"
	@echo "📦 build-linux        - Build for Linux amd64"
	@echo "📦 build-linux-arm64  - Build for Linux arm64"
	@echo "📦 build-darwin       - Build for macOS amd64 (Intel)"
	@echo "📦 build-darwin-arm64 - Build for macOS arm64 (Apple Silicon)"
	@echo "📦 build-windows      - Build for Windows amd64"
	@echo "📦 build-all          - Build for all platforms and architectures"
	@echo "🧹 clean              - Clean build artifacts"
	@echo "🚀 install            - Build and install to /usr/local/bin"
	@echo "✨ fmt                - Format code"
	@echo "🧪 test               - Run tests"
	@echo "📊 test-cov           - Run tests with coverage"
	@echo "🌐 test-cov-ui        - Coverage with HTML report"
	@echo ""
	@echo "💡 Usage: make <target>"

# Install the binary to /usr/local/bin (requires sudo)
install: build
	sudo mv $(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)
	@echo "$(BINARY_NAME) installed to /usr/local/bin"

# Uninstall the binary from /usr/local/bin (requires sudo)
uninstall:
	sudo rm -f /usr/local/bin/$(BINARY_NAME)
	@echo "$(BINARY_NAME) uninstalled from /usr/local/bin"

# Format code
fmt:
	gofmt -s -w .

# Run all tests (Go unit tests + Playwright E2E tests)
test: test-go test-e2e

# Run Go unit tests only
test-go: build-frontend
	@echo "🧪 Running Go tests..."
	@echo "======================"
	@bash -c 'go test -v ./...; exit_code=$$?; echo ""; if [ $$exit_code -ne 0 ]; then echo "❌ Go tests failed with exit code $$exit_code"; else echo "✅ All Go tests passed!"; fi; exit $$exit_code'

# Run Playwright E2E tests (builds everything first)
test-e2e: build
	@echo "🧪 Running Playwright E2E tests..."
	@echo "==================================="
	@cd cmd/ui/frontend && npx playwright test --reporter=list; exit_code=$$?; echo ""; if [ $$exit_code -ne 0 ]; then echo "❌ Playwright tests failed with exit code $$exit_code"; else echo "✅ All Playwright tests passed!"; fi; exit $$exit_code

# Run tests with coverage - beautiful terminal output
test-cov:
	@echo "🧪 Running tests with coverage analysis..."
	@echo "=========================================="
	@go test -coverprofile=coverage.out ./...
	@echo ""
	@echo "📊 Detailed Coverage Report:"
	@echo "=============================="
	@go tool cover -func=coverage.out | column -t
	@echo ""
	@echo "🎯 Overall Project Coverage:"
	@echo "=============================="
	@go tool cover -func=coverage.out | grep "total:" | awk '{print "   📈 " $$3}' 
	@echo ""
	@echo "💡 Tip: Use 'make coverage-ui' for detailed line-by-line analysis!"

# Run tests with coverage and open in browser directly
test-cov-ui:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

# Docker Compose test environments
test-env-up-plaintext:
	@echo "Starting plaintext Kafka test environment (ZooKeeper-based)..."
	docker-compose -f test/docker/docker-compose-plaintext.yml up -d
	@bash test/docker/scripts/wait-for-kafka.sh kcp-test-kafka-plaintext
	@bash test/docker/scripts/setup-test-data.sh kcp-test-kafka-plaintext

test-env-up-kraft:
	@echo "Starting KRaft Kafka test environment (no ZooKeeper)..."
	docker-compose -f test/docker/docker-compose-kraft.yml up -d
	@bash test/docker/scripts/wait-for-kafka.sh kcp-test-kafka-kraft
	@bash test/docker/scripts/setup-test-data.sh kcp-test-kafka-kraft

test-env-up-sasl:
	@echo "Starting SASL/SCRAM Kafka test environment..."
	docker-compose -f test/docker/docker-compose-sasl.yml up -d
	@echo "Waiting for SASL cluster to be ready (this may take 30-40 seconds)..."
	@sleep 30
	@bash test/docker/scripts/setup-test-data-sasl.sh
	@echo "SASL environment is ready on port 9093"

test-certs-generate:
	@echo "Generating TLS certificates for testing..."
	@bash test/docker/scripts/generate-certs.sh

test-env-up-tls: test-certs-generate
	@echo "Starting TLS/mTLS Kafka test environment..."
	docker-compose -f test/docker/docker-compose-tls.yml up -d
	@echo "Waiting for TLS cluster to be ready..."
	@sleep 20
	@bash test/docker/scripts/setup-test-data-tls.sh
	@echo "TLS environment is ready on port 9094"

test-env-up-schema-registry: test-env-up-plaintext
	@echo "Starting Schema Registry test environments..."
	docker-compose -f test/docker/docker-compose-schema-registry.yml up -d
	@bash test/docker/scripts/setup-test-schemas.sh
	@echo "Schema Registry environments are ready"
	@echo "  Unauthenticated: http://localhost:8081"
	@echo "  Basic Auth:      http://localhost:8082 (user: schemauser, pass: schemapass)"

test-env-up-jmx:
	@echo "Starting JMX Kafka test environment (unauthenticated Jolokia)..."
	docker-compose -f test/docker/docker-compose-jmx.yml up -d
	@bash test/docker/scripts/wait-for-kafka.sh kcp-test-kafka-jmx
	@bash test/docker/scripts/setup-test-data-jmx.sh kcp-test-kafka-jmx
	@echo "JMX environment is ready on port 9096"
	@echo "  Kafka:   localhost:9096"
	@echo "  Jolokia: http://localhost:8778/jolokia"

test-env-up-jmx-auth:
	@echo "Starting JMX Kafka test environment (password-authenticated Jolokia)..."
	docker-compose -f test/docker/docker-compose-jmx-auth.yml up -d
	@bash test/docker/scripts/wait-for-kafka.sh kcp-test-kafka-jmx-auth
	@bash test/docker/scripts/setup-test-data-jmx.sh kcp-test-kafka-jmx-auth
	@echo "JMX environment is ready on port 9097"
	@echo "  Kafka:   localhost:9097"
	@echo "  Jolokia: http://localhost:8779/jolokia (user: monitorUser, pass: monitorPass)"

test-env-up-jmx-tls: test-certs-generate
	@echo "Starting JMX Kafka test environment (TLS + password-authenticated Jolokia)..."
	docker-compose -f test/docker/docker-compose-jmx-tls.yml up -d
	@bash test/docker/scripts/wait-for-kafka.sh kcp-test-kafka-jmx-tls
	@bash test/docker/scripts/setup-test-data-jmx.sh kcp-test-kafka-jmx-tls
	@echo "JMX environment is ready on port 9098"
	@echo "  Kafka:   localhost:9098"
	@echo "  Jolokia: https://localhost:8780/jolokia (user: monitorUser, pass: monitorPass)"

test-env-up-prometheus:
	@echo "Starting Prometheus test environment (unauthenticated)..."
	docker-compose -f test/docker/docker-compose-prometheus.yml up -d
	@echo "Waiting for Prometheus seeder to complete..."
	@docker wait kcp-test-prometheus-seeder >/dev/null 2>&1 || true
	@echo "Restarting Prometheus to load seeded data..."
	@docker restart kcp-test-prometheus >/dev/null 2>&1 && sleep 3
	@echo "Prometheus environment is ready"
	@echo "  Prometheus: http://localhost:9190"

test-env-up-prometheus-auth:
	@echo "Starting Prometheus test environment (basic auth)..."
	docker-compose -f test/docker/docker-compose-prometheus-auth.yml up -d
	@echo "Waiting for Prometheus seeder to complete..."
	@docker wait kcp-test-prometheus-auth-seeder >/dev/null 2>&1 || true
	@echo "Restarting Prometheus to load seeded data..."
	@docker restart kcp-test-prometheus-auth >/dev/null 2>&1 && sleep 3
	@echo "Prometheus auth environment is ready"
	@echo "  Prometheus: http://localhost:9191 (user: promuser, pass: prompass)"

test-env-up-prometheus-tls: test-certs-generate
	@echo "Starting Prometheus test environment (TLS + basic auth)..."
	docker-compose -f test/docker/docker-compose-prometheus-tls.yml up -d
	@echo "Waiting for Prometheus seeder to complete..."
	@docker wait kcp-test-prometheus-tls-seeder >/dev/null 2>&1 || true
	@echo "Restarting Prometheus to load seeded data..."
	@docker restart kcp-test-prometheus-tls >/dev/null 2>&1 && sleep 3
	@echo "Prometheus TLS environment is ready"
	@echo "  Prometheus: https://localhost:9192 (user: promuser, pass: prompass)"

test-env-down:
	@echo "Stopping all test environments..."
	docker-compose -f test/docker/docker-compose-schema-registry.yml down -v 2>/dev/null || true
	docker-compose -f test/docker/docker-compose-plaintext.yml down -v 2>/dev/null || true
	docker-compose -f test/docker/docker-compose-kraft.yml down -v 2>/dev/null || true
	docker-compose -f test/docker/docker-compose-sasl.yml down -v 2>/dev/null || true
	docker-compose -f test/docker/docker-compose-tls.yml down -v 2>/dev/null || true
	docker-compose -f test/docker/docker-compose-jmx.yml down -v 2>/dev/null || true
	docker-compose -f test/docker/docker-compose-jmx-auth.yml down -v 2>/dev/null || true
	docker-compose -f test/docker/docker-compose-jmx-tls.yml down -v 2>/dev/null || true
	docker-compose -f test/docker/docker-compose-prometheus.yml down -v 2>/dev/null || true
	docker-compose -f test/docker/docker-compose-prometheus-auth.yml down -v 2>/dev/null || true
	docker-compose -f test/docker/docker-compose-prometheus-tls.yml down -v 2>/dev/null || true

test-integration-osk: test-env-up-plaintext
	@echo "Running OSK integration tests (ZooKeeper mode)..."
	TEST_KAFKA_BOOTSTRAP=localhost:9092 go test -tags=integration ./cmd/scan/clusters/... -v
	$(MAKE) test-env-down
	@echo "Running OSK integration tests (KRaft mode)..."
	$(MAKE) test-env-up-kraft
	TEST_KAFKA_BOOTSTRAP=localhost:9095 go test -tags=integration ./cmd/scan/clusters/... -v
	$(MAKE) test-env-down

test-all-envs:
	@echo "Testing OSK scanning against all Kafka configurations..."
	@echo "\n=== Testing ZooKeeper-based cluster (Plaintext) ==="
	$(MAKE) test-env-up-plaintext
	./kcp scan clusters --source-type osk --credentials-file test/credentials/osk-credentials-plaintext.yaml --state-file test-state-plaintext.json
	$(MAKE) test-env-down
	@echo "\n=== Testing KRaft-based cluster (Plaintext) ==="
	$(MAKE) test-env-up-kraft
	./kcp scan clusters --source-type osk --credentials-file test/credentials/osk-credentials-kraft.yaml --state-file test-state-kraft.json
	$(MAKE) test-env-down
	@echo "\n=== Testing SASL/SCRAM authentication ==="
	$(MAKE) test-env-up-sasl
	./kcp scan clusters --source-type osk --credentials-file test/credentials/osk-credentials-sasl.yaml --state-file test-state-sasl.json
	$(MAKE) test-env-down
	@echo "\n=== Testing TLS/mTLS authentication ==="
	$(MAKE) test-env-up-tls
	./kcp scan clusters --source-type osk --credentials-file test/credentials/osk-credentials-tls.yaml --state-file test-state-tls.json
	$(MAKE) test-env-down
	@echo "\n✅ All environment tests passed!"

