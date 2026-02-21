# SSH Connectivity Architecture

## Overview

Claworc uses SSH as the primary connectivity layer between the control plane and agent instances. Every interaction — terminal sessions, file operations, log streaming, Chrome/VNC access — flows through SSH tunnels established from the control plane to each agent's SSH server. This replaces the earlier model of direct NodePort/WebSocket access with a secure, centrally managed connection model.

## Single Global Key Pair Design

Claworc uses **one ED25519 key pair** for all SSH connectivity. The key pair is stored as files in the data directory alongside the SQLite database:

```
{CLAWORC_DATA_PATH}/          # default: /app/data
├── claworc.db                 # SQLite database
├── ssh_key                    # ED25519 private key (mode 0600)
└── ssh_key.pub                # ED25519 public key (mode 0644)
```

**Key generation flow:**

1. On first startup, `sshproxy.EnsureKeyPair()` checks if `ssh_key` and `ssh_key.pub` exist
2. If missing, a new ED25519 key pair is generated and written to disk
3. The private key is parsed into an `ssh.Signer` for use in SSH client connections
4. The public key is stored as a string in OpenSSH `authorized_keys` format

The same key pair authenticates the control plane with every agent instance. The public key is uploaded on-demand to each instance's `/root/.ssh/authorized_keys` before the first connection.

## Architecture Diagram

```
┌──────────────────────────────────────────────────────────────────┐
│                         Control Plane                            │
│                                                                  │
│  ┌─────────────┐   ┌──────────────┐   ┌───────────────────────┐ │
│  │  SSHManager  │──▶│ TunnelManager │──▶│ Background Reconciler │ │
│  │  (per-inst   │   │ (per-inst     │   │ (60s loop)            │ │
│  │   SSH conn)  │   │  SSH tunnels) │   └───────────────────────┘ │
│  └──────┬───────┘   └──────┬────────┘                            │
│         │                  │                                      │
│  ┌──────┴───────┐   ┌──────┴────────┐                            │
│  │ Health       │   │ Tunnel Health  │                            │
│  │ Checker (30s)│   │ Checker (60s)  │                            │
│  └──────────────┘   └───────────────┘                            │
│                                                                  │
│  SSH Key: {data_dir}/ssh_key (ED25519)                           │
└──────────────────┬───────────────────────────────────────────────┘
                   │ SSH (port 22)
                   │ One multiplexed connection per instance
                   ▼
┌──────────────────────────────────────────────────────────────────┐
│                     Agent Instance (Pod)                          │
│                                                                  │
│  ┌────────┐                                                      │
│  │ sshd   │◀── /root/.ssh/authorized_keys (control plane pubkey) │
│  │ :22    │                                                      │
│  └───┬────┘                                                      │
│      │                                                           │
│      ├── SSH tunnel → port 3000 (Selkies/VNC)                    │
│      ├── SSH tunnel → port 8080 (OpenClaw gateway)               │
│      ├── SSH session → terminal (interactive shell)              │
│      ├── SSH exec   → file operations (ls, cat, write)           │
│      └── SSH exec   → log streaming (tail -F)                   │
└──────────────────────────────────────────────────────────────────┘
```

## Bidirectional Tunnel Architecture

SSH tunnels forward traffic from the control plane to services inside the agent container. Tunnels are created as **local forwards** (the control plane listens on a local port that maps to a remote port on the agent):

| Tunnel | Local Port | Remote Port | Service |
|--------|-----------|-------------|---------|
| VNC    | OS-assigned | 3000 | Selkies (Chrome remote desktop) |
| Gateway | OS-assigned | 8080 | OpenClaw gateway (chat, API) |

The control plane's HTTP handlers proxy browser requests through these tunnels. The browser never connects directly to agent instances — all traffic flows through the control plane's SSH tunnels.

**Tunnel lifecycle:**

1. When an instance starts, the background reconciler detects it needs tunnels
2. `StartTunnelsForInstance()` creates a local TCP listener for each tunnel
3. Each accepted connection is forwarded through an SSH channel to the remote port
4. Bidirectional `io.Copy` relays data between the local socket and SSH channel
5. When an instance stops, the reconciler tears down its tunnels

## On-Demand Key Upload Flow

The control plane uploads its public key to an instance before every new SSH connection. This handles the case where an agent container has restarted and lost its `/root/.ssh/authorized_keys`:

1. **Upload public key**: The orchestrator (K8s or Docker) executes a command inside the container to write the public key to `/root/.ssh/authorized_keys`
2. **Establish SSH connection**: `ssh.Dial()` connects to the agent's SSH server using the private key
3. **Create tunnels**: `TunnelManager.StartTunnelsForInstance()` sets up port forwards
4. **Start health monitoring**: Keepalives and health checks begin immediately

This "upload then connect" pattern runs on every connection attempt, including reconnections. It ensures that even if the agent container was replaced (e.g., pod restart, deployment update), the control plane can always authenticate.

## SSH Components and Responsibilities

### Core Components

| Component | File(s) | Responsibility |
|-----------|---------|----------------|
| **Key Management** | `sshproxy/keys.go` | ED25519 key pair generation, persistence, loading |
| **SSHManager** | `sshproxy/manager.go` | One multiplexed SSH connection per instance, connection lifecycle |
| **TunnelManager** | `sshproxy/tunnel.go` | SSH tunnel creation, background reconciliation loop |
| **Health Checker** | `sshproxy/health.go` | SSH-level health monitoring (30s, `echo ping` command) |
| **Tunnel Health** | `sshproxy/tunnel_health.go` | TCP-level tunnel probing (60s, port connectivity check) |
| **Reconnection** | `sshproxy/reconnect.go` | Automatic reconnection with exponential backoff |
| **State Machine** | `sshproxy/state.go` | Per-instance connection state tracking and transitions |
| **Event System** | `sshproxy/events.go` | Per-instance connection event logging (ring buffer) |
| **Rate Limiter** | `sshproxy/ratelimit.go` | Connection attempt rate limiting and failure blocking |
| **IP Restrictions** | `sshproxy/iprestrict.go` | Per-instance source IP whitelisting |
| **File Operations** | `sshproxy/files.go` | Remote file read/write/mkdir/ls over SSH exec |
| **Log Streaming** | `sshproxy/logs.go` | Remote log tailing via SSH exec (`tail -F`) |

### Supporting Components

| Component | Package | Responsibility |
|-----------|---------|----------------|
| **Key Rotation** | `sshkeys/rotation.go` | Safe multi-step global key rotation across all instances |
| **Key Verification** | `sshkeys/verify.go` | SHA256 fingerprint calculation for display |
| **Audit Logging** | `sshaudit/audit.go` | SQLite-backed persistent event logging with retention |
| **Terminal Sessions** | `sshterminal/session_manager.go` | Persistent terminal sessions with scrollback and reconnect |

## Security Model

### Authentication

- **Key-based only**: Password authentication is disabled on agents; only ED25519 public key authentication is accepted
- **Single key pair**: One global key authenticates with all instances, simplifying management
- **Key rotation**: Keys can be rotated via API with zero-downtime multi-step process (see [Key Rotation](#key-rotation))

### Network Security

- **No direct access**: Agent SSH ports are not exposed externally; only the control plane connects
- **Source IP restrictions**: Optional per-instance whitelist of allowed source IPs/CIDRs for the control plane's outbound connection
- **Rate limiting**: Two-tier protection prevents connection storms:
  - Sliding window: max 10 connection attempts per minute per instance
  - Consecutive failure blocking: escalating blocks from 30s to 5 minutes

### Agent Hardening

- SSH server runs with hardened configuration:
  - `PasswordAuthentication no`
  - `PermitRootLogin prohibit-password`
  - `MaxAuthTries 3`
  - `LoginGraceTime 30`
  - `X11Forwarding no`
  - `AllowAgentForwarding no`
  - `AllowTcpForwarding yes` (required for tunnels)

### Audit Trail

- All SSH events (connections, disconnections, file operations, terminal sessions, key uploads, key rotations) are logged to a SQLite `ssh_audit_logs` table
- Configurable retention (default 90 days) with automatic daily purge
- Queryable via `GET /api/v1/audit-logs` (admin only)

### Threat Mitigation

| Threat | Mitigation |
|--------|------------|
| Stolen private key | Key stored with mode 0600; rotation available; key lives only on control plane |
| Agent impersonation | Host key checking (future); agents only accessible from control plane network |
| Connection storm / DDoS | Rate limiter with sliding window + escalating block-on-failure |
| Unauthorized access from wrong network | Per-instance source IP restrictions with CIDR support |
| Stale credentials after compromise | Key rotation rotates across all instances atomically |
| Audit gap | Persistent SQLite audit log captures all SSH events; configurable retention |

## Connection Lifecycle

### Connection States

Each instance's SSH connection tracks one of five states:

```
                    ┌──────────────┐
                    │ Disconnected │◀──────────────────┐
                    └──────┬───────┘                   │
                           │ Connect()                  │
                           ▼                           │
                    ┌──────────────┐                   │
                    │  Connecting  │                   │
                    └──────┬───────┘                   │
                           │ success                   │ Close()
                           ▼                           │
                    ┌──────────────┐                   │
              ┌────▶│  Connected   │───────────────────┘
              │     └──────┬───────┘
              │            │ keepalive/health fail
              │            ▼
              │     ┌──────────────┐
              │     │ Reconnecting │
              │     └──────┬───────┘
              │            │
              │     success │      all retries fail
              └─────────────┘            │
                                         ▼
                                  ┌──────────┐
                                  │  Failed   │
                                  └──────────┘
```

State transitions are recorded in a per-instance ring buffer (50 entries) and exposed via API for debugging.

### Health Monitoring

Three layers of health checking run concurrently:

1. **SSH keepalive** (30s interval): Protocol-level `keepalive@openssh.com` request sent over the SSH connection. Failure triggers reconnection.

2. **Application health check** (30s interval): Executes `echo ping` as a new SSH session. Verifies the SSH server can accept new sessions and execute commands. Failure closes the connection and triggers reconnection.

3. **Tunnel health check** (60s interval): TCP probe to each tunnel's local port. Verifies the local listener is alive and accepting connections. Failed tunnels are marked "error" for the reconciliation loop to recreate.

### Reconnection

When a connection fails (keepalive timeout, health check failure, or SSH error), automatic reconnection begins:

1. Close the stale connection
2. For each attempt (up to 10 retries):
   a. Re-upload public key to the instance (agent may have restarted)
   b. Fetch the current SSH address from the orchestrator
   c. Attempt SSH connection
   d. On failure, wait with exponential backoff: 1s → 2s → 4s → 8s → 16s (cap)
3. On success: emit `reconnected` event, tunnels recreated by reconciler
4. On failure after all retries: transition to `Failed` state

### Event System

All connection lifecycle events are emitted to registered listeners and stored in per-instance ring buffers (100 entries):

| Event | When |
|-------|------|
| `connected` | SSH connection established |
| `disconnected` | SSH connection lost |
| `reconnecting` | Automatic reconnection starting |
| `reconnected` | Automatic reconnection succeeded |
| `reconnect_failed` | All reconnection retries exhausted |
| `key_uploaded` | Public key uploaded to instance |
| `health_check_failed` | Health check command failed |

Events are accessible via `GET /api/v1/instances/{id}/ssh-events` for debugging.

## Key Rotation

Global key rotation is a safe, multi-step process that avoids service interruption:

1. **Generate new key**: Create new ED25519 key pair in memory
2. **Append new key**: Add new public key to each instance's `authorized_keys` (both old and new keys work)
3. **Backup old key**: Write `ssh_key.old` and `ssh_key.pub.old` to disk
4. **Write new key**: Overwrite `ssh_key` and `ssh_key.pub` with new key pair
5. **Reload in memory**: SSHManager loads new private key via `ReloadKeys()`
6. **Test connectivity**: Verify SSH connection works with new key per instance (concurrent)
7. **Remove old key**: Overwrite `authorized_keys` on instances where new key works (removes old key)
8. **Clean up**: Delete backup files on full success

Partial failures are handled gracefully — instances where the new key fails retain the old key in their `authorized_keys`. Backup files are preserved for operator investigation.

Configuration: `ssh_key_rotation_policy_days` (default 90 days) in the settings table.

## Sequence Diagrams

### Instance Connection Flow

```
Control Plane                            Orchestrator                Agent
     │                                       │                        │
     │  GetSSHAddress(instanceID)             │                        │
     │──────────────────────────────────────▶│                        │
     │  ◀── host:port ──────────────────────│                        │
     │                                       │                        │
     │  ConfigureSSHAccess(instanceID, pubkey)│                        │
     │──────────────────────────────────────▶│                        │
     │                                       │  exec: write pubkey    │
     │                                       │──────────────────────▶│
     │                                       │  ◀── ok ──────────────│
     │  ◀── ok ─────────────────────────────│                        │
     │                                       │                        │
     │  SSH Dial (host:port, ED25519 key)     │                        │
     │────────────────────────────────────────────────────────────────▶│
     │  ◀── SSH handshake ────────────────────────────────────────────│
     │                                       │                        │
     │  Start keepalive goroutine (30s)       │                        │
     │  Emit "connected" event               │                        │
     │  Record ConnectionMetrics             │                        │
     │                                       │                        │
```

### Tunnel Establishment Flow

```
Control Plane                                                   Agent
     │                                                            │
     │  [For each tunnel type: VNC (3000), Gateway (8080)]        │
     │                                                            │
     │  Listen on local TCP port (OS-assigned)                    │
     │  ◀── local listener ready                                  │
     │                                                            │
     │  ... browser request arrives on local port ...             │
     │                                                            │
     │  Accept TCP connection                                     │
     │  SSH client.Dial("tcp", "localhost:{remotePort}")          │
     │────────────────────────────────────────────────────────────▶│
     │  ◀── SSH channel established ──────────────────────────────│
     │                                                            │
     │  io.Copy: local socket ◀══════▶ SSH channel                │
     │                                                            │
```

### Reconnection with Key Re-Upload

```
Control Plane                            Orchestrator                Agent
     │                                       │                        │
     │  [keepalive or health check fails]     │                        │
     │  Close stale connection               │                        │
     │  Emit "disconnected" event            │                        │
     │  Emit "reconnecting" event            │                        │
     │                                       │                        │
     │  ── Attempt 1 (backoff: 1s) ──────────────────────────────────│
     │                                       │                        │
     │  ConfigureSSHAccess(id, pubkey)        │                        │
     │──────────────────────────────────────▶│                        │
     │                                       │  exec: write pubkey    │
     │                                       │──────────────────────▶│
     │                                       │  ◀── ok ──────────────│
     │  Emit "key_uploaded" event            │                        │
     │                                       │                        │
     │  SSH Dial (host:port, ED25519 key)     │                        │
     │────────────────────────────────────────────────────────────────▶│
     │  ◀── SSH handshake ────────────────────────────────────────────│
     │                                       │                        │
     │  Emit "reconnected" event             │                        │
     │                                       │                        │
     │  [If SSH Dial fails → wait 1s, attempt 2 with 2s backoff]     │
     │  [Continues up to 10 attempts, backoff: 1→2→4→8→16s cap]     │
     │  [All fail → transition to "Failed", emit "reconnect_failed"] │
     │                                       │                        │
```

### Global Key Rotation

```
Control Plane                            Orchestrator             Instance A    Instance B
     │                                       │                       │              │
     │  1. Generate new ED25519 key pair     │                       │              │
     │                                       │                       │              │
     │  2. Append new pubkey to all instances │                       │              │
     │──────────────────────────────────────▶│                       │              │
     │                                       │  exec: append pubkey  │              │
     │                                       │──────────────────────▶│              │
     │                                       │──────────────────────────────────────▶│
     │                                       │  ◀── ok ──────────────│              │
     │                                       │  ◀── ok ────────────────────────────│
     │                                       │                       │              │
     │  3. Backup: ssh_key → ssh_key.old     │                       │              │
     │  4. Write new key pair to disk        │                       │              │
     │  5. Reload keys in SSHManager         │                       │              │
     │                                       │                       │              │
     │  6. Test SSH with new key (concurrent)│                       │              │
     │────────────────────────────────────────────────────────────────▶│              │
     │──────────────────────────────────────────────────────────────────────────────▶│
     │  ◀── ok ──────────────────────────────────────────────────────│              │
     │  ◀── ok ────────────────────────────────────────────────────────────────────│
     │                                       │                       │              │
     │  7. Remove old key (overwrite authorized_keys with new only)  │              │
     │──────────────────────────────────────▶│                       │              │
     │                                       │  ConfigureSSHAccess   │              │
     │                                       │──────────────────────▶│              │
     │                                       │──────────────────────────────────────▶│
     │                                       │                       │              │
     │  8. Delete ssh_key.old backups        │                       │              │
     │                                       │                       │              │
```

## Configuration Reference

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CLAWORC_DATA_PATH` | `/app/data` | Directory for SSH key files and SQLite database |
| `CLAWORC_TERMINAL_HISTORY_LINES` | `1000` | Terminal scrollback buffer size (0 to disable) |
| `CLAWORC_TERMINAL_RECORDING_DIR` | *(empty)* | Directory for terminal audit recordings (empty = disabled) |
| `CLAWORC_TERMINAL_SESSION_TIMEOUT` | `30m` | Idle detached terminal session timeout |

### Settings Table

| Key | Default | Description |
|-----|---------|-------------|
| `ssh_key_rotation_policy_days` | `90` | Days between automatic key rotations |
| `ssh_key_last_rotation` | *(timestamp)* | When the last key rotation completed |
| `ssh_audit_retention_days` | `90` | Days to retain audit log entries |

### API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/v1/instances/{id}/ssh-events` | GET | Connection event history (ring buffer) |
| `/api/v1/audit-logs` | GET | Persistent SSH audit logs (admin only) |
| `/api/v1/settings` | GET/PUT | SSH-related settings (rotation policy, audit retention) |
