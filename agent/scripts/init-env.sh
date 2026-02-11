#!/bin/bash
tr '\0' '\n' < /proc/1/environ | grep -E '^(VNC_|DISPLAY_)' > /etc/default/vnc
tr '\0' '\n' < /proc/1/environ | grep -E '^OPENCLAW_' > /etc/default/openclaw


# Initialize openclaw config on first run (PVC empty after mount)
if [ ! -d "/home/claworc/.claworc" ]; then
    # Clean up stale config keys from older OpenClaw versions
    su - claworc -c '/usr/local/bin/openclaw doctor --fix' 2>/dev/null || true

    su - claworc -c "/usr/local/bin/openclaw config set gateway.mode local"
    su - claworc -c "/usr/local/bin/openclaw config set gateway.bind lan"
    su - claworc -c "/usr/local/bin/openclaw config set gateway.controlUi.dangerouslyDisableDeviceAuth true"
    # Always apply browser config from image (ensures CDP URL stays current)
    su - claworc -c '/usr/local/bin/openclaw config set browser "$(cat /opt/browser.json)" --json'
    
    chown claworc:claworc /home/claworc/.openclaw
    chmod 700 /home/claworc/.openclaw
    mkdir -p /home/claworc/.claworc
    chown claworc:claworc /home/claworc/.claworc
fi
