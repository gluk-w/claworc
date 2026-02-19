# OpenClaw Agent Image

Docker image that provides a ready-to-use OpenClaw environment with a browser accessible via VNC.

## What's Inside

- **Ubuntu 24.04** desktop (XFCE) with s6-overlay as PID 1
- **Chromium** with DevTools Protocol enabled for OpenClaw browser automation
- **OpenClaw** gateway running as an s6-overlay service
- **nginx** reverse proxy exposing a single port (3000)
- **VNC access** via TigerVNC + noVNC (websockify bridge)
- **Dev tools**: Node.js 22, Python 3, Poetry, Git

## Architecture

All services are managed by s6-overlay and sit behind an nginx reverse proxy on port 3000:

| Path           | Backend                 | Protocol  |
|----------------|-------------------------|-----------|
| `/`            | noVNC static files      | HTTP      |
| `/websockify`  | websockify :5900        | WebSocket |
| `/gateway`     | openclaw gateway :18789 | WebSocket |

## Architectures

Supports **AMD64** and **ARM64** platforms.
