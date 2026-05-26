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

# Install NanoClaw from the maintained upstream source when pnpm is available.
if command -v git >/dev/null 2>&1 && command -v node >/dev/null 2>&1; then
    echo "Installing NanoClaw..."
    if ! command -v pnpm >/dev/null 2>&1 && command -v corepack >/dev/null 2>&1; then
        corepack enable >/dev/null 2>&1 || true
    fi
    if command -v pnpm >/dev/null 2>&1; then
        rm -rf "${HOME}/.vulpineos/nanoclaw-src"
        git clone --depth=1 https://github.com/qwibitai/nanoclaw.git "${HOME}/.vulpineos/nanoclaw-src" >/dev/null 2>&1 || true
        if [ -d "${HOME}/.vulpineos/nanoclaw-src" ]; then
            (cd "${HOME}/.vulpineos/nanoclaw-src" && pnpm install --frozen-lockfile && pnpm build) || true
            mkdir -p "${HOME}/.vulpineos/nanoclaw"
            cat > "${HOME}/.vulpineos/nanoclaw/nanoclaw" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

SRC="${VULPINE_NANOCLAW_SRC:-${HOME}/.vulpineos/nanoclaw-src}"
PROFILE="${VULPINE_NANOCLAW_HOME:-${HOME}/.vulpineos/nanoclaw}"
mkdir -p "$PROFILE"
cd "$PROFILE"

run_tsx() {
    local tsx_bin="${SRC}/node_modules/.bin/tsx"
    if [ -x "$tsx_bin" ]; then
        exec "$tsx_bin" "$@"
    fi
    exec pnpm --dir "$SRC" exec tsx "$@"
}

if [ "${1:-}" = "run" ]; then
    shift
    if [ -f "${SRC}/dist/index.js" ]; then
        exec node "${SRC}/dist/index.js" "$@"
    fi
    run_tsx "${SRC}/src/index.ts" "$@"
fi

if [ -f "${SRC}/dist/cli/client.js" ]; then
    exec node "${SRC}/dist/cli/client.js" "$@"
fi
run_tsx "${SRC}/src/cli/client.ts" "$@"
EOF
            chmod 0700 "${HOME}/.vulpineos/nanoclaw/nanoclaw"
        fi
    else
        echo "Skipping NanoClaw install: pnpm not found."
    fi
fi

# Build
echo "Building vulpineos..."
go build -o vulpineos ./cmd/vulpineos

echo ""
echo "Installation complete! Run ./vulpineos to start."
echo ""
