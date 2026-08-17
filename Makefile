# Skating - Cross-platform build
BINARY := skating
VERSION ?= 0.2.0
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE := $(shell date -u +%Y-%m-%d 2>/dev/null || echo "unknown")
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)
BUILD_DIR := build

.PHONY: all clean install build-all build-linux build-darwin build-windows test

all: build-all

# Build for all platforms
build-all: build-linux build-darwin build-windows

build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/skating/
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/skating/

build-darwin:
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 ./cmd/skating/
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 ./cmd/skating/

build-windows:
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe ./cmd/skating/
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY)-windows-arm64.exe ./cmd/skating/

# Build for current platform
build:
	go build -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) ./cmd/skating/

# Install for current platform
install: build
	@echo "Installing skating to ~/.skating/bin/ ..."
	@mkdir -p $$HOME/.skating/bin
	cp $(BUILD_DIR)/$(BINARY) $$HOME/.skating/bin/$(BINARY)
	@echo "Add the following to your shell profile:"
	@echo '  export PATH="$$HOME/.skating/bin:$$PATH"'

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...