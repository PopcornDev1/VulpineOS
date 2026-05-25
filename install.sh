#!/bin/bash
set -e

echo "Installing VulpineOS..."

# Check for required tools
command -v go >/dev/null 2>&1 || { echo "Error: Go is required but not installed. Install from https://go.dev/dl/" >&2; exit 1; }

# Clone if not already in a vulpineos directory
if [ ! -f "go.mod" ]; then
    echo "Cloning VulpineOS..."
    git clone https://github.com/VulpineOS/VulpineOS.git
    cd VulpineOS
fi

# Install NanoClaw from the maintained upstream source when pnpm/corepack is available.
if command -v git >/dev/null 2>&1 && command -v corepack >/dev/null 2>&1; then
    echo "Installing NanoClaw..."
    corepack enable >/dev/null 2>&1 || true
    if command -v pnpm >/dev/null 2>&1; then
        rm -rf "${HOME}/.vulpineos/nanoclaw-src"
        git clone --depth=1 https://github.com/qwibitai/nanoclaw.git "${HOME}/.vulpineos/nanoclaw-src" >/dev/null 2>&1 || true
        if [ -d "${HOME}/.vulpineos/nanoclaw-src" ]; then
            (cd "${HOME}/.vulpineos/nanoclaw-src" && pnpm install --frozen-lockfile) || true
        fi
    fi
fi

# Build
echo "Building vulpineos..."
go build -o vulpineos ./cmd/vulpineos

echo ""
echo "Installation complete! Run ./vulpineos to start."
echo ""
