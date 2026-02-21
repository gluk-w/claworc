# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

OpenClaw Orchestrator (Claworc) manages multiple OpenClaw instances in Kubernetes or Docker.
Each instance runs in its own pod and allows users easy access to a Google Chrome and terminal instances
for collaboration with the agent.

The project consists of the following components:
* Control Plane (Golang backend and React frontend) with dashboard, Chrome, Terminal, Logs and other useful stuff.
* Agent image with OpenClaw installed.
* Helm chart for deployment to Kubernetes.

## Repository Structure

- `control-plane/` - Main application (Go backend + React frontend)
  - `main.go` - Entry point, Chi router, embedded SPA serving
  - `internal/` - Go packages (config, database, handlers, middleware, orchestrator)
  - `frontend/` - React TypeScript frontend (npm/Vite)
  - `Dockerfile` - Multi-stage build (Node frontend + Go backend)
- `agent/` - Bot instance Docker image (Ubuntu 24.04, systemd init, dual VNC)
- `helm/` - Helm chart for deploying the dashboard to Kubernetes
- `docs/` - Detailed specs (architecture, API, data model, UI, features)

## Architecture

**Backend** (`control-plane/main.go`): Go Chi router with graceful shutdown. Initializes SQLite (GORM) and orchestrator (Docker or K8s). The built React SPA is embedded into the binary using Go's `embed` package and served via SPA middleware for client-side routing.

**API routes**: All under `/api/v1/`. Instance CRUD at `/api/v1/instances`, settings at `/api/v1/settings`, health at `/health`. Logs are streamed via SSE. WebSocket proxying for chat and VNC.

**K8s integration** (`internal/orchestrator/kubernetes.go`): Uses the official Go `client-go` library. Tries in-cluster config first, falls back to kubeconfig for local dev.

**Docker integration** (`internal/orchestrator/docker.go`): Alternative orchestrator backend using the Docker API for local development.

**Per-instance K8s resources**: Each bot instance creates 8 resources: Deployment, Service (NodePort), 4 PVCs (clawdbot, homebrew, clawd, chrome), Secret (API keys), ConfigMap (clawdbot.json). All named with `bot-{name}` prefix in the `claworc` namespace.

**Crypto** (`internal/crypto/crypto.go`): API keys encrypted at rest in SQLite using Fernet. The Fernet key is auto-generated on first run and stored in the `settings` table.

**Frontend**: React 18 + TypeScript + Vite + TailwindCSS v4. Uses TanStack React Query for data fetching (5s polling on instance list), React Router for SPA routing, Monaco Editor for JSON config editing, Axios for API calls. The `@` import alias maps to `src/`.

**SSH connectivity** (`internal/sshkeys/`, `internal/sshmanager/`, `internal/sshtunnel/`, `internal/sshfiles/`, `internal/sshlogs/`, `internal/sshterminal/`, `internal/sshaudit/`): All control-plane-to-agent communication uses SSH. The control plane is the SSH client; each agent runs sshd. VNC and Gateway access use SSH port-forwarding tunnels. File ops, logs, and terminal use SSH exec/PTY sessions directly. ED25519 key-per-instance with automatic rotation. See `docs/architecture/ssh-connectivity.md`.

**Agent image**: Ubuntu 24.04 with systemd as PID 1 running services using systemd.

## Configuration

Backend settings use `envconfig` with `CLAWORC_` env prefix (see `internal/config/config.go`):
- `CLAWORC_DATABASE_PATH` - SQLite file path (default: `/app/data/claworc.db`)
- `CLAWORC_K8S_NAMESPACE` - Target namespace (default: `claworc`)
## Key Conventions

- K8s-safe instance names are derived from display names: lowercase, hyphens, prefixed with `bot-`, max 63 chars
- API keys are never returned in full by the API -- only masked (`****` + last 4 chars)
- Instance status in API responses is enriched with live K8s/Docker status, not just the DB value
- Config updates (clawdbot.json) trigger automatic pod restarts
- Global API key changes propagate to all instances without overrides
- Frontend is embedded into the Go binary at build time using `//go:embed`
