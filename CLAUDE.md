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

**SSH Proxy** (`internal/sshproxy/`): Unified package consolidating SSH key management, connection management, tunnel management, health monitoring, automatic reconnection, connection state tracking, and connection event logging. Contains eight files: `keys.go` (ED25519 key pair generation/persistence), `manager.go` (SSHManager — one multiplexed SSH connection per instance), `tunnel.go` (TunnelManager — reverse SSH tunnels over managed connections), `health.go` (connection health monitoring with metrics), `reconnect.go` (automatic reconnection with exponential backoff and connection state events), `tunnel_health.go` (TCP-level tunnel health monitoring with per-tunnel metrics), `state.go` (per-instance connection state machine with transition history), and `events.go` (per-instance connection event logging with ring buffer). All connections and tunnels are keyed by database instance ID (`uint`), not by name, so they remain stable across instance renames. TunnelManager depends on SSHManager for connections; a background reconciliation loop ensures tunnels stay healthy. A background health checker runs every 30s, executing `echo ping` over SSH to verify end-to-end command execution (complementing protocol-level keepalives). Per-connection metrics track connected-at time, last health check, and success/failure counts. A separate tunnel health checker runs every 60s, probing each tunnel's local TCP port to verify the listener is alive; failed tunnels are marked as "error" for the reconciliation loop to recreate. Per-tunnel metrics (`TunnelMetrics`) track creation time, last health check, success/failure counts; per-instance reconnection counts track how many times tunnels have been recreated. When a health check or keepalive fails, automatic reconnection is triggered with exponential backoff (1s → 2s → 4s → 8s → 16s cap, up to 10 retries). Each reconnection attempt re-uploads the global public key via `ConfigureSSHAccess` before connecting (the agent container may have restarted, losing `/root/.ssh/authorized_keys`). Connection events (`connected`, `disconnected`, `reconnecting`, `reconnected`, `reconnect_failed`, `key_uploaded`, `health_check_failed`) are emitted to registered `EventListener` callbacks for observability and automatically recorded in a per-instance ring buffer (100 entries) for debugging, accessible via `GetEventHistory(instanceID)` and the `GET /api/v1/instances/{id}/ssh-events` endpoint. Per-instance connection state (`ConnectionState`: Disconnected, Connecting, Connected, Reconnecting, Failed) is tracked automatically by the SSHManager lifecycle methods (Connect, Close, keepalive, reconnect). State transitions are recorded in a per-instance ring buffer (50 entries) for debugging, accessible via `GetStateTransitions(instanceID)`. `StateChangeCallback` functions can be registered via `OnStateChange` for UI updates or alerting.

**SSH Key Rotation** (`internal/sshkeys/`): Global SSH key pair rotation. `RotateGlobalKeyPair` generates a new ED25519 key pair and safely rotates it across all running instances in a multi-step process: (1) generate new key, (2) append new public key to each instance's `authorized_keys` via orchestrator exec (both keys work temporarily), (3) back up old keys on disk (`ssh_key.old`, `ssh_key.pub.old`), (4) write new keys to disk, (5) reload into SSHManager via `ReloadKeys`, (6) test SSH connectivity with new key per instance, (7) remove old key from instances where new key works (via `ConfigureSSHAccess` overwrite), (8) remove backup files on full success. Partial failures are handled gracefully — instances where the new key fails retain the old key. Instance updates run concurrently. Settings `ssh_key_rotation_policy_days` (default: 90) and `ssh_key_last_rotation` (timestamp) are stored in the settings table. The `RotationOrchestrator` interface requires `ConfigureSSHAccess`, `GetSSHAddress`, and `ExecInInstance`.

**SSH Audit** (`internal/sshaudit/`): Persistent SSH access audit logging backed by a dedicated `ssh_audit_logs` SQLite table. The `Auditor` records seven event types: `connection`, `disconnection`, `command_exec`, `file_operation`, `terminal_session`, `key_upload`, and `key_rotation`. Each `AuditEntry` stores event type, instance ID, user, details, and timestamp. The auditor is initialized in `main.go` with `NewAuditor(db, retentionDays)` which auto-migrates its table. An SSHManager `EventListener` callback automatically logs connection lifecycle events (connect, disconnect, key upload). File operation handlers log browse/read/download/create/mkdir/upload. Terminal handlers log session start/reconnect/detach. Key rotation handlers log both manual and automatic rotations. `Query(opts)` supports filtering by instance ID and event type with pagination (newest first). `PurgeOlderThan(d)` deletes old entries; `StartRetentionCleanup(ctx)` runs daily purge based on `ssh_audit_retention_days` setting (default 90 days). Admin-only API: `GET /api/v1/audit-logs` with `instance_id`, `event_type`, `limit`, `offset` query parameters.

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
