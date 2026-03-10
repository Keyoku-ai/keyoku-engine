.PHONY: build test clean install all cross

# Binary name and paths
BINARY := keyoku-server
CMD_DIR := ./cmd/keyoku-server
BIN_DIR := ./bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)

# Default target
all: test build

# Build for current platform
build:
	go build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY) $(CMD_DIR)

# Run all tests
test:
	go test ./...

# Run tests with verbose output
test-v:
	go test -v ./...

# Run tests with race detector
test-race:
	go test -race ./...

# Run benchmarks
bench:
	go test -bench=. -benchmem ./...

# Cross-compile for all release targets
cross:
	GOOS=darwin  GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-darwin-arm64  $(CMD_DIR)
	GOOS=darwin  GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-darwin-amd64  $(CMD_DIR)
	GOOS=linux   GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-linux-arm64   $(CMD_DIR)
	GOOS=linux   GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY)-linux-amd64   $(CMD_DIR)

# Clean build artifacts
clean:
	rm -rf $(BIN_DIR)

# Install to GOPATH/bin
install:
	go install -ldflags="$(LDFLAGS)" $(CMD_DIR)

# Lint (requires golangci-lint)
lint:
	golangci-lint run ./...

# Check compilation without producing binaries
check:
	go build ./...
	go vet ./...
