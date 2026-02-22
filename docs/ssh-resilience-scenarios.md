# SSH Connection Resilience: Expected Failure Behavior

This document describes the expected behavior of the SSH connection management
system under various failure scenarios. Each scenario documents what happens
automatically, what operators should observe, and how the system recovers.

## Architecture Overview

The resilience system consists of three layers:

1. **Protocol-level keepalive** (30s interval) — detects dead TCP connections
2. **Command-level health check** (30s interval) — verifies SSH server responsiveness via `echo ping`
3. **TCP-level tunnel health check** (60s interval) — probes local tunnel listeners

When a failure is detected at any layer, automatic reconnection is triggered
with exponential backoff (1s → 2s → 4s → 8s → 16s cap, up to 10 retries).

## Failure Scenarios

### 1. Agent Container Restart

**Trigger:** Agent container (pod) restarts due to crash, OOM, or manual restart.

**What happens:**
1. The SSH connection dies when the container stops.
2. The keepalive goroutine (30s interval) or health checker detects the dead connection.
3. The connection is removed from the map and state transitions to `Disconnected`.
4. `triggerReconnect` launches an async reconnection goroutine.
5. On each reconnection attempt:
   - The public key is re-uploaded via `ConfigureSSHAccess` (the restarted container has a fresh filesystem, so `/root/.ssh/authorized_keys` is lost).
   - The SSH address is fetched from the orchestrator.
   - A new SSH connection is established.
6. On success, state transitions to `Connected` and tunnels are recreated by the reconciliation loop.

**Key detail:** Public key re-upload before each attempt is critical. Without it, SSH authentication would fail after a container restart.

**Events emitted:** `disconnected` → `health_check_failed` → `reconnecting` → `key_uploaded` → `reconnected`

**State transitions:** Connected → Disconnected → Reconnecting → Connecting → Connected

**Recovery time:** Typically 1-30 seconds after the container is back, depending on which health check interval detects the failure.

### 2. Network Partition

**Trigger:** Network connectivity between control plane and agent is disrupted temporarily.

**What happens:**
1. SSH keepalive packets fail to reach the agent.
2. The keepalive goroutine detects the failure and removes the connection.
3. State transitions to `Disconnected` and `triggerReconnect` is called.
4. Reconnection attempts will fail while the network is down (backoff applies).
5. When the network is restored, the next reconnection attempt succeeds.
6. Existing tunnels become stale (they reference the old SSH client).
7. The tunnel health checker (60s) detects stale tunnels via TCP probe failures.
8. The reconciliation loop (60s) recreates tunnels on the new connection.

**Events emitted:** `disconnected` → `reconnecting` → (repeated `key_uploaded` + failed connect) → `reconnected`

**State transitions:** Connected → Disconnected → Reconnecting → Connected (or → Failed if partition lasts too long)

**Important:** If the partition exceeds the maximum retry window (~3 minutes with default settings), the state transitions to `Failed`. The reconciliation loop will retry on subsequent cycles.

### 3. Control Plane Restart

**Trigger:** The control plane process restarts (deployment update, crash, manual restart).

**What happens:**
1. On startup, SSHManager and TunnelManager are created with empty connection maps — no cached connections.
2. The SSH key pair is loaded from disk (persisted in `$CLAWORC_DATA_PATH`).
3. The background tunnel reconciliation loop starts after a 10-second delay.
4. On first reconcile, for each instance with status "running" in the database:
   - `EnsureConnected` checks for an existing connection (none) and establishes a new one.
   - The public key is uploaded to the agent via `ConfigureSSHAccess`.
   - SSH connection is established.
   - VNC and Gateway tunnels are created.
5. Tunnel ports may differ from the previous run (OS assigns new ports).

**Important:** Agents that were running before the control plane restart do NOT lose their authorized keys (the container didn't restart). The key re-upload is idempotent — writing the same key again is harmless.

**Recovery time:** ~10-70 seconds (10s initial delay + up to 60s reconciliation interval).

### 4. Simultaneous Failure of Multiple Instances

**Trigger:** Multiple agent containers fail at the same time (e.g., node failure, network segment outage).

**What happens:**
1. Each failed instance is detected independently by the health checker.
2. `triggerReconnect` is called for each instance — one reconnection goroutine per instance.
3. Reconnections run concurrently (each instance has its own backoff timer).
4. The `reconnecting` map ensures only one reconnection per instance at a time.
5. Instances recover independently as their agents come back online.

**Key invariants:**
- No deadlocks: the connection mutex (`mu`) and reconnection mutex (`reconnMu`) are always acquired in a consistent order.
- No thundering herd: exponential backoff prevents overwhelming the network or orchestrator.
- Independent recovery: one instance's failure doesn't affect another's reconnection.

**Events emitted:** Each instance emits its own independent event stream.

### 5. Concurrent Reconnection Deduplication

**Trigger:** Multiple triggers (keepalive failure, health check failure, tunnel failure) detect the same dead connection simultaneously.

**What happens:**
1. The first trigger calls `triggerReconnect(instanceID, reason)`.
2. `triggerReconnect` acquires `reconnMu`, checks if a reconnection is already in progress for this instance.
3. If no reconnection in progress: a goroutine is spawned and tracked in the `reconnecting` map.
4. Subsequent triggers for the same instance are silently dropped (logged at debug level).
5. When the reconnection completes (success or failure), it removes itself from the `reconnecting` map.

**Invariant:** At most one reconnection goroutine per instance at any time.

### 6. Permanent SSH Unavailability

**Trigger:** An agent is permanently unreachable (deleted, misconfigured, firewall blocking).

**What happens:**
1. Reconnection is attempted with exponential backoff up to `maxRetries` (default: 10).
2. Each attempt calls `ConfigureSSHAccess` (may fail if orchestrator can't reach the container).
3. After all retries are exhausted:
   - State transitions to `Failed`.
   - `EventReconnectFailed` is emitted with details.
   - The reconnection goroutine exits.
4. The instance is NOT automatically retried — the reconciliation loop uses its own independent backoff (5s → 5min cap) to periodically retry.
5. Other instances are completely unaffected.

**Events emitted:** `disconnected` → `reconnecting` → `reconnect_failed`

**State transitions:** Connected → Disconnected → Reconnecting → Failed

**Operator action:** Check the instance's SSH events endpoint (`GET /api/v1/instances/{id}/ssh-events`) for failure details. The `reconnect_failed` event includes the error message.

## Monitoring and Debugging

### API Endpoints

| Endpoint | Description |
|----------|-------------|
| `GET /api/v1/instances/{id}/ssh-status` | Current connection state, health metrics, active tunnels, recent state transitions |
| `GET /api/v1/instances/{id}/ssh-events` | Last 100 connection events per instance (ring buffer) |

### Connection States

| State | Meaning |
|-------|---------|
| `disconnected` | No SSH connection exists |
| `connecting` | SSH handshake in progress |
| `connected` | SSH connection established and healthy |
| `reconnecting` | Automatic reconnection in progress (after failure) |
| `failed` | Reconnection retries exhausted |

### Event Types

| Event | Meaning |
|-------|---------|
| `connected` | SSH connection successfully established |
| `disconnected` | SSH connection lost (keepalive or health check failure) |
| `reconnecting` | Automatic reconnection started |
| `reconnected` | Automatic reconnection succeeded |
| `reconnect_failed` | All reconnection retries exhausted |
| `key_uploaded` | Public key uploaded via ConfigureSSHAccess |
| `health_check_failed` | `echo ping` command failed over SSH |

### Timing Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| Keepalive interval | 30s | Protocol-level keepalive probes |
| Health check interval | 30s | `echo ping` command checks |
| Health check timeout | 5s | Max wait for health check command |
| Tunnel health interval | 60s | TCP probe for tunnel listeners |
| Reconnect initial backoff | 1s | First retry delay |
| Reconnect max backoff | 16s | Maximum retry delay |
| Reconnect max retries | 10 | Attempts before giving up |
| Tunnel reconcile interval | 60s | Background tunnel reconciliation |
| Tunnel reconcile initial delay | 10s | Delay before first reconciliation |
| Tunnel backoff initial | 5s | First tunnel retry delay |
| Tunnel backoff max | 5min | Maximum tunnel retry delay |
