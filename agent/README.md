# OpenClaw Agent Image

Docker image that provides a ready-to-use OpenClaw environment with a browser accessible via VNC.

## What's Inside

- **Ubuntu 24.04** desktop (XFCE) with s6-overlay as PID 1
- **Chromium** with DevTools Protocol enabled for OpenClaw browser automation
- **OpenClaw** gateway running as an s6-overlay service
- **VNC access** via TigerVNC + noVNC (websockify bridge)
- **SSH server** for remote access and port forwarding
- **Dev tools**: Node.js 22, Python 3, Poetry, Git

## Architecture

All services are managed by s6-overlay:

| Service        | Port  | Description                    |
|----------------|-------|--------------------------------|
| sshd           | 22    | SSH server for remote access   |
| svc-openclaw   | 18789 | OpenClaw gateway               |
| noVNC          | 3000  | Browser-based VNC access       |

## Architectures

Supports **AMD64** and **ARM64** platforms.
