#!/bin/bash
set -e

# Start virtual display for non-headless mode
# Some bot-detection systems can detect --headless flag
Xvfb :99 -screen 0 1920x1080x24 -nolisten tcp -ac &
sleep 1

# Build args
ARGS="--serve --binary ./browser/camoufox --port 8443"

if [ -n "$VULPINE_API_KEY" ]; then
    ARGS="$ARGS --api-key $VULPINE_API_KEY"
fi

if [ -n "$VULPINE_TLS_CERT" ] && [ -n "$VULPINE_TLS_KEY" ]; then
    ARGS="$ARGS --tls-cert $VULPINE_TLS_CERT --tls-key $VULPINE_TLS_KEY"
fi

# Pass any additional args from docker run
exec ./vulpineos $ARGS "$@"
