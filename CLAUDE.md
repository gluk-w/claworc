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
  - `internal/` - Go packages (config, database, handlers, middleware, orchestrator, sshproxy, sshterminal)
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

**SSH Proxy** (`internal/sshproxy/`): Unified package consolidating SSH key management, connection management, tunnel management, health monitoring, and automatic reconnection. Contains five files: `keys.go` (ED25519 key pair generation/persistence), `manager.go` (SSHManager — one multiplexed SSH connection per instance), `tunnel.go` (TunnelManager — reverse SSH tunnels over managed connections), `health.go` (connection health monitoring with metrics), and `reconnect.go` (automatic reconnection with exponential backoff and connection state events). All connections and tunnels are keyed by database instance ID (`uint`), not by name, so they remain stable across instance renames. TunnelManager depends on SSHManager for connections; a background reconciliation loop ensures tunnels stay healthy. A background health checker runs every 30s, executing `echo ping` over SSH to verify end-to-end command execution (complementing protocol-level keepalives). Per-connection metrics track connected-at time, last health check, and success/failure counts. When a health check or keepalive fails, automatic reconnection is triggered with exponential backoff (1s → 2s → 4s → 8s → 16s cap, up to 10 retries). Each reconnection attempt re-uploads the global public key via `ConfigureSSHAccess` before connecting (the agent container may have restarted, losing `/root/.ssh/authorized_keys`). Connection state change events (`connected`, `disconnected`, `reconnecting`, `reconnect_failed`, `key_uploaded`) are emitted to registered `EventListener` callbacks for observability.

**SSH Terminal** (`internal/sshterminal/`): Interactive terminal sessions over SSH with session persistence. `SessionManager` tracks multiple concurrent sessions per instance, each identified by UUID. Sessions survive WebSocket disconnect (detached state) and can be reconnected via `?session_id=` query parameter. A ring-buffer scrollback captures recent output for replay on reconnect. Optional audit recording writes all session output to timestamped files. Idle detached sessions are reaped after a configurable timeout.

**Frontend**: React 18 + TypeScript + Vite + TailwindCSS v4. Uses TanStack React Query for data fetching (5s polling on instance list), React Router for SPA routing, Monaco Editor for JSON config editing, Axios for API calls. The `@` import alias maps to `src/`.

**Agent image**: Ubuntu 24.04 with systemd as PID 1 running services using systemd.

## Configuration

Backend settings use `envconfig` with `CLAWORC_` env prefix (see `internal/config/config.go`):
- `CLAWORC_DATA_PATH` - Data directory for SQLite database and SSH keys (default: `/app/data`)
- `CLAWORC_K8S_NAMESPACE` - Target namespace (default: `claworc`)
- `CLAWORC_TERMINAL_HISTORY_LINES` - Scrollback buffer size in lines (default: `1000`, `0` to disable)
- `CLAWORC_TERMINAL_RECORDING_DIR` - Directory for audit recordings (default: empty, disabled)
- `CLAWORC_TERMINAL_SESSION_TIMEOUT` - Idle detached session timeout (default: `30m`)
## Key Conventions

- K8s-safe instance names are derived from display names: lowercase, hyphens, prefixed with `bot-`, max 63 chars
- API keys are never returned in full by the API -- only masked (`****` + last 4 chars)
- Instance status in API responses is enriched with live K8s/Docker status, not just the DB value
- Config updates (clawdbot.json) trigger automatic pod restarts
- Global API key changes propagate to all instances without overrides
- Frontend is embedded into the Go binary at build time using `//go:embed`
- SSH connections and tunnels are keyed by instance ID (uint), not name — this ensures stability across renames and avoids name-to-ID mapping overhead
