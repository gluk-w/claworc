# Claworc - OpenClaw Orchestrator

Claworc is a Kubernetes dashboard for managing multiple OpenClaw bot instances. Each instance runs in its own pod with isolated Google Chrome and terminal sessions accessible via VNC, enabling teams to manage 5-20 concurrent bot instances from a single interface.

## What is OpenClaw?

OpenClaw (clawdbot/moltbot) is a WhatsApp gateway CLI built on Baileys with a Pi RPC agent. Each bot instance includes:
- Dual VNC sessions (Chrome kiosk + xterm terminal)
- Google Chrome with full profile persistence
- Node.js runtime with clawdbot npm package
- Python toolchain (pip, pipx, poetry, uv)
- Homebrew package manager

## Quick Start

```bash
# Local development
make install-dev && make dev
```

The dashboard will be available at http://localhost:5173.

For detailed development setup, deployment instructions, and environment configuration, see [docs/development.md](docs/development.md).

## Documentation

- [Features](docs/features.md) - Feature specifications and user workflows
- [Architecture](docs/architecture.md) - System architecture and design decisions
- [API](docs/api.md) - REST API endpoints and request/response formats
- [Data Model](docs/data-model.md) - Database schema and Kubernetes resource model
- [UI](docs/ui.md) - Frontend pages, components, and interaction patterns

## Tech Stack

- **Backend**: Python, FastAPI, SQLite, Kubernetes Python client
- **Frontend**: React, TypeScript, Vite, TailwindCSS v4
- **Infrastructure**: Kubernetes (MicroK8s)
- **Container Images**: Ubuntu 24.04 with systemd, dual VNC sessions
- **Dependency Management**: Poetry (Python), npm (JavaScript)

## Target Environment

- MicroK8s cluster on single server (192.168.1.104)
- Internal network tool - no authentication required
- Dashboard deployed in `claworc` namespace
- Supports up to 50 concurrent bot instances (NodePort pairs 30100-30199)
