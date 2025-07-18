# Simple Makefile for kcp
BINARY_NAME=kcp
MAIN_PATH=./cmd/cli

.PHONY: build clean help install fmt test test-cov test-cov-ui build-linux build-linux-arm64 build-darwin build-darwin-arm64 build-all

# Build the binary
build:
	go build -o $(BINARY_NAME) $(MAIN_PATH)

# Individual platform builds
build-linux:
	GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME)-linux-amd64 $(MAIN_PATH)

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build -o $(BINARY_NAME)-linux-arm64 $(MAIN_PATH)

build-darwin:
	GOOS=darwin GOARCH=amd64 go build -o $(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build -o $(BINARY_NAME)-darwin-arm64 $(MAIN_PATH)

# Build for all platforms and architectures
build-all:
	GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME)-linux-amd64 $(MAIN_PATH)
	GOOS=linux GOARCH=arm64 go build -o $(BINARY_NAME)-linux-arm64 $(MAIN_PATH)
	GOOS=darwin GOARCH=amd64 go build -o $(BINARY_NAME)-darwin-amd64 $(MAIN_PATH)
	GOOS=darwin GOARCH=arm64 go build -o $(BINARY_NAME)-darwin-arm64 $(MAIN_PATH)

# Clean build artifacts
clean:
	rm -f $(BINARY_NAME) coverage.out

# Show available commands
help:
	@echo "🛠️  KCP - Available Commands:"
	@echo "=========================================="
	@echo ""
	@echo "📦 build              - Build the binary for current platform"
	@echo "📦 build-linux        - Build for Linux amd64"
	@echo "📦 build-linux-arm64  - Build for Linux arm64"
	@echo "📦 build-darwin       - Build for macOS amd64 (Intel)"
	@echo "📦 build-darwin-arm64 - Build for macOS arm64 (Apple Silicon)"
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

# Run tests
test:
	@echo "🧪 Running tests..."
	@echo "=================="
	@go test -v ./... || (echo ""; echo "❌ TESTS FAILED - See failures above"; echo "=========================================="; exit 1)
	@echo ""
	@echo "✅ All tests passed!"

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

