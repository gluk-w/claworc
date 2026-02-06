# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Openclaw Orchestrator (Claworc) is a Kubernetes dashboard for managing multiple OpenClaw bot instances. 
Each instance runs in its own pod and allows users easy access to a Google Chrome and terminal instances
for collaboration with the agent.

## Repository Structure

- `dashboard/` - Main application to control OpenClaw instances 
  - `backend/` - Python FastAPI backend (Poetry-managed)
  - `frontend/` - React TypeScript frontend (npm/Vite)
- `agent/` - Bot instance Docker image (Ubuntu 24.04, systemd init, dual VNC)
- `helm/` - Helm chart for deploying the dashboard to Kubernetes
- `docs/` - Detailed specs (architecture, API, data model, UI, features)

## Development Commands

### Backend (from `dashboard/`)
```bash
poetry install                          # Install Python dependencies
poetry run uvicorn backend.app:app --reload --port 8000   # Run backend dev server
```

### Frontend (from `dashboard/frontend/`)
```bash
npm install                             # Install JS dependencies
npm run dev                             # Vite dev server (proxies /api to localhost:8000)
npm run build                           # Production build to dist/
```

### Docker & Deployment (from repo root)
```bash
make dashboard-build                    # Build dashboard Docker image
make dashboard-push                     # Push to registry
make agent-build                        # Build agent (bot instance) Docker image
make agent-push                         # Push agent image
make helm-install                       # Install Helm chart to cluster
make helm-upgrade                       # Upgrade existing release
make helm-template                      # Render templates locally (debug)
```

The Makefile expects a `../kubeconfig` file. Override with `make helm-install KUBECONFIG=--kubeconfig /path/to/config`.

## Architecture

**Backend** (`backend/app.py`): FastAPI app with lifespan that initializes SQLite (aiosqlite) and K8s client. Serves the built React SPA via static files + SPA middleware for client-side routing. Routes are organized under `backend/routes/`.

**API routes**: All under `/api/v1/`. Instance CRUD at `/api/v1/instances`, settings at `/api/v1/settings`, health at `/health`. Logs are streamed via SSE (`sse-starlette`).

**K8s integration** (`backend/kubernetes.py`): Uses the official Python `kubernetes` client. All K8s API calls are wrapped with `asyncio.to_thread()` since the client is synchronous. Tries in-cluster config first, falls back to kubeconfig for local dev.

**Per-instance K8s resources**: Each bot instance creates 8 resources: Deployment, Service (NodePort), 4 PVCs (clawdbot, homebrew, clawd, chrome), Secret (API keys), ConfigMap (clawdbot.json). All named with `bot-{name}` prefix in the `claworc` namespace.

**NodePort allocation**: Consecutive even/odd pairs from 30100-30199 (max 50 instances). Even port = Chrome VNC, odd port = Terminal VNC. Allocated from SQLite, lowest available first.

**Crypto** (`backend/crypto.py`): API keys encrypted at rest in SQLite using Fernet. The Fernet key is auto-generated on first run and stored in the `settings` table.

**Frontend**: React 18 + TypeScript + Vite + TailwindCSS v4. Uses TanStack React Query for data fetching (5s polling on instance list), React Router for SPA routing, Monaco Editor for JSON config editing, Axios for API calls. The `@` import alias maps to `src/`.

**Agent image**: Ubuntu 24.04 with systemd as PID 1 running 7 services across two isolated X displays. Requires privileged security context. Chrome runs in kiosk mode on DISPLAY=:1, xterm on DISPLAY=:2, each with their own TigerVNC + noVNC pair.

## Configuration

Backend settings use `pydantic-settings` with `CLAWORC_` env prefix (see `backend/config.py`):
- `CLAWORC_DATABASE_PATH` - SQLite file path (default: `/app/data/claworc.db`)
- `CLAWORC_K8S_NAMESPACE` - Target namespace (default: `claworc`)
- `CLAWORC_K8S_NODE_IP` - Node IP for VNC URLs (default: `192.168.1.104`)
- `CLAWORC_CONTAINER_IMAGE` - Bot container image
- `CLAWORC_NODEPORT_START` / `CLAWORC_NODEPORT_END` - Port range

## Key Conventions

- Use Poetry for all Python dependency management
- K8s-safe instance names are derived from display names: lowercase, hyphens, prefixed with `bot-`, max 63 chars
- API keys are never returned in full by the API -- only masked (`****` + last 4 chars)
- Instance status in API responses is enriched with live K8s pod status, not just the DB value
- Config updates (clawdbot.json) trigger automatic pod restarts
- Global API key changes propagate to all instances without overrides
