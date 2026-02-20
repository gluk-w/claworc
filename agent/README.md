# OpenClaw Agent Image

Docker image that provides a ready-to-use OpenClaw environment with a browser accessible via VNC.

## What's Inside

- **Ubuntu 24.04** desktop (XFCE) with s6-overlay as PID 1
- **Chromium** with DevTools Protocol enabled for OpenClaw browser automation
- **OpenClaw** gateway running as an s6-overlay service
- **claworc-proxy** binary providing mTLS tunnel listener (port 3001), Neko VNC, terminal PTY, file browser, and log streaming
- **Dev tools**: Node.js 22, Python 3, Poetry, Git

## Architecture

All services are managed by s6-overlay. The `claworc-proxy` binary listens on port 3001 for mTLS tunnel connections from the control plane and multiplexes all traffic over yamux streams:

| Channel      | Service                          | Protocol  |
|--------------|----------------------------------|-----------|
| `neko`       | Neko VNC (desktop access)        | HTTP/WS   |
| `terminal`   | PTY terminal sessions            | WebSocket |
| `files`      | File browser                     | HTTP      |
| `logs`       | Log streaming                    | SSE       |
| `chat`       | OpenClaw chat relay              | WebSocket |
| `control`    | OpenClaw control relay           | WebSocket |
| `ping`       | Health check                     | TCP       |

## Architectures

Supports **AMD64** and **ARM64** platforms.
