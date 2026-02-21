---
type: reference
title: SSH Configuration Reference
created: 2026-02-21
tags:
  - ssh
  - configuration
  - reference
related:
  - "[[ssh-connectivity]]"
  - "[[ssh-operations]]"
  - "[[proxy-to-ssh-migration]]"
---

# SSH Configuration Reference

This document provides a comprehensive reference for all SSH-related configuration in the OpenClaw Orchestrator. It covers environment variables, database schema, agent and control plane settings, key storage, tunnel port allocation, and deployment-specific examples.

## Table of Contents

- [Environment Variables](#environment-variables)
- [Database Schema](#database-schema)
- [Agent SSH Server Configuration](#agent-ssh-server-configuration)
- [Control Plane SSH Client Configuration](#control-plane-ssh-client-configuration)
- [Key Storage Paths and Permissions](#key-storage-paths-and-permissions)
- [Tunnel Port Allocation Strategy](#tunnel-port-allocation-strategy)
- [Configuration Examples](#configuration-examples)

---

## Environment Variables

SSH configuration is primarily managed through hardcoded constants and database fields rather than environment variables. The main application config uses `envconfig` with the `CLAWORC_` prefix (see `internal/config/config.go`):

| Variable | Type | Default | Description |
|----------|------|---------|-------------|
| `CLAWORC_DATABASE_PATH` | string | `/app/data/claworc.db` | SQLite database file path (stores SSH metadata) |
| `CLAWORC_K8S_NAMESPACE` | string | `claworc` | Kubernetes namespace for instance pods |
| `CLAWORC_DOCKER_HOST` | string | `""` | Docker daemon socket (for Docker orchestrator) |
| `CLAWORC_AUTH_DISABLED` | bool | `false` | Disable authentication (dev only) |

> **Note:** There are no dedicated SSH-specific environment variables. All SSH tuning constants (timeouts, retry counts, ports) are defined as Go constants in the respective packages. To change these values, modify the source constants and rebuild.

### SSH-Related Constants by Package

#### `sshkeys` (`internal/sshkeys/sshkeys.go`)

| Constant | Value | Description |
|----------|-------|-------------|
| `DefaultKeyDir` | `/app/data/ssh-keys` | Base directory for storing SSH private keys |

#### `sshmanager` (`internal/sshmanager/manager.go`)

| Constant | Value | Description |
|----------|-------|-------------|
| `defaultKeepaliveInterval` | `30s` | Interval between SSH keepalive requests |
| `HealthCheckTimeout` | `5s` | Timeout for `echo ping` health check command |
| `DefaultMaxRetries` | `10` | Maximum reconnection attempts before giving up |
| `reconnectBaseDelay` | `1s` | Initial backoff delay for reconnection |
| `reconnectMaxDelay` | `16s` | Maximum backoff delay for reconnection |
| `reconnectBackoffFactor` | `2` | Exponential backoff multiplier |

#### `sshmanager` - Rate Limiting (`internal/sshmanager/ratelimit.go`)

| Constant | Value | Description |
|----------|-------|-------------|
| `DefaultMaxAttemptsPerMinute` | `10` | Max connection attempts per instance per minute |
| `DefaultMaxConsecFailures` | `5` | Consecutive failures before temporary block |
| `DefaultBlockDuration` | `5m` | Duration to block after max consecutive failures |

#### `sshtunnel` (`internal/sshtunnel/tunnel.go`)

| Constant | Value | Description |
|----------|-------|-------------|
| `DefaultVNCPort` | `3000` | Selkies/VNC port on agent |
| `DefaultGatewayPort` | `8080` | OpenClaw gateway port on agent |
| `defaultHealthCheckInterval` | `10s` | Per-instance tunnel health check interval |
| `tunnelHealthCheckInterval` | `60s` | Global tunnel health check sweep interval |
| `tunnelHealthProbeTimeout` | `5s` | TCP probe timeout for tunnel liveness |
| `reconnectBaseDelay` | `1s` | Initial backoff delay for tunnel reconnection |
| `reconnectMaxDelay` | `60s` | Maximum backoff delay for tunnel reconnection |
| `reconnectBackoffFactor` | `2` | Exponential backoff multiplier |

#### `sshterminal` (`internal/sshterminal/`)

| Constant | Value | Source File | Description |
|----------|-------|-------------|-------------|
| `DefaultShell` | `/bin/bash` | `terminal.go` | Shell started for interactive sessions |
| `defaultTermCols` | `80` | `terminal.go` | Initial PTY width (columns) |
| `defaultTermRows` | `24` | `terminal.go` | Initial PTY height (rows) |
| `DefaultIdleTimeout` | `30m` | `session_manager.go` | Idle timeout before session cleanup |
| `defaultScrollbackSize` | `1 MB` (1048576) | `scrollback.go` | Maximum scrollback buffer size |

#### `sshaudit` (`internal/sshaudit/audit.go`)

| Constant | Value | Description |
|----------|-------|-------------|
| `DefaultRetentionDays` | `90` | Days to retain audit log entries |

#### `sshmanager` - Event and State Tracking

| Constant | Value | Source File | Description |
|----------|-------|-------------|-------------|
| `maxEventsPerInstance` | `100` | `events.go` | Ring buffer capacity for connection events |
| `maxTransitionsPerInstance` | `50` | `state.go` | State transition history capacity |

---

## Database Schema

SSH metadata is stored in the SQLite database alongside instance records. Two models carry SSH-related fields.

### Instance Model (`instances` table)

The `Instance` model (defined in `internal/database/models.go`) includes the following SSH fields:

| Column | Go Field | Type | GORM Tag | JSON | Default | Description |
|--------|----------|------|----------|------|---------|-------------|
| `ssh_public_key` | `SSHPublicKey` | `string` | `type:text` | `-` (hidden) | — | ED25519 public key in authorized_keys format |
| `ssh_private_key_path` | `SSHPrivateKeyPath` | `string` | `type:text` | `-` (hidden) | — | Filesystem path to the instance's private key |
| `ssh_key_fingerprint` | `SSHKeyFingerprint` | `string` | `type:text` | `-` (hidden) | — | SHA256 fingerprint for key verification |
| `ssh_port` | `SSHPort` | `int` | `default:22` | `ssh_port` | `22` | SSH port on the agent |
| `last_key_rotation` | `LastKeyRotation` | `*time.Time` | — | `last_key_rotation` | `null` | Timestamp of last key rotation |
| `key_rotation_policy` | `KeyRotationPolicy` | `int` | `default:90` | `key_rotation_policy` | `90` | Days between automatic key rotations (0 = disabled) |
| `allowed_source_ips` | `AllowedSourceIPs` | `string` | `type:text;default:''` | `allowed_source_ips` | `""` (allow all) | Comma-separated IPs/CIDRs for connection restrictions |

**Security notes:**
- `SSHPublicKey`, `SSHPrivateKeyPath`, and `SSHKeyFingerprint` are tagged `json:"-"` and are never exposed via the API.
- `SSHPort`, `LastKeyRotation`, `KeyRotationPolicy`, and `AllowedSourceIPs` are visible in API responses.

### SSH Audit Log Model (`ssh_audit_logs` table)

The `SSHAuditLog` model (defined in `internal/database/models.go`) records security-relevant SSH events:

| Column | Go Field | Type | GORM Tag | JSON | Description |
|--------|----------|------|----------|------|-------------|
| `id` | `ID` | `uint` | `primaryKey;autoIncrement` | `id` | Primary key |
| `instance_id` | `InstanceID` | `uint` | `index;not null` | `instance_id` | Foreign key to instance |
| `instance_name` | `InstanceName` | `string` | `index;not null` | `instance_name` | Denormalized instance name |
| `event_type` | `EventType` | `string` | `index;not null` | `event_type` | Event category (see below) |
| `username` | `Username` | `string` | — | `username` | SSH username |
| `source_ip` | `SourceIP` | `string` | — | `source_ip` | Client IP address |
| `details` | `Details` | `string` | `type:text` | `details` | Event-specific details |
| `duration` | `Duration` | `int64` | — | `duration_ms` | Connection duration in ms |
| `created_at` | `CreatedAt` | `time.Time` | `autoCreateTime;index` | `created_at` | Event timestamp |

**Audit event types** (`internal/sshaudit/audit.go`):

| Event Type | Description |
|------------|-------------|
| `connection_established` | SSH connection successfully opened |
| `connection_terminated` | SSH connection closed (includes duration) |
| `command_execution` | Command executed over SSH |
| `file_operation` | File read/write/list over SSH |
| `terminal_session_start` | Interactive PTY session started |
| `terminal_session_end` | Interactive PTY session ended |
| `key_rotation` | SSH key pair rotated |
| `connection_failed` | SSH connection attempt failed |
| `fingerprint_mismatch` | Host key fingerprint mismatch detected |
| `ip_restricted` | Connection blocked by IP restriction |

### Database Migration

Both models are auto-migrated at startup via GORM (`internal/database/database.go`):

```go
DB.AutoMigrate(&Instance{}, &Setting{}, &InstanceAPIKey{}, &User{},
    &UserInstance{}, &WebAuthnCredential{}, &SSHAuditLog{})
```

No manual migration steps are required. GORM adds new columns non-destructively when models change.

---

## Agent SSH Server Configuration

Each agent instance runs an OpenSSH server (`sshd`) supervised by s6-overlay. The configuration is hardened for security.

### SSHD Configuration

**File:** `agent/rootfs/etc/ssh/sshd_config.d/claworc.conf`

#### Network
```
Port 22
ListenAddress 0.0.0.0
```

#### Authentication (Public Key Only)
```
PubkeyAuthentication yes
PasswordAuthentication no
PermitEmptyPasswords no
PermitRootLogin prohibit-password
KbdInteractiveAuthentication no
HostbasedAuthentication no
UsePAM no
```

- Only ED25519 public key authentication is accepted.
- Root login is permitted via public key only (`prohibit-password`).
- All password-based and interactive authentication is disabled.

#### Security Hardening
```
StrictModes yes
MaxAuthTries 3
LoginGraceTime 30
MaxSessions 10
MaxStartups 10:30:60
```

| Setting | Value | Description |
|---------|-------|-------------|
| `StrictModes` | `yes` | Enforce correct file permissions on `~/.ssh` |
| `MaxAuthTries` | `3` | Maximum authentication attempts per connection |
| `LoginGraceTime` | `30` | Seconds before unauthenticated connection is dropped |
| `MaxSessions` | `10` | Maximum concurrent sessions per connection |
| `MaxStartups` | `10:30:60` | Rate limiting: allow 10, then 30% rejection up to 60 |

#### Forwarding Restrictions
```
AllowTcpForwarding local
PermitOpen localhost:3000 localhost:8080
X11Forwarding no
AllowAgentForwarding no
AllowStreamLocalForwarding no
```

- Only local TCP forwarding is permitted (needed for SSH tunnels to VNC/Gateway).
- `PermitOpen` restricts forwarding targets to `localhost:3000` (VNC) and `localhost:8080` (Gateway).
- X11, agent, and Unix socket forwarding are all disabled.

#### Logging
```
SyslogFacility AUTH
LogLevel INFO
```

Logs are written to `/var/log/sshd.log` via stderr redirection.

### Service Startup

**File:** `agent/rootfs/etc/s6-overlay/s6-rc.d/svc-sshd/run`

The s6-overlay supervised service:

1. Generates ED25519 host keys on first start (`ssh-keygen -A`).
2. Removes legacy host key types (DSA, ECDSA) — keeps only ED25519 and RSA.
3. Creates `/run/sshd` privilege separation directory.
4. Ensures `/root/.ssh` exists with `700` permissions.
5. Sets `authorized_keys` to `600` permissions if it exists.
6. Starts sshd in foreground mode: `exec /usr/sbin/sshd -D -e 2>> /var/log/sshd.log`

### Authorized Key Provisioning

When an instance is created, the control plane provisions the SSH public key to the agent:

1. Waits up to 120 seconds for the instance to be running (`internal/orchestrator/common.go`).
2. Creates `/root/.ssh` directory with `700` permissions.
3. Writes the public key to `/root/.ssh/authorized_keys` with `600` permissions.
4. Uses base64 encoding to avoid shell escaping issues during key transfer.

This flow is triggered by both the Docker and Kubernetes orchestrators after container/pod creation.

---

## Control Plane SSH Client Configuration

The control plane acts as the SSH client, connecting to each agent's sshd. Configuration is managed through Go constants and runtime initialization.

### Connection Parameters

| Parameter | Value | Description |
|-----------|-------|-------------|
| SSH User | `root` | All connections authenticate as root |
| Authentication | Public key (ED25519) | Private key loaded from disk |
| Connection Timeout | `10s` | Timeout for initial TCP + SSH handshake |
| Host Key Verification | TOFU | Trust On First Use; fingerprint stored for subsequent checks |

### Keepalive and Health Checks

| Parameter | Value | Description |
|-----------|-------|-------------|
| Keepalive Interval | `30s` | `keepalive@openssh.com` request sent every 30s |
| Health Check Timeout | `5s` | Timeout for `echo ping` command |
| Health Check Command | `echo ping` | Verifies SSH session is functional |

### Reconnection with Exponential Backoff

When a connection is lost, the SSH manager attempts automatic reconnection:

| Parameter | Value | Description |
|-----------|-------|-------------|
| Max Retries | `10` | Attempts before declaring connection failed |
| Base Delay | `1s` | Initial delay between retries |
| Max Delay | `16s` | Cap on exponential backoff |
| Backoff Factor | `2` | Delay doubles after each attempt (1s, 2s, 4s, 8s, 16s, 16s, ...) |

After exhausting retries, the connection state transitions to `failed` and an `EventReconnectFailed` event is emitted.

### Rate Limiting

Per-instance rate limiting protects against connection storms:

| Parameter | Value | Description |
|-----------|-------|-------------|
| Max Attempts/Minute | `10` | Sliding window per-minute limit |
| Max Consecutive Failures | `5` | Failures before temporary block |
| Block Duration | `5m` | How long the instance is blocked |

### Connection States

The SSH manager tracks connection state per instance:

| State | Description |
|-------|-------------|
| `disconnected` | No active connection |
| `connecting` | Connection attempt in progress |
| `connected` | Connection established and healthy |
| `reconnecting` | Lost connection, attempting to restore |
| `failed` | Reconnection gave up after max retries |

### IP Source Restrictions

Per-instance IP allow lists can restrict which source IPs may connect:

- Supports single IPs (converted to /32 or /128 CIDR) and CIDR ranges.
- Comma-separated format: `"10.0.0.0/8, 192.168.1.0/24"`.
- Empty string means allow all connections.
- Configured via the `AllowedSourceIPs` database field.

### Connection Multiplexing

A single SSH connection per instance is shared by all subsystems:

- **Tunnels** — VNC and Gateway port forwards
- **File operations** — SFTP-style read/write/list
- **Log streaming** — Remote log tail via SSH exec
- **Terminal sessions** — Interactive PTY shells

This eliminates per-request connection overhead (1-5ms on loopback vs 20-100ms for K8s exec).

---

## Key Storage Paths and Permissions

### Control Plane Key Storage

| Item | Path | Permissions | Description |
|------|------|-------------|-------------|
| Key directory | `/app/data/ssh-keys/` | `0700` | Base directory for all private keys |
| Private key file | `/app/data/ssh-keys/{instanceName}.key` | `0600` | PEM-encoded ED25519 private key |

- One private key per instance, named by the Kubernetes-safe instance name.
- Keys are created during instance provisioning and deleted on instance removal.
- The directory is automatically created on first key save if it doesn't exist.

### Agent Key Storage

| Item | Path | Permissions | Description |
|------|------|-------------|-------------|
| SSH directory | `/root/.ssh/` | `0700` | Standard SSH directory for root user |
| Authorized keys | `/root/.ssh/authorized_keys` | `0600` | Contains the instance's public key |
| Host keys | `/etc/ssh/ssh_host_ed25519_key` | `0600` | Server host key (auto-generated) |
| Host key (RSA) | `/etc/ssh/ssh_host_rsa_key` | `0600` | RSA host key (compatibility fallback) |
| SSHD log | `/var/log/sshd.log` | — | SSH daemon log file |

### Key Lifecycle

1. **Generation:** `ed25519.GenerateKey()` produces a 256-bit key pair.
2. **Storage:** Private key saved to control plane disk in PEM format; public key saved to database.
3. **Provisioning:** Public key written to agent's `/root/.ssh/authorized_keys`.
4. **Rotation:** Zero-downtime rotation appends new key, tests connectivity, then removes old key.
5. **Deletion:** Private key file removed from disk; database fields cleared.

### Key Rotation

Rotation is zero-downtime and follows this sequence:

1. Generate new ED25519 key pair.
2. Append new public key to agent's `authorized_keys` (both keys active).
3. Test new key by opening a fresh SSH connection.
4. Remove old public key from agent's `authorized_keys`.
5. Update database with new fingerprint and rotation timestamp.

If the new key test fails, the new key is rolled back (removed from `authorized_keys`), and the old key remains active. The `KeyRotationPolicy` field controls automatic rotation interval (default: 90 days, 0 to disable).

---

## Tunnel Port Allocation Strategy

### Overview

SSH tunnels use local TCP forwarding to expose agent services on the control plane. Each tunnel binds to an ephemeral local port on `127.0.0.1`.

### Port Allocation

| Parameter | Value | Description |
|-----------|-------|-------------|
| Bind Address | `127.0.0.1` | Tunnels are only accessible locally |
| Local Port | `0` (auto-assigned) | OS assigns an available ephemeral port |
| VNC Remote Port | `3000` | Selkies/VNC service on agent |
| Gateway Remote Port | `8080` | OpenClaw gateway service on agent |

The actual bound port is returned to the caller after listener creation. This avoids port conflicts when running multiple instances.

### Tunnel Types

| Type | SSH Equivalent | Direction | Usage |
|------|---------------|-----------|-------|
| `forward` | `ssh -L` | Local → Remote | Standard port forwarding |
| `reverse` | `ssh -R` | Remote → Local | Reverse port forwarding |

Currently, VNC and Gateway tunnels use the reverse tunnel type (creating a local listener that forwards to the remote agent port via `direct-tcpip` SSH channels).

### Tunnel Health Monitoring

Two levels of health monitoring ensure tunnel reliability:

**Per-instance monitoring** (every 10 seconds):
- Checks if expected tunnels (VNC, Gateway) are still active.
- Detects closed tunnels and triggers reconnection.
- Reconnects with exponential backoff (1s base, 60s max, factor 2).

**Global health sweep** (every 60 seconds):
- Probes all active tunnel local ports via TCP connect.
- Closes tunnels whose local listeners are no longer accepting connections.
- 5-second timeout per probe.

### Tunnel Lifecycle

1. **Creation:** `StartTunnelsForInstance()` creates VNC and Gateway tunnels.
2. **Monitoring:** Per-instance monitor starts on tunnel creation.
3. **Recovery:** Detected failures trigger automatic reconnection with backoff.
4. **Shutdown:** `StopTunnelsForInstance()` cancels monitors and closes all tunnels.

### Performance

| Metric | Value |
|--------|-------|
| Per-request overhead | ~55 us on loopback |
| Throughput | >27,000 req/s with 10 concurrent clients |
| WebSocket round-trip latency | ~55 us additional |

---

## Configuration Examples

### Development (Docker)

For local development with Docker orchestrator:

```bash
# Environment variables
export CLAWORC_DATABASE_PATH="./data/claworc.db"
export CLAWORC_DOCKER_HOST=""  # uses default Docker socket
export CLAWORC_AUTH_DISABLED=true

# SSH keys will be stored at /app/data/ssh-keys/ inside the container.
# For local dev, mount a volume:
docker run -v ./data:/app/data claworc
```

**Key storage for local dev:**
- Database: `./data/claworc.db`
- SSH keys: `./data/ssh-keys/`
- Both persist across restarts via the mounted volume.

**Agent SSH access:**
- Docker orchestrator maps port 22 from the agent container.
- Control plane connects to the Docker-assigned port.
- No firewall rules needed for local Docker networking.

### Staging (Kubernetes)

For staging environments with Kubernetes orchestrator:

```yaml
# Kubernetes deployment environment
env:
  - name: CLAWORC_DATABASE_PATH
    value: "/app/data/claworc.db"
  - name: CLAWORC_K8S_NAMESPACE
    value: "claworc-staging"

# Volume mount for persistent data
volumeMounts:
  - name: data
    mountPath: /app/data

volumes:
  - name: data
    persistentVolumeClaim:
      claimName: claworc-data
```

**Key storage for staging:**
- SSH keys stored on the PVC at `/app/data/ssh-keys/`.
- Database stored on the same PVC at `/app/data/claworc.db`.
- PVC should use `ReadWriteOnce` access mode.

**Network requirements:**
- Control plane pod must reach agent pods on port 22.
- Agent service uses NodePort for external access to VNC/Gateway.
- Consider NetworkPolicy to restrict SSH traffic to the control plane pod only.

### Production (Kubernetes)

For production deployments:

```yaml
# Production environment
env:
  - name: CLAWORC_DATABASE_PATH
    value: "/app/data/claworc.db"
  - name: CLAWORC_K8S_NAMESPACE
    value: "claworc"

# Recommended: separate PVC for SSH keys with restricted access
volumeMounts:
  - name: data
    mountPath: /app/data
  - name: ssh-keys
    mountPath: /app/data/ssh-keys

volumes:
  - name: data
    persistentVolumeClaim:
      claimName: claworc-data
  - name: ssh-keys
    persistentVolumeClaim:
      claimName: claworc-ssh-keys
```

**Production hardening recommendations:**

1. **Key storage isolation:** Use a separate PVC for SSH keys with more restrictive RBAC.
2. **Key rotation policy:** Set `KeyRotationPolicy` to 30 days (via API).
3. **IP restrictions:** Configure `AllowedSourceIPs` per instance to restrict access.
4. **Audit retention:** Default 90-day retention; adjust based on compliance requirements.
5. **Network policies:** Create Kubernetes NetworkPolicy resources to restrict SSH traffic:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-ssh-from-control-plane
  namespace: claworc
spec:
  podSelector:
    matchLabels:
      app: bot
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app: claworc-control-plane
      ports:
        - port: 22
          protocol: TCP
```

6. **Monitoring:** Watch for `connection_failed` and `fingerprint_mismatch` audit events as security indicators.
7. **Backup:** Include `/app/data/ssh-keys/` in backup procedures alongside the database.

---

## Quick Reference: All Configuration at a Glance

| Component | Parameter | Value |
|-----------|-----------|-------|
| **Key Management** | Algorithm | ED25519 |
| | Key directory | `/app/data/ssh-keys/` |
| | Key file permissions | `0600` |
| | Directory permissions | `0700` |
| | Default rotation policy | 90 days |
| **SSH Connection** | User | `root` |
| | Port | `22` |
| | Connection timeout | 10s |
| | Keepalive interval | 30s |
| | Health check timeout | 5s |
| | Max retries | 10 |
| | Reconnect backoff | 1s → 16s (factor 2) |
| **Rate Limiting** | Max attempts/min | 10 |
| | Max consecutive failures | 5 |
| | Block duration | 5 min |
| **Tunnels** | VNC remote port | 3000 |
| | Gateway remote port | 8080 |
| | Local bind address | `127.0.0.1:0` (ephemeral) |
| | Per-instance health check | 10s |
| | Global health sweep | 60s |
| | Tunnel probe timeout | 5s |
| | Tunnel reconnect backoff | 1s → 60s (factor 2) |
| **Terminal** | Shell | `/bin/bash` |
| | Terminal type | `xterm-256color` |
| | Dimensions | 80x24 |
| | Idle timeout | 30 min |
| | Scrollback buffer | 1 MB |
| **Audit** | Retention | 90 days |
| | Default query limit | 50 |
| | Max query limit | 1000 |
| **Agent sshd** | `MaxAuthTries` | 3 |
| | `LoginGraceTime` | 30s |
| | `MaxSessions` | 10 |
| | `MaxStartups` | 10:30:60 |
| | `PermitOpen` | `localhost:3000 localhost:8080` |
| **Event Tracking** | Events per instance | 100 (ring buffer) |
| | State transitions | 50 (ring buffer) |
