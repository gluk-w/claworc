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
- `CLAWORC_DATA_PATH` - Data directory for SQLite database and SSH keys (default: `/app/data`)
- `CLAWORC_K8S_NAMESPACE` - Kubernetes namespace (default: `claworc`)
- `CLAWORC_NODE_IP` - Node IP for VNC URLs (default: `192.168.1.104`)
- `CLAWORC_PORT_START` / `CLAWORC_PORT_END` - Port range (default: 30100-30199)

## SSH Proxy Package (`internal/sshproxy`)

The `sshproxy` package consolidates SSH key management, connection management, and tunnel management into a single package. It replaces the former `sshkeys`, `sshmanager`, and `sshtunnel` packages.

### Package Structure

| File | Responsibility |
|------|---------------|
| `keys.go` | ED25519 key pair generation, persistence, and loading |
| `manager.go` | `SSHManager` — one multiplexed SSH connection per instance |
| `tunnel.go` | `TunnelManager` — reverse SSH tunnels over managed connections |

### ID-Based Architecture

All connections and tunnels are keyed by **database instance ID** (`uint`), not by instance name (`string`). This ensures that connections and tunnels remain valid even if the instance display name changes, and avoids name-to-ID mapping overhead.

### Key Types

- **`SSHManager`**: Holds the global SSH key pair and maintains a map of active connections (`map[uint]*managedConn`). Provides `EnsureConnected` as the main entry point for obtaining a connection.
- **`TunnelManager`**: Creates reverse SSH tunnels over connections from `SSHManager`. Maintains a map of active tunnels (`map[uint][]*ActiveTunnel`). Runs a background reconciliation loop to keep tunnels healthy.
- **`Orchestrator`**: Interface that `SSHManager.EnsureConnected` uses to upload public keys and resolve SSH addresses. Implemented by both the Kubernetes and Docker orchestrators.

### Initialization (from `main.go`)

```go
import "github.com/gluk-w/claworc/control-plane/internal/sshproxy"

// 1. Ensure the SSH key pair exists (generates on first run)
sshSigner, sshPublicKey, err := sshproxy.EnsureKeyPair(config.Cfg.DataPath)

// 2. Create the SSH connection manager
sshMgr := sshproxy.NewSSHManager(sshSigner, sshPublicKey)

// 3. Create the tunnel manager (depends on SSHManager)
tunnelMgr := sshproxy.NewTunnelManager(sshMgr)

// 4. Start background reconciliation (maintains tunnels for running instances)
tunnelMgr.StartBackgroundManager(ctx, listRunningInstances, orch)
```

### Common Operations

**Establish tunnels for an instance** (connection is created on-demand):
```go
err := tunnelMgr.StartTunnelsForInstance(ctx, instanceID, orch)
```

**Get the local port for a VNC or Gateway tunnel:**
```go
vncPort := tunnelMgr.GetVNCLocalPort(instanceID)       // 0 if not found
gwPort  := tunnelMgr.GetGatewayLocalPort(instanceID)   // 0 if not found
```

**Check if an SSH connection is alive:**
```go
alive := sshMgr.IsConnected(instanceID)
```

**Stop tunnels and close connections for an instance:**
```go
tunnelMgr.StopTunnelsForInstance(instanceID)
sshMgr.Close(instanceID)
```

**Shutdown (close everything):**
```go
tunnelMgr.StopAll()
sshMgr.CloseAll()
```

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
