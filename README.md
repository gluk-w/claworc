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

### Local Development (without Kubernetes)

```bash
# Install all dependencies
make install-dev

# Run both backend and frontend dev servers
make dev

# Stop development servers
make dev-stop
```

The dashboard will be available at http://localhost:5173 (frontend proxies API calls to backend on port 8000).

### Production Deployment

```bash
# Build and push Docker images
make dashboard-build dashboard-push
make agent-build agent-push

# Deploy to Kubernetes
make helm-install

# Upgrade existing deployment
make helm-upgrade
```

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

## Development Commands

### Local Development (from repo root)
```bash
make install-dev    # Install all dependencies (Poetry + npm)
make dev            # Run backend + frontend dev servers
make dev-stop       # Stop all dev servers
```

### Backend (from `dashboard/`)
```bash
poetry install                                          # Install dependencies
poetry run uvicorn backend.app:app --reload --port 8000 # Run dev server
```

### Frontend (from `dashboard/frontend/`)
```bash
npm install      # Install dependencies
npm run dev      # Vite dev server
npm run build    # Production build
```

### Docker & Kubernetes (from repo root)
```bash
make dashboard-build    # Build dashboard image
make dashboard-push     # Push to registry
make agent-build        # Build agent (bot instance) image
make agent-push         # Push agent image
make helm-install       # Install Helm chart
make helm-upgrade       # Upgrade deployment
make helm-template      # Render templates (debug)
```

## Environment Variables

All settings use the `CLAWORC_` prefix:
- `CLAWORC_DATABASE_PATH` - SQLite file path (default: `/app/data/claworc.db`)
- `CLAWORC_K8S_NAMESPACE` - Kubernetes namespace (default: `claworc`)
- `CLAWORC_K8S_NODE_IP` - Node IP for VNC URLs (default: `192.168.1.104`)
- `CLAWORC_CONTAINER_IMAGE` - Bot container image
- `CLAWORC_NODEPORT_START` / `CLAWORC_NODEPORT_END` - Port range (default: 30100-30199)

## Target Environment

- MicroK8s cluster on single server (192.168.1.104)
- Internal network tool - no authentication required
- Dashboard deployed in `claworc` namespace
- Supports up to 50 concurrent bot instances (NodePort pairs 30100-30199)
