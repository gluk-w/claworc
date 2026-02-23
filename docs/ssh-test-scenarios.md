# SSH Proxy Manual Test Scenarios

This document describes manual test scenarios for the SSH proxy system, covering SSH connection establishment, tunnel creation and management, instance lifecycle interactions, and edge cases.

## Prerequisites

- A running Claworc control plane (Docker or Kubernetes mode)
- At least one agent instance created via the dashboard or API
- Access to the Claworc API (default: `http://localhost:8080`)
- `curl` or similar HTTP client for API calls

## 1. SSH Connection Establishment Using Instance ID

### 1.1 Successful SSH Connection to a Running Instance

**Steps:**
1. Create and start an instance via the API:
   ```
   POST /api/v1/instances
   {"name": "test-ssh", "config": {}}
   ```
2. Wait for the instance to reach "running" status:
   ```
   GET /api/v1/instances
   ```
3. Note the instance ID from the response.
4. Test SSH connectivity:
   ```
   GET /api/v1/instances/{id}/ssh-test
   ```

**Expected Result:**
- Response status: 200 OK
- Body contains `"status": "ok"`, `"output": "SSH test successful\n"`, and a positive `"latency_ms"` value.
- The control plane auto-generated an ED25519 key pair (if first connection), uploaded the public key to the agent, and established an SSH session.

### 1.2 SSH Connection Reuse (Cached Connection)

**Steps:**
1. With an instance already connected (from 1.1), call the SSH test endpoint again:
   ```
   GET /api/v1/instances/{id}/ssh-test
   ```
2. Compare latency with the first call.

**Expected Result:**
- Response status: 200 OK with `"status": "ok"`.
- Latency should be noticeably lower than the first call because the SSH connection is reused (no key upload or handshake needed).

### 1.3 SSH Reconnection After Connection Loss

**Steps:**
1. Establish an SSH connection via the ssh-test endpoint.
2. Restart the agent instance:
   ```
   POST /api/v1/instances/{id}/restart
   ```
3. Wait for the instance to reach "running" status again.
4. Call the SSH test endpoint:
   ```
   GET /api/v1/instances/{id}/ssh-test
   ```

**Expected Result:**
- The first call after restart may take longer (new SSH handshake required).
- Response eventually returns `"status": "ok"` — the system automatically re-uploads the key and reconnects.

### 1.4 SSH Connection to a Stopped Instance

**Steps:**
1. Stop an instance:
   ```
   POST /api/v1/instances/{id}/stop
   ```
2. Attempt an SSH test:
   ```
   GET /api/v1/instances/{id}/ssh-test
   ```

**Expected Result:**
- Response status: 200 OK with `"status": "error"`.
- The `"error"` field describes a connection failure (the orchestrator cannot resolve an SSH address for a stopped instance).
- `"latency_ms"` reflects the time spent attempting.

## 2. Tunnel Creation and Management Using Instance ID

### 2.1 Automatic Tunnel Creation on Instance Start

**Steps:**
1. Start an instance and wait for "running" status.
2. Wait at least 70 seconds (background manager initial delay of 10s + reconcile interval of 60s).
3. Query tunnel status:
   ```
   GET /api/v1/instances/{id}/tunnels
   ```

**Expected Result:**
- Response contains a `"tunnels"` array with two entries:
  - VNC tunnel: `"label": "VNC"`, `"remote_port": 3000`, `"status": "active"`, `"type": "reverse"`
  - Gateway tunnel: `"label": "Gateway"`, `"remote_port": 18789`, `"status": "active"`, `"type": "reverse"`
- Each tunnel has a non-zero `"local_port"` (auto-assigned by OS).
- `"last_check"` timestamps are recent.

### 2.2 Tunnel Port Uniqueness

**Steps:**
1. Start two instances and wait for tunnels to be created.
2. Query tunnel status for both instances:
   ```
   GET /api/v1/instances/{id1}/tunnels
   GET /api/v1/instances/{id2}/tunnels
   ```

**Expected Result:**
- All four tunnels (2 per instance) have unique `"local_port"` values.
- No port conflicts between instances.

### 2.3 Tunnel Idempotency

**Steps:**
1. With tunnels already running for an instance, wait for the background manager to run another reconciliation cycle (60s).
2. Query tunnel status again:
   ```
   GET /api/v1/instances/{id}/tunnels
   ```

**Expected Result:**
- The same two tunnels exist (no duplicates created).
- Local ports remain the same as before.
- Status remains `"active"`.

### 2.4 VNC Access Through Tunnel

**Steps:**
1. Note the VNC tunnel's `"local_port"` from the tunnel status endpoint.
2. Open a browser or VNC client and connect to `http://localhost:{local_port}`.

**Expected Result:**
- The VNC/Selkies web interface loads, showing the agent's desktop.
- The connection is routed through the SSH tunnel to the agent's port 3000.

### 2.5 Tunnel Status After Agent Service Restart

**Steps:**
1. With active tunnels, restart a service inside the agent (e.g., VNC service) without restarting the whole instance.
2. Query tunnel status:
   ```
   GET /api/v1/instances/{id}/tunnels
   ```

**Expected Result:**
- Tunnels remain `"active"` since the SSH connection is unaffected by internal agent service restarts.
- VNC access is temporarily unavailable until the VNC service restarts, but the tunnel itself stays up.

## 3. Instance Lifecycle with SSH Cleanup

### 3.1 Stop Instance — Tunnel Cleanup

**Steps:**
1. Start an instance and verify tunnels are active.
2. Note the local ports of the tunnels.
3. Stop the instance:
   ```
   POST /api/v1/instances/{id}/stop
   ```
4. Query tunnel status:
   ```
   GET /api/v1/instances/{id}/tunnels
   ```
5. Verify the local ports are freed:
   ```
   lsof -i :{local_port}
   ```

**Expected Result:**
- Tunnel status returns an empty array (`"tunnels": []`).
- The local ports are no longer bound (lsof returns no results).
- No orphaned listeners remain.

### 3.2 Delete Instance — Full Cleanup

**Steps:**
1. Start an instance and verify tunnels are active.
2. Delete the instance:
   ```
   DELETE /api/v1/instances/{id}
   ```
3. Verify the instance no longer exists:
   ```
   GET /api/v1/instances/{id}
   ```
4. Check that local ports are freed.

**Expected Result:**
- Instance returns 404.
- All tunnels are cleaned up; local ports are released.
- No SSH connections remain for the deleted instance ID.

### 3.3 Restart Instance — Tunnel Recreation

**Steps:**
1. Start an instance and verify tunnels are active. Note the local ports.
2. Restart the instance:
   ```
   POST /api/v1/instances/{id}/restart
   ```
3. Wait for the instance to reach "running" status.
4. Wait for background manager reconciliation (up to 70s).
5. Query tunnel status:
   ```
   GET /api/v1/instances/{id}/tunnels
   ```

**Expected Result:**
- Tunnels were stopped during restart (old local ports released).
- New tunnels are created by the background manager with potentially different local ports.
- Both VNC and Gateway tunnels show `"status": "active"`.

### 3.4 Start Instance — Deferred Tunnel Creation

**Steps:**
1. Start a previously stopped instance:
   ```
   POST /api/v1/instances/{id}/start
   ```
2. Immediately query tunnels:
   ```
   GET /api/v1/instances/{id}/tunnels
   ```
3. Wait for background reconciliation and query again.

**Expected Result:**
- Immediately after start: tunnels array may be empty (start handler does not create tunnels).
- After background reconciliation: VNC and Gateway tunnels appear with `"status": "active"`.
- This confirms tunnel creation is handled by the background manager, not the start handler.

## 4. Edge Cases and Error Scenarios

### 4.1 Instance Not Found

**Steps:**
1. Call the SSH test endpoint with a non-existent instance ID:
   ```
   GET /api/v1/instances/99999/ssh-test
   ```

**Expected Result:**
- Response status: 404 Not Found.
- Body contains an error message indicating the instance was not found.

### 4.2 Invalid Instance ID Format

**Steps:**
1. Call the SSH test endpoint with a non-numeric ID:
   ```
   GET /api/v1/instances/abc/ssh-test
   ```

**Expected Result:**
- Response status: 400 Bad Request.
- Body contains an error about invalid instance ID.

### 4.3 SSH Manager Not Initialized

**Steps:**
1. If testing in a degraded environment where SSH key generation failed at startup, call:
   ```
   GET /api/v1/instances/{id}/ssh-test
   ```

**Expected Result:**
- Response status: 503 Service Unavailable.
- Body indicates that the SSH manager is not available.

### 4.4 Tunnel Manager Not Initialized

**Steps:**
1. Query tunnels when the tunnel manager is not initialized:
   ```
   GET /api/v1/instances/{id}/tunnels
   ```

**Expected Result:**
- Response contains `"tunnels": []` with an `"error"` field explaining the tunnel manager is unavailable.

### 4.5 SSH Connection Failure — Agent Not Ready

**Steps:**
1. Create an instance and immediately (before it's fully running) call:
   ```
   GET /api/v1/instances/{id}/ssh-test
   ```

**Expected Result:**
- Response status: 200 OK with `"status": "error"`.
- The error describes a connection failure (SSH server not yet listening or key upload failed).
- Retrying after the instance is fully running should succeed.

### 4.6 Tunnel Query for Instance With No Tunnels

**Steps:**
1. Create an instance but don't start it.
2. Query tunnels:
   ```
   GET /api/v1/instances/{id}/tunnels
   ```

**Expected Result:**
- Response contains `"tunnels": []` (empty array, no error).

### 4.7 Concurrent SSH Tests on Multiple Instances

**Steps:**
1. Start 3+ instances and wait for them to be running.
2. Simultaneously call the SSH test endpoint for all instances (use parallel curl or a script).

**Expected Result:**
- All requests return `"status": "ok"` independently.
- No cross-instance interference — each instance has its own SSH connection.
- The SSHManager's RWMutex ensures thread-safe access.

### 4.8 Rapid Stop/Start Cycle

**Steps:**
1. With an instance running and tunnels active, rapidly:
   ```
   POST /api/v1/instances/{id}/stop
   POST /api/v1/instances/{id}/start
   ```
2. Wait for the instance to be running and background reconciliation to complete.
3. Query tunnel status.

**Expected Result:**
- Stop cleans up existing tunnels.
- After start and reconciliation, new tunnels are created.
- No stale tunnels or port leaks from the rapid transition.

### 4.9 Keepalive Detection of Dead Connection

**Steps:**
1. Establish an SSH connection to a running instance.
2. Force-kill the SSH server process inside the agent container (without stopping the instance).
3. Wait 30–60 seconds for the keepalive to detect the dead connection.
4. Call the SSH test endpoint:
   ```
   GET /api/v1/instances/{id}/ssh-test
   ```

**Expected Result:**
- The keepalive goroutine (30s interval) detects the dead connection and removes it from the connection map.
- The SSH test triggers a fresh `EnsureConnected` flow, re-uploading the key and establishing a new connection.
- Response returns `"status": "ok"` after successful reconnection.

### 4.10 Access Control — Forbidden Instance

**Steps:**
1. As a non-admin user without assignment to the instance, call:
   ```
   GET /api/v1/instances/{id}/ssh-test
   ```

**Expected Result:**
- Response status: 403 Forbidden.
- SSH connection is never attempted — access is checked before any SSH operations.
