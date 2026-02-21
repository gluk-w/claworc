# SSH-Based Connectivity Architecture

## Overview

Claworc uses SSH as its sole transport layer for all communication between the control plane and agent instances. The control plane acts as an SSH **client** and each agent runs an SSH **server** (sshd on port 22). All services — desktop streaming, chat, file operations, log streaming, and terminal access — flow through persistent, multiplexed SSH connections. No agent ports are exposed directly; the control plane binds ephemeral local ports via SSH tunnels and proxies browser traffic through them.

```
Browser ──HTTP/WS──► Control Plane ──SSH──► Agent Pod
                         │                     │
                    SSHManager              sshd (port 22)
                    TunnelManager              │
                    SessionManager          Services:
                         │                  ├─ Selkies VNC (3000)
                    LocalPort:N ◄──tunnel─► ├─ Gateway (8080)
                         │                  ├─ Shell (/bin/bash)
                    HTTP Proxy              ├─ Files (exec)
                    WS Proxy                └─ Logs (exec)
```

### Key Design Principles

- **Zero direct exposure**: Agent services never bind to externally accessible ports. All access goes through SSH tunnels on the control plane.
- **One connection per agent**: A single SSH connection multiplexes all tunnel traffic, exec sessions, and PTY sessions for a given instance.
- **Ephemeral local ports**: The control plane binds `127.0.0.1:0` for each tunnel, assigning random high ports. These are only accessible on localhost.
- **ED25519 keys only**: No password authentication. Each instance has a unique key pair.

## Components

### SSH Packages

| Package | Path | Responsibility |
|---------|------|----------------|
| `sshkeys` | `internal/sshkeys/` | Key generation, storage, rotation, fingerprint verification |
| `sshmanager` | `internal/sshmanager/` | Connection pool, keepalive, reconnection, state tracking, events |
| `sshtunnel` | `internal/sshtunnel/` | Port-forwarding tunnels, health probes, per-instance monitoring |
| `sshfiles` | `internal/sshfiles/` | Remote file CRUD via SSH exec sessions |
| `sshlogs` | `internal/sshlogs/` | Log streaming via `tail -F` over SSH exec sessions |
| `sshterminal` | `internal/sshterminal/` | Interactive PTY sessions, scrollback, session persistence, recording |
| `sshaudit` | `internal/sshaudit/` | Database-backed security audit logging with retention |

### Component Interaction

```
┌─────────────────────────────────────────────────────┐
│                   Control Plane                      │
│                                                      │
│  ┌──────────┐    ┌──────────────┐    ┌────────────┐ │
│  │ Handlers │───►│  SSHManager  │───►│ ssh.Client │─┼──► Agent sshd
│  └────┬─────┘    │  (pool +     │    └────────────┘ │
│       │          │   keepalive) │                    │
│       │          └──────────────┘                    │
│       │                                              │
│       ├──► TunnelManager ──► local:N ◄─tunnel─► agent:3000 (VNC)
│       │                  ──► local:M ◄─tunnel─► agent:8080 (Gateway)
│       │                                              │
│       ├──► sshfiles     ──► exec: ls, cat, mkdir     │
│       ├──► sshlogs      ──► exec: tail -F            │
│       ├──► sshterminal  ──► PTY session (bash)       │
│       └──► sshaudit     ──► SQLite audit_logs table  │
│                                                      │
└─────────────────────────────────────────────────────┘
```

## Bidirectional Tunnel Architecture

The tunnel system provides transparent access to agent services through local TCP ports on the control plane.

### Tunnel Types

| Service | Agent Port | Local Port | Protocol | Handler |
|---------|-----------|------------|----------|---------|
| Desktop (Selkies VNC) | 3000 | Ephemeral | HTTP + WebSocket | `DesktopProxy` |
| Gateway (OpenClaw) | 8080 | Ephemeral | HTTP + WebSocket | `ControlProxy`, `ChatProxy` |

### Tunnel Data Flow

```
Browser                  Control Plane                     Agent
  │                          │                               │
  │  GET /desktop            │                               │
  │─────────────────────────►│                               │
  │                          │  TunnelManager.GetTunnels()   │
  │                          │  → local port 54321           │
  │                          │                               │
  │                          │  HTTP proxy to 127.0.0.1:54321│
  │                          │──────────────────────────────►│
  │                          │   (SSH channel "direct-tcpip" │
  │                          │    to 127.0.0.1:3000)         │
  │                          │                               │
  │  ◄── response ───────────│◄──────────────────────────────│
  │                          │                               │
```

Each incoming TCP connection to a tunnel's local port triggers:
1. `listener.Accept()` on the local port
2. `sshClient.Dial("tcp", "127.0.0.1:{remotePort}")` opens an SSH channel
3. Bidirectional `io.Copy` between the local connection and the SSH channel
4. Byte counters track transferred data per tunnel

### Non-Tunnel Services

File operations, log streaming, and terminal sessions do not use port-forwarding tunnels. They use SSH sessions directly:

- **Files**: Open an SSH session, execute a command (`ls`, `cat`, `mkdir`, etc.), read stdout
- **Logs**: Open an SSH session, run `tail -F /path/to/logfile`, stream stdout line by line
- **Terminal**: Open an SSH session with a PTY, start an interactive shell, relay I/O over WebSocket

## Key Generation and Distribution

### Flow

```
Instance Creation
       │
       ▼
  ┌─────────────────────────────────┐
  │ 1. sshkeys.GenerateKeyPair()   │  ED25519 key pair
  └──────────────┬──────────────────┘
                 │
       ┌─────────┴──────────┐
       ▼                    ▼
  ┌──────────┐     ┌───────────────────────────┐
  │ Save     │     │ Format public key for     │
  │ private  │     │ authorized_keys           │
  │ key to   │     │                           │
  │ disk     │     │ Compute SHA256 fingerprint│
  └──────────┘     └───────────┬───────────────┘
       │                       │
       ▼                       ▼
  /app/data/ssh-keys/    Database: Instance record
  {name}.key (0600)      SSHPublicKey, SSHKeyFingerprint,
                         SSHPrivateKeyPath, SSHPort=22
                               │
                               ▼
                    ┌──────────────────────┐
                    │ Orchestrator creates │
                    │ container/pod with   │
                    │ SSH public key       │
                    └──────────┬───────────┘
                               │
                               ▼
                    ┌──────────────────────┐
                    │ configureSSHKey()    │
                    │ waits for container, │
                    │ writes to            │
                    │ /root/.ssh/          │
                    │   authorized_keys    │
                    └──────────────────────┘
```

### Key Storage

| Artifact | Location | Permissions | Notes |
|----------|----------|-------------|-------|
| Private key | `/app/data/ssh-keys/{name}.key` | `0600` | Directory is `0700` |
| Public key | SQLite `instances.ssh_public_key` | — | Never exposed via API (`json:"-"`) |
| Private key path | SQLite `instances.ssh_private_key_path` | — | Never exposed via API (`json:"-"`) |
| Fingerprint | SQLite `instances.ssh_key_fingerprint` | — | SHA256, never exposed via API |
| Authorized keys | Agent `/root/.ssh/authorized_keys` | `0600` | Directory is `0700` |

### Key Rotation

Zero-downtime key rotation avoids any period where authentication is unavailable:

```
1. Generate new ED25519 key pair
2. Append new public key to agent's authorized_keys
   (now both old and new keys are valid)
3. Test SSH connection with new key
4. Remove old public key from authorized_keys via new connection
5. Update database records (key, fingerprint, path, rotation timestamp)
6. Reconnect SSHManager with new key
7. Delete old private key file from disk
```

Automatic rotation runs daily (configurable per-instance, default 90-day policy). Manual rotation is available via the admin API.

## Security Model

### Authentication

- **ED25519 public key authentication only** — passwords disabled
- **Unique key per instance** — compromise of one key does not affect others
- SSH user is `root` (agent containers run as root for systemd)

### Host Key Verification (TOFU)

The control plane uses Trust On First Use for agent host keys:
- First connection: accept and store the host key fingerprint
- Subsequent connections: log a warning if the fingerprint changes

Host key changes are expected (agent containers regenerate keys on restart), so mismatches are logged but do not reject connections.

### Agent SSH Server Hardening

The agent's sshd is configured with defense-in-depth settings:

| Setting | Value | Purpose |
|---------|-------|---------|
| `PasswordAuthentication` | `no` | Key-only auth |
| `PermitRootLogin` | `prohibit-password` | Allow key auth for root |
| `MaxAuthTries` | `3` | Limit brute-force attempts |
| `LoginGraceTime` | `30` | Short authentication window |
| `MaxSessions` | `10` | Limit multiplexed sessions |
| `MaxStartups` | `3:50:6` | Rate-limit unauthenticated connections |
| `AllowTcpForwarding` | `local` | Only local forwarding (for tunnels) |
| `X11Forwarding` | `no` | Disabled |
| `PermitTunnel` | `no` | No L2/L3 tunnels |
| `AllowAgentForwarding` | `no` | Disabled |
| `ClientAliveInterval` | `30` | Server-side keepalive |
| `ClientAliveCountMax` | `3` | 3 missed keepalives = disconnect |

### Connection Security Controls

| Control | Mechanism | Default |
|---------|-----------|---------|
| Rate limiting | Max 10 attempts/minute/instance | 5-minute block after 5 consecutive failures |
| Source IP restrictions | Per-instance allowlist (IPs/CIDRs) | No restriction (all IPs allowed) |
| Connection limits | Configurable max connections | Unlimited |
| Audit logging | All events to `ssh_audit_logs` table | 90-day retention |

### Terminal Session Security

| Control | Value |
|---------|-------|
| Shell whitelist | `/bin/bash`, `/bin/sh`, `/bin/zsh` |
| Input size limit | 64 KB per message |
| Terminal resize cap | 500 columns x 200 rows |
| Rate limiting | 100 messages/sec, burst 200 |
| Idle timeout | 30 minutes (detached sessions auto-close) |

### Input Sanitization

- Log messages pass through `logutil.SanitizeForLog()` to prevent log injection
- Shell arguments use `shellQuote()` (single-quote escaping) for command injection prevention
- File writes use stdin piping, not shell arguments, to avoid argument length limits and injection

### Audit Trail

All SSH operations are recorded in the `ssh_audit_logs` database table with the following event types:

| Event Type | When |
|------------|------|
| `connection_established` | SSH connection succeeds |
| `connection_terminated` | SSH connection closed |
| `connection_failed` | SSH connection attempt fails |
| `command_execution` | Remote command executed |
| `file_operation` | File read/write/list/mkdir |
| `terminal_session_start` | PTY session opened |
| `terminal_session_end` | PTY session closed |
| `key_rotation` | SSH keys rotated |
| `fingerprint_mismatch` | Host key changed |
| `ip_restricted` | Connection blocked by IP allowlist |

## Connection Resilience

See [SSH Resilience](../ssh-resilience.md) for detailed failure scenarios and recovery behavior.

### Summary

The system has three layers of resilience:

1. **SSHManager**: Keepalive every 30s (`keepalive@openssh.com` + `echo ping`). Triggers exponential-backoff reconnection (1s base, 16s max, 10 retries) on failure.
2. **TunnelManager**: Global TCP probe every 60s closes unhealthy tunnels. Per-instance monitor every 10s recreates missing tunnels with backoff (1s base, 60s max).
3. **Maintenance loop** (main.go): Every 60s, ensures tunnels exist for running instances and cleans up stale ones.

### Connection State Machine

```
                ┌──────────────┐
                │ Disconnected │◄───────────────────────┐
                └──────┬───────┘                        │
                       │ Connect()                      │ failure
                ┌──────▼───────┐                        │
                │  Connecting  │────────────────────────►│
                └──────┬───────┘                        │
                       │ success                        │
                ┌──────▼───────┐   keepalive/health     │
                │  Connected   │───────fail──────┐      │
                └──────────────┘                 │      │
                                          ┌──────▼──────┴──┐
                                          │ Reconnecting   │
                                          └──┬──────────┬──┘
                               success ┌─────┘          └─────┐ exhausted
                                ┌──────▼───────┐       ┌──────▼───────┐
                                │  Connected   │       │   Failed     │
                                └──────────────┘       └──────────────┘
```

## Sequence Diagrams

### Instance Creation with SSH Key Setup

```
User            Control Plane          Orchestrator         Agent Container
 │                   │                      │                     │
 │  Create Instance  │                      │                     │
 │──────────────────►│                      │                     │
 │                   │                      │                     │
 │                   │ GenerateKeyPair()    │                     │
 │                   │ (ED25519)            │                     │
 │                   │                      │                     │
 │                   │ SavePrivateKey()     │                     │
 │                   │ → /app/data/ssh-keys/│                     │
 │                   │                      │                     │
 │                   │ Store in DB:         │                     │
 │                   │  public key,         │                     │
 │                   │  fingerprint,        │                     │
 │                   │  key path            │                     │
 │                   │                      │                     │
 │                   │ CreateInstance()     │                     │
 │                   │─────────────────────►│                     │
 │                   │                      │ Create pod/container│
 │                   │                      │────────────────────►│
 │                   │                      │                     │
 │                   │                      │ Wait for running    │
 │                   │                      │ (up to 120s)        │
 │                   │                      │                     │
 │                   │                      │ mkdir /root/.ssh    │
 │                   │                      │────────────────────►│
 │                   │                      │                     │
 │                   │                      │ Write               │
 │                   │                      │ authorized_keys     │
 │                   │                      │────────────────────►│
 │                   │                      │                     │
 │  201 Created      │                      │                     │
 │◄──────────────────│                      │                     │
```

### SSH Tunnel Establishment

```
Control Plane                    SSHManager                   Agent
     │                               │                          │
     │ tunnelMaintenanceLoop         │                          │
     │ detects running instance      │                          │
     │ without tunnels               │                          │
     │                               │                          │
     │ GetClient(instance)           │                          │
     │──────────────────────────────►│                          │
     │                               │                          │
     │ ◄── *ssh.Client ─────────────│                          │
     │                               │                          │
     │ CreateTunnelForVNC()          │                          │
     │                               │                          │
     │ net.Listen("tcp",             │                          │
     │   "127.0.0.1:0")             │                          │
     │ → local port 54321           │                          │
     │                               │                          │
     │         [ Per incoming connection ]                      │
     │                               │                          │
     │ listener.Accept()             │                          │
     │                               │                          │
     │ sshClient.Dial("tcp",        │                          │
     │   "127.0.0.1:3000")          │      SSH channel         │
     │──────────────────────────────────────────────────────────►
     │                               │                          │
     │ ◄── bidirectional io.Copy ──────────────────────────────►│
     │                               │                          │
     │ CreateTunnelForGateway()      │                          │
     │ (same flow for port 8080)     │                          │
     │                               │                          │
     │ Start per-instance monitor    │                          │
     │ (10s health check)            │                          │
```

### SSH Reconnection After Failure

```
SSHManager              Agent
     │                    │
     │  keepalive req     │
     │───────────X        │  (connection broken)
     │                    │
     │  State → Disconnected
     │  Remove dead client
     │  Emit EventDisconnected
     │
     │  State → Reconnecting
     │  Start reconnect goroutine
     │
     │  Attempt 1 (delay: 1s)
     │───────────X        │  (agent still down)
     │
     │  Attempt 2 (delay: 2s)
     │───────────X        │
     │
     │  Attempt 3 (delay: 4s)
     │────────────────────►│  (agent back online)
     │◄────────────────────│
     │
     │  State → Connected
     │  Store new client
     │  Emit EventReconnectSuccess
     │
     │  TunnelManager detects
     │  missing tunnels, recreates
     │
     │  ◄── Tunnels restored ──►│
```

### Key Rotation

```
Admin           Control Plane           Agent
  │                  │                    │
  │ POST rotate-key  │                    │
  │─────────────────►│                    │
  │                  │                    │
  │                  │ Generate new       │
  │                  │ ED25519 key pair   │
  │                  │                    │
  │                  │ Append new pubkey  │
  │                  │ to authorized_keys │
  │                  │───────────────────►│
  │                  │                    │
  │                  │ Test connection    │
  │                  │ with new key       │
  │                  │───────────────────►│
  │                  │◄───────────────────│
  │                  │                    │
  │                  │ Remove old pubkey  │
  │                  │ from authorized_keys│
  │                  │───────────────────►│
  │                  │                    │
  │                  │ Update DB:         │
  │                  │  new key, fingerprint,
  │                  │  rotation timestamp│
  │                  │                    │
  │                  │ Reconnect SSH      │
  │                  │ with new key       │
  │                  │───────────────────►│
  │                  │◄───────────────────│
  │                  │                    │
  │                  │ Delete old key file│
  │                  │                    │
  │  200 OK          │                    │
  │◄─────────────────│                    │
```

## Initialization and Shutdown

### Startup Sequence

1. `sshtunnel.InitGlobal()` — creates global `SSHManager`, `TunnelManager`, `SessionManager`
2. `sshaudit.InitGlobal(db, 90)` — creates global `Auditor` with 90-day retention
3. Background goroutines started:
   - `tunnelMaintenanceLoop` (60s) — ensures tunnels for running instances, cleans stale ones
   - `keyRotationLoop` (24h) — automatic rotation per instance policy
   - `auditRetentionLoop` (24h) — purges expired audit logs

### Shutdown Sequence

1. `TunnelManager.Shutdown()` — stops health checks, stops per-instance monitors, closes all tunnels
2. `SSHManager.CloseAll()` — stops keepalive loop, closes all SSH connections, clears state
3. HTTP server graceful shutdown (10s timeout)

## Performance

SSH tunnels on localhost add minimal overhead:

- **Latency**: ~55 microseconds per HTTP request through tunnel
- **Throughput**: >27,000 requests/sec
- **p99 latency**: ~170 microseconds

The performance gain over previous K8s exec-based file operations is significant: SSH exec completes in 1-5ms on loopback compared to 20-100ms for K8s exec.
