# Claworc

## Project Overview

OpenClaw Orchestrator (Claworc) manages multiple OpenClaw instances in Kubernetes or Docker.
Each instance runs in its own container/pod and allows users easy access to a Chromium browser & terminal 
for collaboration with the agent.

The project consists of the following components:
* Control Plane (Golang backend and React frontend) with dashboard, VNC client for Chromium, Terminal, Logs and other useful stuff.
* Agent image with OpenClaw installed. It is compatible with both ARM64 and AMD64 architectures.
* Helm chart for deployment to Kubernetes.

## Repository Structure

- `agent/` - Base docker image with OpenClaw instance (`claworc/openclaw`) and images with various browsers `claworc/<browser>-browser`
- `control-plane/` - Main application (Go backend + React frontend)
    - `main.go` - Entry point, Chi router, embedded SPA serving
    - `internal/` - Go packages (config, database, handlers, middleware, orchestrator, sshproxy, sshterminal)
    - `frontend/` - React TypeScript frontend (npm/Vite)
    - `Dockerfile` - Multi-stage build (Node frontend + Go backend)
- `helm/` - Helm chart for deploying the dashboard to Kubernetes
- `website/` - Landing page for claworc.com
- `website_docs/` - End-user documentation powered by Mintlify. It is automatically deployed to claworc.com/docs
- `docs/` - Detailed internal specs (architecture, API, data model, UI, features)

## Architecture

**Backend** (`control-plane/main.go`): Go Chi router with graceful shutdown. Initializes SQLite (GORM) and orchestrator 
(Docker or K8s). The built React SPA is embedded into the binary using Go's `embed` package and served via 
SPA middleware for client-side routing.

**API routes**: All under `/api/v1/`. Instance CRUD at `/api/v1/instances`, settings at `/api/v1/settings`, 
health at `/health`. Logs are streamed via SSE. WebSocket proxying for chat and VNC.

**LLM Gateway**: Proxy for LLM requests that replaces virtual keys with real, globally configured API tokens. It
records statistics in a separate SQLite database. See`docs/virtual-keys.md`.

**Orchestrator** (`internal/orchestrator/`): Thin abstraction over the underlying container runtime
(Kubernetes or Docker). Its job is generic container primitives only — instance lifecycle, exec, file
streaming, SSH address, resource updates, image updates, volume cloning. It does NOT own browser-pod,
terminal, or other feature-specific orchestration; those live in their own packages
(e.g. `browserprov/` for the on-demand browser pod) and depend on the orchestrator only through small
purpose-specific backend interfaces.

**K8s integration** (`internal/orchestrator/kubernetes.go`): Uses the official Go `client-go` library. 
Tries in-cluster config first, falls back to kubeconfig for local dev.

**Docker integration** (`internal/orchestrator/docker.go`): Alternative orchestrator backend using the Docker API 
for local development.

**Crypto** (`internal/crypto/crypto.go`): API keys encrypted at rest in SQLite using Fernet. The Fernet key is 
auto-generated on first run and stored in the `settings` table.

**Database migrations** (`internal/database/migrations/`): Goose v3 invoked as a library, embedded into the binary, applied at startup from `database.Init()`. New migrations are versioned Go files in the `migrations` subpackage that use the GORM Migrator interface; model types live in `internal/database/models/` and are re-exported by the `database` package via type aliases for backward compat. See `docs/migrations.md` for the full spec, including the `make migration` workflow that delegates to the `migration-author` subagent.

**SSH Proxy** (`internal/sshproxy/`): Unified package consolidating SSH key management, connection management, 
tunnel management, health monitoring, automatic reconnection, connection state tracking, and connection event logging. 

**SSH Audit** (`internal/sshaudit/`): Persistent SSH access audit logging backed by a dedicated `ssh_audit_logs` SQLite table.

**SSH Terminal** (`internal/sshterminal/`): Interactive terminal sessions over SSH with session persistence. 
`SessionManager` tracks multiple concurrent sessions per instance, each identified by UUID. 
Sessions survive WebSocket disconnect (detached state) and can be reconnected via `?session_id=` query parameter. 
A ring-buffer scrollback captures recent output for replay on reconnect.

**Frontend**: React 18 + TypeScript + Vite + TailwindCSS v4. Uses TanStack React Query for data fetching 
(5s polling on instance list), React Router for SPA routing, Monaco Editor for JSON config editing, 
Axios for API calls. The `@` import alias maps to `src/`.


## Configuration

Backend settings use `envconfig` with `CLAWORC_` env prefix (see `internal/config/config.go`):
- `CLAWORC_DATA_PATH` - Data directory for SQLite database and SSH keys (default: `/app/data`)
- `CLAWORC_BACKUPS_PATH` - Directory for backup archives (default: empty, falls back to `<DATA_PATH>/backups`)
- `CLAWORC_K8S_NAMESPACE` - Target namespace (default: `claworc`)
- `CLAWORC_TERMINAL_HISTORY_LINES` - Scrollback buffer size in lines (default: `1000`, `0` to disable)
- `CLAWORC_TERMINAL_RECORDING_DIR` - Directory for audit recordings (default: empty, disabled)
- `CLAWORC_TERMINAL_SESSION_TIMEOUT` - Idle detached session timeout (default: `30m`)

## Key Conventions

- K8s-safe instance names are derived from display names: lowercase, hyphens, prefixed with `bot-`, max 63 chars
- API keys are never returned in full by the API -- only masked (`****` + last 4 chars)
- Instance status in API responses is enriched with live K8s/Docker status, not just the DB value
- Global API key changes propagate to all instances without overrides
- Frontend is embedded into the Go binary at build time using `//go:embed`
- SSH connections and tunnels are keyed by instance ID (uint), not name — this ensures stability across 
  renames and avoids name-to-ID mapping overhead
- User Experience is very important - ensure elements are consistently formatted (see `docs/style-guide.md`) and properly labeled. 
