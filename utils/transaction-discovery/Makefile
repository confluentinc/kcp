BINARY := txn-discovery
PKG    := ./cmd/txn-discovery

.PHONY: build test tidy fmt vet clean dist

## build: static binary for the current platform (no cgo)
build:
	CGO_ENABLED=0 go build -o bin/$(BINARY) $(PKG)

## test: run unit tests
test:
	go test ./...

## tidy: resolve and pin dependencies
tidy:
	go mod tidy

## fmt: gofmt the tree
fmt:
	gofmt -s -w .

## vet: go vet
vet:
	go vet ./...

## dist: cross-compiled static binaries to hand to a customer
dist:
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -o dist/$(BINARY)-linux-amd64       $(PKG)
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -o dist/$(BINARY)-linux-arm64       $(PKG)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -o dist/$(BINARY)-darwin-arm64      $(PKG)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/$(BINARY)-windows-amd64.exe $(PKG)

## clean: remove build artifacts
clean:
	rm -rf bin dist
