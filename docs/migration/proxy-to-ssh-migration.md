---
type: reference
title: Proxy to SSH Migration Guide
created: 2026-02-21
tags:
  - migration
  - ssh
  - architecture
  - operations
related:
  - "[[ssh-connectivity]]"
  - "[[ssh-operations]]"
  - "[[ssh-resilience]]"
---

# Migration Guide: HTTP Proxy to SSH-Based Connectivity

## Overview

Claworc's control-plane-to-agent communication was migrated from a multi-protocol proxy model (HTTP transports, K8s exec, yamux tunnels, mTLS) to a unified SSH-based architecture. SSH is now the **sole transport layer** for all agent communication: desktop streaming, chat, file operations, log streaming, and interactive terminals.

This document covers every change made during the migration, step-by-step upgrade procedures, a rollback plan, and a testing checklist.

---

## Table of Contents

- [Architecture Comparison](#architecture-comparison)
- [Removed Components](#removed-components)
- [New Components](#new-components)
- [Database Schema Changes](#database-schema-changes)
- [API Changes](#api-changes)
- [Breaking Changes](#breaking-changes)
- [Migration Procedure](#migration-procedure)
- [Rollback Plan](#rollback-plan)
- [Testing Checklist](#testing-checklist)

---

## Architecture Comparison

### Before: HTTP Proxy / K8s Exec Model

```
Browser ──HTTP/WS──► Control Plane ──K8s API──► Agent Pod
                          │                       │
                     getProxyClient()           Services:
                     defaultTransport            ├─ VNC (:3000)
                     GetHTTPTransport()          ├─ Gateway (:8080)
                          │                      ├─ Shell (K8s exec)
                     K8s exec / port-forward     ├─ Files (K8s exec)
                     mTLS yamux tunnels          └─ Logs (K8s exec)
                          │
                     Direct pod IP access
                     or NodePort routing
```

**Characteristics of the old model:**

- Desktop (VNC) and Gateway traffic used `GetVNCBaseURL()` / `GetGatewayWSURL()` to build direct URLs to the agent pod.
- `GetHTTPTransport()` returned an `*http.Transport` for direct HTTP access to agent pods.
- `getProxyClient()` in `control.go` built a per-request HTTP client using `defaultTransport`.
- File operations (`ReadFile`, `WriteFile`, `ListDirectory`, `CreateDirectory`) used Kubernetes `exec` API calls.
- Log streaming used `StreamInstanceLogs()` on the `ContainerOrchestrator` interface via K8s exec.
- Terminal sessions used `ExecInteractive()` / `ExecSession()` on the orchestrator interface via K8s exec.
- Agent service ports were potentially exposed via NodePort or direct pod networking.

### After: SSH-Based Model

```
Browser ──HTTP/WS──► Control Plane ──SSH──► Agent Pod
                          │                    │
                     SSHManager             sshd (port 22)
                     TunnelManager             │
                     SessionManager         Services:
                          │                 ├─ VNC (:3000)
                     LocalPort:N ◄─tunnel─► ├─ Gateway (:8080)
                          │                 ├─ Shell (SSH PTY)
                     HTTP Proxy             ├─ Files (SSH exec)
                     WS Proxy               └─ Logs (SSH exec)
```

**Characteristics of the new model:**

- A single multiplexed SSH connection per agent carries all traffic.
- VNC and Gateway traffic flows through SSH port-forwarding tunnels bound to ephemeral localhost ports.
- File operations use SSH exec sessions with shell command execution.
- Log streaming uses `tail -F` over SSH exec sessions.
- Terminal sessions use SSH PTY sessions with full scrollback and recording.
- No agent ports are directly exposed -- all access goes through SSH tunnels on the control plane.
- ED25519 key-per-instance with automatic rotation.
- Three-layer resilience: SSHManager keepalive, TunnelManager health probes, maintenance loop.

---

## Removed Components

The following components were removed during the migration:

### Orchestrator Interface Methods (Removed)

| Method | Package | Purpose | Replaced By |
|--------|---------|---------|-------------|
| `GetVNCBaseURL()` | `orchestrator` | Build URL for direct VNC pod access | `sshtunnel.TunnelManager` (VNC tunnel) |
| `GetGatewayWSURL()` | `orchestrator` | Build URL for direct Gateway pod access | `sshtunnel.TunnelManager` (Gateway tunnel) |
| `GetHTTPTransport()` | `orchestrator` | Get HTTP transport for pod network access | `tunnelHTTPClient` (localhost proxy) |
| `ReadFile()` | `orchestrator` | Read files via K8s exec | `sshfiles.ReadFile()` |
| `WriteFile()` | `orchestrator` | Write files via K8s exec | `sshfiles.WriteFile()` |
| `ListDirectory()` | `orchestrator` | List directories via K8s exec | `sshfiles.ListDirectory()` |
| `CreateDirectory()` | `orchestrator` | Create directories via K8s exec | `sshfiles.CreateDirectory()` |
| `StreamInstanceLogs()` | `orchestrator` | Stream logs via K8s exec | `sshlogs.StreamLogs()` |
| `ExecInteractive()` | `orchestrator` | Interactive shell via K8s exec | `sshterminal.SessionManager` |
| `ExecSession()` | `orchestrator` | One-shot exec via K8s exec | `sshterminal.SessionManager` |

### Internal Functions (Removed)

| Function | File | Purpose | Replaced By |
|----------|------|---------|-------------|
| `getProxyClient()` | `control.go` | Build per-request HTTP client | `tunnelHTTPClient` (shared client) |
| `defaultTransport` | `control.go` | Default HTTP transport for pod access | `http.Transport` optimized for localhost |

### Agent Proxy Module (Removed)

The intermediate agent proxy Go module and its s6-overlay service were removed:

| Component | Purpose | Status |
|-----------|---------|--------|
| Agent proxy HTTP/WS gateway | Reverse-proxy inside agent pod | Replaced by direct SSH tunnels |
| mTLS tunnel listener with yamux | Multiplexed TLS tunnels | Replaced by SSH multiplexing |
| TLS cert/key injection from env vars | Mutual TLS auth | Replaced by SSH ED25519 key auth |
| s6-overlay `svc-proxy` service | Run proxy inside agent | No longer needed |

### Orchestrator Interface Changes

The `ContainerOrchestrator` interface was simplified. The current interface retains:
- `Initialize`, `IsAvailable`, `BackendName` (infrastructure)
- `CreateInstance`, `DeleteInstance`, `StartInstance`, `StopInstance`, `RestartInstance`, `GetInstanceStatus` (lifecycle)
- `UpdateInstanceConfig` (config management)
- `CloneVolumes` (data)
- `GetInstanceSSHEndpoint` (new -- returns host:port for SSH connections)

---

## New Components

### SSH Packages

| Package | Path | Responsibility |
|---------|------|----------------|
| `sshkeys` | `internal/sshkeys/` | ED25519 key generation, disk storage (`/app/data/ssh-keys/`), key rotation with zero-downtime, fingerprint verification (TOFU) |
| `sshmanager` | `internal/sshmanager/` | Connection pool, keepalive health checks (30s), exponential-backoff reconnection (1s-16s, 10 retries), connection state machine, event logging, rate limiting, IP restrictions |
| `sshtunnel` | `internal/sshtunnel/` | SSH port-forwarding tunnels (VNC on agent:3000, Gateway on agent:8080), TCP probe health monitoring, per-instance monitors, byte transfer counting, global singleton registry |
| `sshfiles` | `internal/sshfiles/` | Remote file CRUD via SSH exec: `ListDirectory` (ls), `ReadFile` (cat), `WriteFile` (cat > stdin), `CreateDirectory` (mkdir -p). Shell injection prevention via `shellQuote()` |
| `sshlogs` | `internal/sshlogs/` | Log streaming via `tail -F` over SSH exec. Supports openclaw, browser, system log types. Log rotation aware |
| `sshterminal` | `internal/sshterminal/` | Interactive PTY sessions over SSH. Multi-session support, 30-min idle timeout, 1MB scrollback buffer, session recording (asciinema-compatible), shell whitelist security |
| `sshaudit` | `internal/sshaudit/` | Database-backed audit logging (10 event types). Configurable retention (default 90 days). Query filtering by instance, event type, user, time range |

### Handler Changes

| Handler | File | Old Mechanism | New Mechanism |
|---------|------|---------------|---------------|
| `DesktopProxy` | `sshproxy.go` | `getProxyClient()` + direct pod URL | `getTunnelPort()` + `proxyToLocalPort()` / `websocketProxyToLocalPort()` |
| `ControlProxy` | `sshproxy.go` | `getProxyClient()` + direct pod URL | `getTunnelPort()` + `proxyToLocalPort()` / `websocketProxyToLocalPort()` |
| `ChatProxy` | `sshproxy.go` | Direct WebSocket to pod | `getTunnelPort()` + `websocketProxyToLocalPort()` |
| `TerminalWSProxy` | `terminal.go` | K8s exec PTY | SSH PTY via `sshterminal.SessionManager` |
| `StreamLogs` | `logs.go` | K8s exec `tail` | `sshlogs.StreamLogs()` via SSH exec |
| File handlers | `files.go` | K8s exec | `sshfiles.*` via SSH exec |

### New API Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/instances/{id}/ssh-status` | Connection state, health, tunnel status, recent events |
| GET | `/api/v1/instances/{id}/ssh-events` | Connection event history |
| POST | `/api/v1/instances/{id}/ssh-test` | Full connection test with latency measurement |
| POST | `/api/v1/instances/{id}/ssh-reconnect` | Force reconnection + tunnel restart |
| GET | `/api/v1/instances/{id}/ssh-fingerprint` | Public key fingerprint and verification |
| GET | `/api/v1/instances/{id}/tunnels` | Active tunnel details |
| POST | `/api/v1/instances/{id}/rotate-ssh-key` | Manual key rotation (admin) |
| GET | `/api/v1/instances/{id}/ssh-allowed-ips` | View IP allowlist (admin) |
| PUT | `/api/v1/instances/{id}/ssh-allowed-ips` | Update IP allowlist (admin) |
| GET | `/api/v1/ssh-audit-logs` | Query audit logs with filters (admin) |
| POST | `/api/v1/ssh-audit-logs/purge` | Manual audit log purge (admin) |
| GET | `/api/v1/ssh-status` | Global SSH status overview |
| GET | `/api/v1/ssh-metrics` | Aggregated SSH metrics |

### Frontend Components (New)

- SSH status indicator on instance detail page
- Connection event history viewer
- SSH troubleshooting dialog
- Global SSH connection dashboard
- SSH metrics visualization (recharts)

### Background Processes (New)

| Process | Interval | Purpose |
|---------|----------|---------|
| `tunnelMaintenanceLoop` | 60s | Ensure tunnels for running instances, clean stale tunnels |
| `keyRotationLoop` | 24h (1-min initial delay) | Auto-rotate keys per instance policy |
| `auditRetentionLoop` | 24h (5-min initial delay) | Purge expired audit logs |
| SSHManager keepalive | 30s | `keepalive@openssh.com` + `echo ping` health checks |
| TunnelManager global health | 60s | TCP probe all tunnel local ports |
| TunnelManager per-instance monitor | 10s | Detect/recreate missing tunnels |

---

## Database Schema Changes

### New Fields on `instances` Table

| Column | Type | Default | Description |
|--------|------|---------|-------------|
| `ssh_public_key` | TEXT | — | ED25519 public key (never exposed via API) |
| `ssh_private_key_path` | TEXT | — | Path to private key on disk (never exposed via API) |
| `ssh_key_fingerprint` | TEXT | — | SHA256 fingerprint (never exposed via API) |
| `ssh_port` | INT | 22 | SSH port on the agent |
| `last_key_rotation` | DATETIME | NULL | Timestamp of last key rotation |
| `key_rotation_policy` | INT | 90 | Days between automatic rotations (0 = disabled) |
| `allowed_source_ips` | TEXT | `""` | Comma-separated IP/CIDR allowlist |
| `log_paths` | TEXT | `"{}"` | JSON map of custom log file paths |

### New Table: `ssh_audit_logs`

| Column | Type | Description |
|--------|------|-------------|
| `id` | UINT (PK) | Auto-increment |
| `instance_id` | UINT (indexed) | Foreign key to instance |
| `instance_name` | VARCHAR (indexed) | Instance name for fast queries |
| `event_type` | VARCHAR (indexed) | One of 10 event types |
| `username` | VARCHAR | SSH username |
| `source_ip` | VARCHAR | Client source IP |
| `details` | TEXT | Event-specific details |
| `duration` | BIGINT | Duration in milliseconds |
| `created_at` | DATETIME (indexed) | Auto-set timestamp |

Both are auto-migrated by GORM on startup. No manual SQL migrations required.

---

## API Changes

### Removed Endpoints

No public API endpoints were removed. The migration was internal -- the same URL paths (`/desktop/*`, `/control/*`, `/chat`, `/terminal`, `/logs`, `/files/*`) continue to work with identical request/response formats. The transport mechanism changed from K8s exec/direct pod access to SSH, but this is transparent to API consumers.

### Changed Behavior

| Endpoint | Change |
|----------|--------|
| Desktop/Control/Chat proxy | Now routes through SSH tunnel localhost ports instead of direct pod IPs |
| Terminal WebSocket | SSH PTY replaces K8s exec. Adds session persistence, scrollback, recording |
| Log streaming (SSE) | SSH exec `tail -F` replaces K8s exec. Adds log rotation awareness |
| File operations | SSH exec replaces K8s exec. Latency improved from 20-100ms to 1-5ms |

### New Endpoints

See [New API Endpoints](#new-api-endpoints) above.

---

## Breaking Changes

### For Operators

1. **SSH key storage directory required**: The control plane now requires a persistent directory at `/app/data/ssh-keys/` (or the configured path) with permissions `0700`. In Kubernetes, this must be backed by a PVC or persistent volume.

2. **Agent must run sshd**: The agent image must include and run an OpenSSH server. The agent's sshd is configured via `/etc/ssh/sshd_config` with hardened settings (see [[ssh-connectivity]]).

3. **Port 22 on agents**: The control plane connects to agents on port 22 (configurable per-instance via `ssh_port`). Network policies must allow TCP traffic from the control plane to agent pods on this port.

4. **No more direct pod access**: Proxy handlers no longer construct URLs to pod IPs. The `GetVNCBaseURL()`, `GetGatewayWSURL()`, and `GetHTTPTransport()` methods are gone. Any custom tooling that relied on these methods must be updated.

5. **Database migration**: New columns on `instances` and a new `ssh_audit_logs` table are auto-migrated by GORM. No manual intervention required, but the first startup after upgrade will alter the schema.

### For Developers

1. **Orchestrator interface slimmed**: The `ContainerOrchestrator` interface no longer includes file, log, terminal, or proxy methods. All data-plane operations now go through the SSH packages directly.

2. **New dependency: SSH packages**: Handlers import `sshmanager`, `sshtunnel`, `sshfiles`, `sshlogs`, `sshterminal`, `sshaudit` directly instead of going through the orchestrator.

3. **Global singletons**: `sshtunnel.InitGlobal()` and `sshaudit.InitGlobal()` must be called during application startup (in `main.go`) before handlers are registered.

4. **Key lifecycle tied to instance lifecycle**: Creating an instance now includes SSH key generation, and deleting an instance includes SSH key cleanup.

### For API Consumers

- **No breaking changes to existing endpoints**. All existing URLs, request formats, and response formats remain the same.
- New SSH status/management endpoints are additive.

---

## Migration Procedure

### Prerequisites

- Claworc control plane binary built from the `ssh-proxy` branch (or later)
- Agent image version that includes sshd and the hardened sshd configuration
- Persistent volume for `/app/data/ssh-keys/` in Kubernetes deployments

### Step-by-Step Procedure

#### 1. Back Up the Database

```bash
# Kubernetes
kubectl cp claworc/<control-plane-pod>:/app/data/claworc.db ./claworc-backup.db

# Docker
docker cp claworc-control-plane:/app/data/claworc.db ./claworc-backup.db
```

#### 2. Update the Agent Image

Deploy the new agent image that includes sshd:

```bash
# Verify the agent image has sshd
docker run --rm <new-agent-image> which sshd
# Expected: /usr/sbin/sshd
```

The agent image must:
- Run sshd as a systemd service
- Have `/etc/ssh/sshd_config` with hardened settings
- Allow the control plane to write to `/root/.ssh/authorized_keys`

#### 3. Ensure Persistent Storage for SSH Keys

**Kubernetes:**

Add a PVC for SSH key storage if not already present. The control plane stores keys at `/app/data/ssh-keys/`:

```yaml
# In your Helm values or deployment manifest
volumes:
  - name: data
    persistentVolumeClaim:
      claimName: claworc-data
volumeMounts:
  - name: data
    mountPath: /app/data
```

**Docker:**

```bash
docker run -v claworc-data:/app/data <control-plane-image>
```

#### 4. Update Network Policies

Ensure the control plane can reach agent pods on TCP port 22:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: allow-ssh-to-agents
  namespace: claworc
spec:
  podSelector:
    matchLabels:
      app: bot-instance
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app: claworc-control-plane
      ports:
        - protocol: TCP
          port: 22
```

#### 5. Deploy the New Control Plane

```bash
# Kubernetes (Helm)
helm upgrade claworc ./helm --set image.tag=<new-version>

# Docker
docker pull <control-plane-image>:<new-version>
docker-compose up -d
```

On first startup:
- GORM auto-migrates the database (adds SSH columns to `instances`, creates `ssh_audit_logs` table)
- The `ssh-keys` directory is created at `/app/data/ssh-keys/`
- Global SSH singletons (SSHManager, TunnelManager, SessionManager, Auditor) are initialized

#### 6. Recreate Existing Instances

Existing instances do not have SSH keys. You must recreate them or manually provision keys:

**Option A: Recreate instances (recommended)**

```bash
# For each instance, delete and recreate
# This generates fresh SSH keys and configures the agent
curl -X DELETE http://localhost:8080/api/v1/instances/<id>
curl -X POST http://localhost:8080/api/v1/instances -d '{"name": "...", ...}'
```

**Option B: Manual key provisioning (advanced)**

For each instance without SSH keys:
1. Generate an ED25519 key pair
2. Save the private key to `/app/data/ssh-keys/<name>.key`
3. Update the database record with the public key, fingerprint, and key path
4. Install the public key on the agent at `/root/.ssh/authorized_keys`

#### 7. Verify Connectivity

```bash
# Test SSH connectivity for each instance
curl http://localhost:8080/api/v1/instances/<id>/ssh-test

# Check tunnel status
curl http://localhost:8080/api/v1/instances/<id>/tunnels

# View global SSH status
curl http://localhost:8080/api/v1/ssh-status
```

#### 8. Monitor Post-Migration

- Watch SSH connection events in the UI (instance detail > SSH status panel)
- Check the global SSH dashboard for connection health
- Review audit logs: `GET /api/v1/ssh-audit-logs`
- Monitor for `connection_failed` or `fingerprint_mismatch` events

---

## Rollback Plan

If critical issues arise after migration, follow this rollback procedure:

### Immediate Rollback (Within Minutes)

1. **Revert the control plane image** to the pre-SSH version:

   ```bash
   # Kubernetes
   helm rollback claworc <previous-revision>

   # Docker
   docker-compose down
   docker-compose up -d  # with previous image tag
   ```

2. **Restore the database** if schema changes cause issues:

   ```bash
   # Stop the control plane first
   kubectl cp ./claworc-backup.db claworc/<pod>:/app/data/claworc.db
   ```

3. **Revert agent images** to the version without sshd requirements if needed.

### Considerations

- The new SSH columns (`ssh_public_key`, etc.) on the `instances` table are additive. The old control plane version will ignore them (GORM does not fail on extra columns).
- The `ssh_audit_logs` table will remain in the database but is unused by the old version.
- Existing instances that were recreated with SSH keys will need to be recreated again after rollback (the old code does not read SSH key fields).
- Network policy changes (allowing port 22) are harmless to leave in place.

### Rollback Decision Criteria

| Symptom | Likely Cause | Action |
|---------|-------------|--------|
| All SSH connections failing | SSH keys not provisioned correctly | Check `/app/data/ssh-keys/` and agent authorized_keys |
| Tunnels not establishing | Network policy blocking port 22 | Add network policy for SSH traffic |
| Database migration fails | GORM auto-migration conflict | Restore backup, investigate schema |
| Agent sshd not starting | Missing sshd or config issue | Verify agent image, check systemd logs |
| Performance degradation | Unexpected -- SSH is faster | Profile; check tunnel health |

---

## Testing Checklist

Complete all items before considering the migration successful.

### Connectivity

- [ ] SSH connection test passes for all instances (`POST /instances/{id}/ssh-test`)
- [ ] Global SSH status shows all instances as `Connected` (`GET /ssh-status`)
- [ ] VNC tunnel is active and desktop streaming works (`GET /instances/{id}/tunnels`)
- [ ] Gateway tunnel is active and chat/control work (`GET /instances/{id}/tunnels`)

### Core Functionality

- [ ] Desktop (VNC) streaming loads in the browser
- [ ] Chat via WebSocket connects and sends/receives messages
- [ ] File browser lists directories correctly
- [ ] File read/write operations work
- [ ] File download works for large files
- [ ] Log streaming (SSE) displays live logs for all three log types
- [ ] Terminal opens an interactive bash session
- [ ] Terminal supports command execution with output

### Resilience

- [ ] Restarting an agent pod triggers automatic SSH reconnection
- [ ] Tunnels are recreated after agent restart
- [ ] Terminal sessions survive brief disconnections (scrollback replay)
- [ ] Rate limiter blocks excessive connection attempts (verify with `ssh-events`)

### Security

- [ ] SSH keys are generated during instance creation
- [ ] Private keys are stored with `0600` permissions
- [ ] Public keys are not exposed in API responses (`json:"-"`)
- [ ] Key rotation completes without service interruption (`POST /rotate-ssh-key`)
- [ ] Audit logs record connection events (`GET /ssh-audit-logs`)
- [ ] IP restriction blocks connections from disallowed IPs (when configured)

### Operations

- [ ] SSH status is visible in the instance detail UI
- [ ] Connection event history displays in the UI
- [ ] Global SSH dashboard shows all instances
- [ ] SSH metrics visualization loads correctly
- [ ] Troubleshooting dialog provides actionable information

### Performance

- [ ] File operations complete in < 10ms (vs 20-100ms with K8s exec)
- [ ] HTTP proxy through tunnel adds < 1ms overhead
- [ ] No connection leaks after extended operation (check `ssh-metrics`)
- [ ] Memory usage is stable (no unbounded growth in event/state buffers)

---

## Migration Timeline Summary

The migration was implemented in phases across the following commits:

| Phase | Description | Key Changes |
|-------|-------------|-------------|
| 1 | Agent proxy scaffolding | Initial HTTP/WS gateway, mTLS yamux tunnels (later replaced) |
| 2 | SSH tunnel introduction | Desktop and control proxies switched to SSH tunnels; `getProxyClient`, `GetHTTPTransport`, `GetVNCBaseURL`, `GetGatewayWSURL` removed |
| 3 | SSH file operations | File handlers migrated from K8s exec to `sshfiles`; orchestrator file methods removed |
| 4 | SSH log streaming | Log handler migrated from K8s exec to `sshlogs`; `StreamInstanceLogs` removed from orchestrator |
| 5 | SSH terminal | Terminal handler migrated from K8s exec to `sshterminal`; `ExecInteractive`/`ExecSession` removed |
| 6 | Resilience & monitoring | Keepalive, reconnection, tunnel health, connection state machine, status API |
| 7 | Frontend integration | SSH status components, event history, troubleshooting dialog, global dashboard, metrics |
| 8 | Security hardening | Key rotation, rate limiting, audit logging, sshd hardening, fingerprint verification, IP restrictions |
| 9 | Documentation | Architecture docs, operations runbook, sequence diagrams |
| 10 | Migration guide | This document |

---

## Further Reading

- [[ssh-connectivity]] -- Full SSH architecture documentation with diagrams
- [[ssh-operations]] -- Operations runbook for monitoring, troubleshooting, and maintenance
- [[ssh-resilience]] -- Detailed failure scenarios and expected recovery behavior
