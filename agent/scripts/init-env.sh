#!/bin/bash
tr '\0' '\n' < /proc/1/environ | grep -E '^(VNC_|DISPLAY_)' > /etc/default/vnc
tr '\0' '\n' < /proc/1/environ | grep -E '^OPENCLAW_' > /etc/default/openclaw

# Initialize openclaw config on first run (PVC empty after mount)
OPENCLAW_CONFIG="/home/claworc/.openclaw/openclaw.json"
if [ ! -f "$OPENCLAW_CONFIG" ]; then
    su - claworc -c "/usr/local/bin/openclaw config set gateway.mode local"
    su - claworc -c "/usr/local/bin/openclaw config set gateway.bind lan"
    su - claworc -c "/usr/local/bin/openclaw config set gateway.controlUi.dangerouslyDisableDeviceAuth true"
    su - claworc -c '/usr/local/bin/openclaw config set browser "$(cat /opt/browser.json)" --json'
    chown claworc:claworc /home/claworc/.openclaw
    chmod 700 /home/claworc/.openclaw
fi
