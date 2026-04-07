# Simple Makefile for kcp
BINARY_NAME := kcp
MAIN_PATH := .

COMMIT := $(shell git rev-parse HEAD 2>/dev/null || echo "unknown")
DATE := $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
VERSION := 0.0.0-localdev
LD_FLAGS :=	-X github.com/confluentinc/kcp/internal/build_info.Version=$(VERSION) \
			-X github.com/confluentinc/kcp/internal/build_info.Commit=$(COMMIT) \
			-X github.com/confluentinc/kcp/internal/build_info.Date=$(DATE)

.PHONY: build clean help install fmt test test-cov test-cov-ui build-linux build-linux-arm64 build-darwin build-darwin-arm64 build-windows build-all build-frontend lint pre-commit-install e2e-setup e2e-teardown ci-e2e-tests e2e

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
	@echo "🔍 lint               - Run Go linters (golangci-lint)"
	@echo "🔗 pre-commit-install - Install git pre-commit hooks"
	@echo ""
	@echo "🔬 E2E Tests:"
	@echo "🔄 e2e               - Run full E2E lifecycle (setup → test → teardown)"
	@echo "🏗️  e2e-setup          - Set up Minikube + CFK infrastructure"
	@echo "🧪 ci-e2e-tests       - Run E2E migration tests"
	@echo "🧹 e2e-teardown       - Tear down E2E infrastructure"
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

# Run Go linters
lint:
	golangci-lint run --config .golangci.yml ./...

# Install git pre-commit hooks
pre-commit-install:
	git config --local core.hooksPath .githooks

# Run tests
test: build-frontend
	@echo "🧪 Running tests..."
	@echo "=================="
	@bash -c 'go test -timeout 15m -v ./...; exit_code=$$?; echo ""; if [ $$exit_code -ne 0 ]; then echo "❌ Tests failed with exit code $$exit_code"; else echo "✅ All tests passed!"; fi; exit $$exit_code'

# Run tests with coverage - beautiful terminal output
test-cov:
	@echo "🧪 Running tests with coverage analysis..."
	@echo "=========================================="
	@go test -timeout 15m -coverprofile=coverage.out ./...
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

# Run full E2E lifecycle: setup, test, teardown (teardown always runs)
e2e: e2e-setup
	@trap 'echo ""; echo "🧹 Tearing down E2E test infrastructure..."; bash test/e2e/migration/testdata/teardown.sh' EXIT; \
	echo "🧪 Running E2E tests..."; \
	echo "======================"; \
	bash -c 'go test -v -tags=e2e -timeout 15m ./test/e2e/...; exit_code=$$?; echo ""; if [ $$exit_code -ne 0 ]; then echo "❌ E2E tests failed"; else echo "✅ E2E tests passed!"; fi; exit $$exit_code'

# E2E test infrastructure setup (Minikube + CFK + CP clusters)
e2e-setup:
	@echo "🏗️  Setting up E2E test infrastructure..."
	@bash test/e2e/migration/testdata/setup.sh

# E2E test infrastructure teardown
e2e-teardown:
	@echo "🧹 Tearing down E2E test infrastructure..."
	@bash test/e2e/migration/testdata/teardown.sh

# Run E2E tests (requires infrastructure from e2e-setup)
ci-e2e-tests:
	@echo "🧪 Running E2E tests..."
	@echo "======================"
	@bash -c 'go test -v -tags=e2e -timeout 15m ./test/e2e/...; exit_code=$$?; echo ""; if [ $$exit_code -ne 0 ]; then echo "❌ E2E tests failed"; else echo "✅ E2E tests passed!"; fi; exit $$exit_code'

