# Skating - Cross-platform build
BINARY := skating
VERSION := 0.1.0
LDFLAGS := -ldflags="-X main.version=$(VERSION)"
BUILD_DIR := build

.PHONY: all clean install build-all build-linux build-darwin build-windows test

all: build-all

# Build for all platforms
build-all: build-linux build-darwin build-windows

build-linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/skating/
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/skating/

build-darwin:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-amd64 ./cmd/skating/
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-darwin-arm64 ./cmd/skating/

build-windows:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe ./cmd/skating/

# Build for current platform
build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/skating/

# Install for current platform
install: build
	@echo "Installing skating to ~/.skating/bin/ ..."
	@mkdir -p $$HOME/.skating/bin
	cp $(BUILD_DIR)/$(BINARY) $$HOME/.skating/bin/$(BINARY)
	@echo "Add the following to your shell profile:"
	@echo '  export PATH="$$HOME/.skating/bin:$$PATH"'
	@echo "Or run: ln -sf $$HOME/.skating/bin/skating /usr/local/bin/skating"

clean:
	rm -rf $(BUILD_DIR)

test:
	go test ./...