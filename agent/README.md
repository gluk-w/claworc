# OpenClaw Agent Image

Docker image that provides a ready-to-use OpenClaw environment with a browser accessible via VNC.

## What's Inside

- **Debian Bookworm** minimal with s6-overlay v3 as PID 1
- **Chromium** with DevTools Protocol enabled for OpenClaw browser automation
- **OpenClaw** gateway running as an s6-overlay service
- **VNC access** via TigerVNC + noVNC (websockify bridge)
- **Openbox** window manager
- **SSH server** for remote access and port forwarding
- **Dev tools**: Node.js 22, Python 3, Poetry, Git

## Architecture

All services are managed by s6-overlay:

| Service        | Port  | Description                    |
|----------------|-------|--------------------------------|
| sshd           | 22    | SSH server for remote access   |
| svc-openclaw   | 18789 | OpenClaw gateway               |
| svc-xvnc       | 5900  | TigerVNC X server              |
| svc-novnc      | 3000  | noVNC websockify bridge        |
| svc-desktop    | -     | Openbox + Chromium             |

## Persistent Data

The entire `/home/claworc` directory is a single persistent volume containing:
- `.openclaw/` - OpenClaw configuration
- `chrome-data/` - Chromium user data and CDP

Homebrew lives at `/home/linuxbrew/.linuxbrew` (separate volume).

## Architectures

Supports **AMD64** and **ARM64** platforms.
