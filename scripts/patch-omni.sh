#!/bin/bash
# Patches an existing Camoufox.app with VulpineOS Juggler modifications.
# Usage: ./scripts/patch-omni.sh /path/to/Camoufox.app
#
# This replaces the JS files inside omni.ja without needing a full Firefox rebuild.
# NOTE: Action-Lock (Phase 2) requires a C++ rebuild and cannot be patched this way.

set -e

CAMOUFOX_APP="${1:-$HOME/Downloads/Camoufox.app}"
VULPINEOS_DIR="$(cd "$(dirname "$0")/.." && pwd)"
OMNI_PATH="$CAMOUFOX_APP/Contents/Resources/omni.ja"
BROWSER_OMNI_PATH="$CAMOUFOX_APP/Contents/Resources/browser/omni.ja"
SETTINGS_DIR="$CAMOUFOX_APP/Contents/Resources"

if [ ! -f "$OMNI_PATH" ]; then
    echo "Error: omni.ja not found at $OMNI_PATH"
    echo "Usage: $0 /path/to/Camoufox.app"
    exit 1
fi

echo "VulpineOS Patcher"
echo "================="
echo "Target: $CAMOUFOX_APP"
echo ""

# Backup original
if [ ! -f "$OMNI_PATH.bak" ]; then
    echo "[1/5] Backing up original omni.ja..."
    cp "$OMNI_PATH" "$OMNI_PATH.bak"
else
    echo "[1/5] Backup already exists, skipping."
fi

# Create temp working directory
WORK_DIR=$(mktemp -d)
trap "rm -rf $WORK_DIR" EXIT

echo "[2/5] Extracting omni.ja..."
cd "$WORK_DIR"
unzip -q "$OMNI_PATH" -d omni

echo "[3/5] Replacing Juggler files with VulpineOS versions..."

# Core modified files
JUGGLER_SRC="$VULPINEOS_DIR/additions/juggler"
JUGGLER_DST="$WORK_DIR/omni/chrome/juggler/content"

# PageAgent.js (Phases 1, 3 + telemetry alerts)
cp "$JUGGLER_SRC/content/PageAgent.js" "$JUGGLER_DST/content/PageAgent.js"
echo "  - content/PageAgent.js (injection filter, optimized DOM, role map)"

# main.js (Action-Lock handler — will be no-op without C++ patch)
cp "$JUGGLER_SRC/content/main.js" "$JUGGLER_DST/content/main.js"
echo "  - content/main.js (action-lock handler stub)"

# Protocol.js (all new methods/events)
cp "$JUGGLER_SRC/protocol/Protocol.js" "$JUGGLER_DST/protocol/Protocol.js"
echo "  - protocol/Protocol.js (new methods + events)"

# PageHandler.js (routing for new methods)
cp "$JUGGLER_SRC/protocol/PageHandler.js" "$JUGGLER_DST/protocol/PageHandler.js"
echo "  - protocol/PageHandler.js (injection alerts, optimized DOM, action-lock)"

# BrowserHandler.js (telemetry + trust warming handlers)
cp "$JUGGLER_SRC/protocol/BrowserHandler.js" "$JUGGLER_DST/protocol/BrowserHandler.js"
echo "  - protocol/BrowserHandler.js (telemetry, trust warming)"

# TargetRegistry.js (action-lock method on PageTarget)
cp "$JUGGLER_SRC/TargetRegistry.js" "$JUGGLER_DST/TargetRegistry.js"
echo "  - TargetRegistry.js (setActionLock)"

# NEW files — TrustWarmService and TelemetryService
cp "$JUGGLER_SRC/TrustWarmService.js" "$JUGGLER_DST/TrustWarmService.js"
echo "  - TrustWarmService.js (NEW — autonomous trust warming)"

cp "$JUGGLER_SRC/TelemetryService.js" "$JUGGLER_DST/TelemetryService.js"
echo "  - TelemetryService.js (NEW — engine telemetry)"

echo "[4/5] Repacking omni.ja..."
cd "$WORK_DIR/omni"
# Firefox requires omni.ja to be stored (not compressed) for mmap
zip -q -0 -r "$WORK_DIR/omni-new.ja" .
cp "$WORK_DIR/omni-new.ja" "$OMNI_PATH"

echo "[5/5] Patching settings..."
# Copy VulpineOS preferences
cp "$VULPINEOS_DIR/settings/camoufox.cfg" "$SETTINGS_DIR/camoufox.cfg"
echo "  - camoufox.cfg (VulpineOS preferences)"

echo ""
echo "Done! VulpineOS Juggler patches applied to $CAMOUFOX_APP"
echo ""
echo "To test:"
echo "  1. Build the Go TUI:  cd $VULPINEOS_DIR && go build -o vulpineos ./cmd/vulpineos/"
echo "  2. Run with the patched browser:"
echo "     ./vulpineos --binary '$CAMOUFOX_APP/Contents/MacOS/camoufox'"
echo "  3. Or run demo mode (no browser):  ./vulpineos --no-browser"
echo ""
echo "To restore original: cp '$OMNI_PATH.bak' '$OMNI_PATH'"
echo ""
echo "NOTE: Phase 2 (Action-Lock) requires a full C++ rebuild."
echo "      The setActionLock protocol method will be available but"
echo "      docShell.suspendPage()/resumePage() won't exist without the patch."
