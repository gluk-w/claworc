# Claworc

## Project Overview

OpenClaw Orchestrator (Claworc) manages multiple AI agent instances in Kubernetes or Docker — OpenClaw by
default, plus Hermes, NanoClaw, or any custom image implementing the agent shim contract (`docs/shim.md`).
Each instance runs in its own container/pod and allows users easy access to a Chromium browser & terminal 
for collaboration with the agent.

The project consists of the following components:
* Control Plane (Golang backend and React frontend) with dashboard, VNC client for Chromium, Terminal, Logs and other useful stuff.
* Agent images (`claworc/openclaw`, `claworc/hermes`, `claworc/nanoclaw`, plus a copy-me template). Compatible with both ARM64 and AMD64 architectures.
* Helm chart for deployment to Kubernetes.

## Repository Structure

- `agent/` - Agent docker images (`agent/openclaw`, `agent/hermes`, `agent/nanoclaw`, `agent/template`) and browser images `claworc/<browser>-browser`
- `control-plane/` - Main application (Go backend + React frontend)
    - `main.go` - Entry point, Chi router, embedded SPA serving
    - `internal/` - Go packages (config, database, handlers, middleware, orchestrator, sshproxy, sshterminal)
    - `frontend/` - React TypeScript frontend (npm/Vite)
    - `Dockerfile` - Multi-stage build (Node frontend + Go backend)
- `helm/` - Helm chart for deploying the dashboard to Kubernetes
- `website/` - Landing page for claworc.com
- `docs/` - Detailed internal specs (architecture, API, data model, UI, features)

## Architecture

**Backend** (`control-plane/main.go`): Go Chi router with graceful shutdown. Initializes SQLite (GORM) and orchestrator 
(Docker or K8s). The built React SPA is embedded into the binary using Go's `embed` package and served via 
SPA middleware for client-side routing.

**API routes**: All under `/api/v1/`. Instance CRUD at `/api/v1/instances`, settings at `/api/v1/settings`, 
health at `/health`. Logs are streamed via SSE. WebSocket proxying for chat and VNC.

**LLM Gateway**: Proxy for LLM requests that replaces virtual keys with real, globally configured API tokens. It
records statistics in a separate SQLite database. See`docs/virtual-keys.md`.

**Agent Shim** (`internal/agentshim/`): The universal interface between the control plane and the AI agent
running inside an instance container (OpenClaw, Hermes, NanoClaw, custom). All agent-specific knowledge —
chat protocol, config paths, LLM provider config, restart — lives behind the `Client`/`Session` interfaces.
Two adapters: `shimexec/` speaks the exec-based shim contract (`docs/shim.md`, scripts at `/opt/claworc/shim/`
inside the image, invoked over SSH), and `openclawnative/` drives pre-shim OpenClaw images via their gateway
WebSocket + CLI. The factory prefers the shim when the image ships it and falls back to native for legacy
OpenClaw images. Chat, webhooks, config editing, and virtual-key routing all go through this layer. The
agent-type registry (`registry.go`) drives per-type defaults and UI capability gating. Layering is strict:
handlers → agentshim → sshproxy (transport) → orchestrator (containers).

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
- `CLAWORC_ALLOWED_HOST_MOUNTS` - Comma-separated allowlist of host path prefixes within which shared folders may be backed by a host bind mount. Empty (default) disables host-backed shared folders entirely. See `docs/shared-folders.md`.
- `CLAWORC_WEBHOOK_IDLE_TIMEOUT` - Idle gap the synchronous webhook bridge tolerates between events from OpenClaw before giving up (default: `120s`). The deadline re-arms on every event, so an actively-streaming agent is never cut off; only a genuine stall trips it.
- `CLAWORC_SSH_GATEWAY_ENABLED` / `CLAWORC_SSH_GATEWAY_PORT` / `CLAWORC_SSH_GATEWAY_PUBLIC_HOST` - Inbound SSH gateway (`ssh <user>+<instance>@host`, default port `2222`). See `docs/ssh-gateway.md`.

## Terminology

- **"Agent" (user-facing) = "Instance" (code)**: The UI calls them "Agents" but the backend, database, API paths (`/api/v1/instances`), routes (`/instances/...`), TypeScript types (`Instance`), and Go types all use `Instance`. When editing user-visible strings use "Agent"; when editing code identifiers, types, routes, or API paths keep `Instance`.

## Key Conventions

- K8s-safe instance names are derived from display names: lowercase, hyphens, prefixed with `bot-`, max 63 chars
- API keys are never returned in full by the API -- only masked (`****` + last 4 chars)
- Instance status in API responses is enriched with live K8s/Docker status, not just the DB value
- Global API key changes propagate to all instances without overrides
- Frontend is embedded into the Go binary at build time using `//go:embed`
- SSH connections and tunnels are keyed by instance ID (uint), not name — this ensures stability across 
  renames and avoids name-to-ID mapping overhead
- User Experience is very important - ensure elements are consistently formatted (see `docs/style-guide.md`) and properly labeled. 
