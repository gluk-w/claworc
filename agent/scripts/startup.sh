#!/bin/bash
set -e

# Initialize OpenClaw configuration if not present
if [ ! -d "/home/headless/.claworc" ]; then
    echo "Initializing OpenClaw configuration..."

    # Fix potential config issues
    openclaw doctor --fix 2>/dev/null || true

    openclaw config set gateway.mode local
    openclaw config set gateway.bind lan
    openclaw config set gateway.controlUi.dangerouslyDisableDeviceAuth true

    # Apply browser config if present
    if [ -f "/opt/browser.json" ]; then
        openclaw config set browser "$(cat /opt/browser.json)" --json
    fi

    # Create necessary directories
    mkdir -p /home/headless/.claworc
    mkdir -p /home/headless/.openclaw
fi

# Set gateway token if provided
if [ -n "$OPENCLAW_GATEWAY_TOKEN" ]; then
    openclaw config set gateway.auth.token "$OPENCLAW_GATEWAY_TOKEN"
fi

# Start OpenClaw Gateway in background
echo "Starting OpenClaw Gateway..."
openclaw gateway &

# Execute the original entrypoint (VNC startup)
echo "Starting VNC/Xfce environment..."
exec /dockerstartup/vnc_startup.sh "$@"
