# Development Guide

This guide covers local development setup, common commands, and environment configuration for Claworc.

## Local Development (without Kubernetes)

```bash
# Install all dependencies
make install-dev

# Run both backend and frontend dev servers
make dev

# Stop development servers
make dev-stop
```

The dashboard will be available at http://localhost:5173 (frontend proxies API calls to backend on port 8000).

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
- `CLAWORC_NODE_IP` - Node IP for VNC URLs (default: `192.168.1.104`)
- `CLAWORC_PORT_START` / `CLAWORC_PORT_END` - Port range (default: 30100-30199)

## Production Deployment

```bash
# Build and push Docker images
make dashboard-build dashboard-push
make agent-build agent-push

# Deploy to Kubernetes
make helm-install

# Upgrade existing deployment
make helm-upgrade
```
