#!/bin/bash
# Bundle OpenClaw + Node.js runtime into the VulpineOS distribution.
# Usage: ./scripts/bundle-openclaw.sh [--platform linux|macos]
#
# Creates an openclaw/ directory alongside the vulpineos binary containing:
#   openclaw/node              — Standalone Node.js binary
#   openclaw/node_modules/     — OpenClaw + dependencies
#   openclaw/package.json      — npm project for OpenClaw

set -e

PLATFORM="${1:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
ARCH="$(uname -m)"
NODE_VERSION="24"
OPENCLAW_VERSION="latest"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="$SCRIPT_DIR/../openclaw"

echo "VulpineOS OpenClaw Bundler"
echo "========================="
echo "Platform: $PLATFORM / $ARCH"
echo "Output:   $OUT_DIR"
echo ""

# Normalize platform/arch for Node.js download
case "$PLATFORM" in
    darwin|macos) NODE_PLATFORM="darwin" ;;
    linux*)       NODE_PLATFORM="linux" ;;
    *)            echo "Unsupported platform: $PLATFORM"; exit 1 ;;
esac

case "$ARCH" in
    x86_64|amd64) NODE_ARCH="x64" ;;
    arm64|aarch64) NODE_ARCH="arm64" ;;
    *)             echo "Unsupported arch: $ARCH"; exit 1 ;;
esac

mkdir -p "$OUT_DIR"

# Step 1: Download standalone Node.js binary
echo "[1/3] Downloading Node.js $NODE_VERSION ($NODE_PLATFORM-$NODE_ARCH)..."
NODE_URL="https://nodejs.org/dist/latest-v${NODE_VERSION}.x/node-v${NODE_VERSION}.0.0-${NODE_PLATFORM}-${NODE_ARCH}.tar.gz"
# Use the index to find the actual latest version
NODE_INDEX=$(curl -sL "https://nodejs.org/dist/latest-v${NODE_VERSION}.x/" | grep -oE "node-v[0-9]+\.[0-9]+\.[0-9]+-${NODE_PLATFORM}-${NODE_ARCH}\.tar\.(gz|xz)" | head -1)
if [ -z "$NODE_INDEX" ]; then
    echo "Could not find Node.js $NODE_VERSION for $NODE_PLATFORM-$NODE_ARCH"
    echo "Trying direct download..."
fi

TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

if [ -n "$NODE_INDEX" ]; then
    curl -sL "https://nodejs.org/dist/latest-v${NODE_VERSION}.x/$NODE_INDEX" | tar xz -C "$TEMP_DIR" --strip-components=1
else
    # Fallback: try to use system node
    if command -v node &>/dev/null; then
        echo "Using system Node.js: $(node --version)"
        cp "$(command -v node)" "$OUT_DIR/node"
    else
        echo "Error: Cannot find Node.js. Install it first: brew install node"
        exit 1
    fi
fi

# Copy node binary
if [ -f "$TEMP_DIR/bin/node" ]; then
    cp "$TEMP_DIR/bin/node" "$OUT_DIR/node"
    chmod +x "$OUT_DIR/node"
    echo "  Node.js binary: $("$OUT_DIR/node" --version)"
elif [ -f "$OUT_DIR/node" ]; then
    echo "  Node.js binary: $("$OUT_DIR/node" --version)"
fi

# We also need npm temporarily for installation
NPM_DIR=""
if [ -f "$TEMP_DIR/bin/npm" ]; then
    NPM_DIR="$TEMP_DIR"
fi

# Step 2: Install OpenClaw
echo "[2/3] Installing OpenClaw ($OPENCLAW_VERSION)..."
cd "$OUT_DIR"

# Create a minimal package.json
cat > package.json <<'PKGJSON'
{
  "name": "vulpineos-openclaw",
  "version": "1.0.0",
  "private": true,
  "dependencies": {
    "openclaw": "latest"
  }
}
PKGJSON

# Use the bundled npm or system npm
if [ -n "$NPM_DIR" ]; then
    PATH="$NPM_DIR/bin:$PATH" npm install --production 2>&1 | tail -5
else
    npm install --production 2>&1 | tail -5
fi

echo "  OpenClaw installed"

# Step 3: Create start wrapper
echo "[3/3] Creating start script..."
cat > start.sh <<'STARTSH'
#!/bin/bash
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
export NODE_PATH="$SCRIPT_DIR/node_modules"
exec "$SCRIPT_DIR/node" "$SCRIPT_DIR/node_modules/.bin/openclaw" "$@"
STARTSH
chmod +x start.sh

echo ""
echo "Done! OpenClaw bundled at: $OUT_DIR"
echo "  Node:     $OUT_DIR/node"
echo "  OpenClaw: $OUT_DIR/node_modules/openclaw"
echo "  Start:    $OUT_DIR/start.sh"
echo ""
echo "Size: $(du -sh "$OUT_DIR" | cut -f1)"
