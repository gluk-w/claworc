# SSH Connection Resilience: Expected Behavior

This document describes the expected behavior for each failure scenario handled by the SSH connection monitoring and resilience system.

## Overview

The system provides three layers of resilience:

1. **SSH Connection Manager** (`sshmanager.SSHManager`): Manages SSH connections with keepalive health checks (30s interval) and automatic reconnection with exponential backoff.
2. **Tunnel Manager** (`sshtunnel.TunnelManager`): Monitors tunnel health via TCP probes (60s global, 10s per-instance) and recreates failed tunnels.
3. **Connection State Tracker**: Records state transitions and emits events for observability and debugging.

## Failure Scenarios

### 1. Agent Container Restart

**Trigger:** An agent pod/container restarts while the control plane is running.

**Detection:** The keepalive loop (every 30s) sends `keepalive@openssh.com` requests. When the agent container stops, the TCP connection breaks, and the keepalive request fails.

**Recovery sequence:**
1. Keepalive failure detected → state transitions to `Disconnected`
2. Dead client removed from pool, connection params preserved
3. `EventDisconnected` emitted with "keepalive failed" reason
4. Background reconnection goroutine started → state transitions to `Reconnecting`
5. Exponential backoff retries: 1s, 2s, 4s, 8s, 16s (max), up to 10 attempts
6. When the agent comes back online, connection succeeds → state transitions to `Connected`
7. `EventReconnectSuccess` emitted

**Expected time to recovery:** Depends on agent restart time + reconnect attempt alignment. Best case: ~30s (next keepalive tick) + 1s (first retry). Worst case: ~30s + sum of backoff delays.

### 2. Network Partition

**Trigger:** Network connectivity between control plane and agent is temporarily lost.

**Detection:** Same as agent restart — keepalive or health check (`echo ping`) fails.

**Recovery sequence:** Identical to agent restart. Since the SSH server is still running on the agent side, reconnection succeeds as soon as network connectivity is restored.

**Key difference from restart:** The agent doesn't need to restart its SSH server; only network connectivity needs to be restored.

### 3. Control Plane Restart

**Trigger:** The control plane process restarts (deployment update, crash, etc.).

**Detection:** N/A — the new process starts fresh with no active connections.

**Recovery sequence:**
1. New `SSHManager` created (all maps empty)
2. Application reads known instances from the database
3. For each instance, calls `Connect()` with stored connection parameters
4. Tunnels are recreated via `StartTunnelsForInstance()`

**Important:** Connection parameters (host, port, key path) must be persisted externally (database) since in-memory state is lost on restart. The SSHManager stores params in memory only for reconnection purposes.

### 4. Simultaneous Failure of Multiple Instances

**Trigger:** Multiple agents fail at the same time (e.g., node failure, network switch issue).

**Detection:** Each connection is checked independently in `checkConnections()`.

**Recovery sequence:**
- Each instance gets its own reconnection goroutine via `triggerReconnect()`
- Reconnection is deduplicated per instance (only one goroutine per instance)
- All reconnections run concurrently and independently
- No lock contention between instances (separate map entries)

**Concurrency guarantees:**
- `RWMutex` protects client/metrics/params maps
- Callbacks fire outside locks to prevent deadlocks
- Event logging uses a separate mutex from client operations
- State changes are atomic per-instance

### 5. Permanently Unavailable Agent

**Trigger:** An agent instance is permanently destroyed or unreachable.

**Detection:** All reconnection attempts fail.

**Recovery sequence:**
1. Reconnection attempts exhaust `maxRetries` (default: 10)
2. State transitions to `Failed`
3. `EventReconnectFailed` emitted with "gave up after N attempts"
4. Connection params and metrics are cleaned up (memory freed)
5. No further automatic reconnection is attempted

**Manual intervention required:** An operator must either:
- Restart the agent and manually trigger a new `Connect()`
- Remove the instance from the system

### 6. Tunnel Failure (Local Port Stops Listening)

**Trigger:** A tunnel's local listener crashes or is killed.

**Detection:**
- Global health check (every 60s): TCP probe to local port fails
- Per-instance monitor (every 10s): detects missing tunnel type

**Recovery sequence:**
1. TCP probe fails → tunnel marked unhealthy, then closed
2. Per-instance monitor detects missing VNC or Gateway tunnel
3. If SSH client is available, tunnel is recreated with exponential backoff (1s → 60s max)
4. If SSH client is missing, reconnection is skipped (SSH reconnect handles this)
5. Reconnection counter incremented on success

### 7. SSH Connection Stale (Half-Open)

**Trigger:** The remote side closes the connection but the local TCP stack hasn't detected it yet (half-open connection).

**Detection:** The `echo ping` health check command fails because creating a new SSH session or running the command returns an error.

**Recovery sequence:** Same as network partition — health check failure triggers reconnection.

## State Machine

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

## Event Types

| Event | When Emitted | Details |
|-------|-------------|---------|
| `connected` | SSH connection established | Address connected to |
| `disconnected` | Connection lost or closed | Reason (keepalive/health check/manual) |
| `health_check_failed` | Health check command fails | Error message |
| `reconnecting` | Reconnection attempt starts | Max retries count |
| `reconnect_success` | Reconnection succeeds | Number of attempts taken |
| `reconnect_failed` | All retries exhausted | Total attempts made |

## Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| Keepalive interval | 30s | How often to check SSH connections |
| Health check timeout | 5s | Max time for `echo ping` command |
| Max reconnect retries | 10 | Attempts before giving up |
| Reconnect base delay | 1s | Initial backoff delay |
| Reconnect max delay | 16s | Maximum backoff delay |
| Backoff factor | 2x | Multiplier per attempt |
| Tunnel health check (global) | 60s | TCP probe interval for all tunnels |
| Tunnel health check (per-instance) | 10s | Check interval per instance |
| Tunnel probe timeout | 5s | TCP connection test timeout |
| Tunnel reconnect max delay | 60s | Max delay for tunnel reconnection |
