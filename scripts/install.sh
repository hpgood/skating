#!/bin/bash
# Skating CLI Installer for Linux / macOS
set -e

VERSION="${1:-0.1.0}"
INSTALL_DIR="$HOME/.skating/bin"
BINARY="skating"

# Detect OS and ARCH
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    arm64)   ARCH="arm64" ;;
    *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

TARBALL="${BINARY}-${OS}-${ARCH}"
URL="https://github.com/hpgood/skating/releases/download/v${VERSION}/${TARBALL}"

echo "Installing skating v${VERSION} for ${OS}/${ARCH}..."
mkdir -p "$INSTALL_DIR"

# Download binary (fallback to local build if URL unavailable)
if command -v curl &> /dev/null; then
    curl -fsSL "$URL" -o "$INSTALL_DIR/$BINARY" 2>/dev/null || {
        echo "Download failed, building from source..."
        go build -o "$INSTALL_DIR/$BINARY" ./cmd/skating/
    }
elif command -v wget &> /dev/null; then
    wget -q "$URL" -O "$INSTALL_DIR/$BINARY" 2>/dev/null || {
        echo "Download failed, building from source..."
        go build -o "$INSTALL_DIR/$BINARY" ./cmd/skating/
    }
else
    echo "Building from source..."
    go build -o "$INSTALL_DIR/$BINARY" ./cmd/skating/
fi

chmod +x "$INSTALL_DIR/$BINARY"

# Update PATH
SHELL_PROFILE=""
case "$SHELL" in
    */bash) SHELL_PROFILE="$HOME/.bashrc" ;;
    */zsh)  SHELL_PROFILE="$HOME/.zshrc" ;;
    *)      SHELL_PROFILE="$HOME/.profile" ;;
esac

if ! grep -q ".skating/bin" "$SHELL_PROFILE" 2>/dev/null; then
    echo 'export PATH="$HOME/.skating/bin:$PATH"' >> "$SHELL_PROFILE"
    echo "Added skating to PATH in $SHELL_PROFILE"
fi

echo "skating v$VERSION installed to $INSTALL_DIR/$BINARY"
echo "Run 'source $SHELL_PROFILE' or restart your shell, then try: skating --version"