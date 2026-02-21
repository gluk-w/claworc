---
type: reference
title: SSH Operations Runbook
created: 2026-02-21
tags:
  - operations
  - ssh
  - runbook
  - troubleshooting
related:
  - "[[ssh-connectivity]]"
  - "[[ssh-resilience]]"
  - "[[security-test-results]]"
---

# SSH Operations Runbook

This runbook provides operational procedures for managing SSH connectivity between the Claworc control plane and agent instances. It covers monitoring, troubleshooting, key management, and performance tuning.

> **Prerequisites:** All API calls require authentication. Admin-only endpoints are noted. Replace `{id}` with the instance ID and `{base}` with your Claworc URL (e.g., `http://localhost:8000`).

---

## Table of Contents

1. [Monitoring SSH Connection Health](#1-monitoring-ssh-connection-health)
2. [Troubleshooting Connection Failures](#2-troubleshooting-connection-failures)
3. [Manual SSH Key Rotation](#3-manual-ssh-key-rotation)
4. [Debugging Tunnel Issues](#4-debugging-tunnel-issues)
5. [Common Error Messages and Resolutions](#5-common-error-messages-and-resolutions)
6. [Accessing SSH Audit Logs](#6-accessing-ssh-audit-logs)
7. [Performance Tuning Recommendations](#7-performance-tuning-recommendations)
8. [Decision Trees for Common Scenarios](#8-decision-trees-for-common-scenarios)

---

## 1. Monitoring SSH Connection Health

### Global SSH Dashboard

Get an overview of all SSH connections across all instances:

```bash
curl -s -H "Cookie: session=<token>" {base}/api/v1/ssh-status | jq .
```

Response includes per-instance connection state, health status, and tunnel info.

### Per-Instance SSH Status

Check connection details for a specific instance:

```bash
curl -s -H "Cookie: session=<token>" {base}/api/v1/instances/{id}/ssh-status | jq .
```

**Response fields:**
- `state` — One of: `disconnected`, `connecting`, `connected`, `reconnecting`, `failed`
- `healthy` — Boolean; `true` if last health check passed
- `connected_at` — Timestamp of connection establishment
- `last_health_check` — Timestamp of most recent health check
- `successful_checks` / `failed_checks` — Cumulative counters
- `uptime` — Duration since connection was established
- `tunnels` — List of active tunnels with health info
- `recent_events` — Last connection events

### SSH Metrics

Get aggregated metrics for all accessible instances:

```bash
curl -s -H "Cookie: session=<token>" {base}/api/v1/ssh-metrics | jq .
```

### Connection Events

View recent connection events (connects, disconnects, health failures, reconnections):

```bash
curl -s -H "Cookie: session=<token>" \
  "{base}/api/v1/instances/{id}/ssh-events?limit=50" | jq .
```

### Health Check Internals

The SSH health monitoring operates on two tiers:

1. **Keepalive requests** — Sent every 30 seconds via `keepalive@openssh.com` SSH request
2. **Command-based health check** — Executes `echo ping` with a 5-second timeout

If a keepalive fails, the manager triggers a full health check. If the full check also fails, reconnection is initiated.

### What to Watch For

| Metric | Healthy | Investigate |
|--------|---------|-------------|
| `state` | `connected` | `reconnecting`, `failed`, `disconnected` |
| `healthy` | `true` | `false` (check `failed_checks` counter) |
| `failed_checks` | 0 or low | Increasing over time |
| `last_health_check` | Recent (within 60s) | Stale (several minutes old) |
| Tunnel `healthy` | `true` | `false` with `last_error` set |

---

## 2. Troubleshooting Connection Failures

### Test SSH Connectivity

Run an on-demand SSH connection test:

```bash
curl -s -X POST -H "Cookie: session=<token>" \
  {base}/api/v1/instances/{id}/ssh-test | jq .
```

This performs a full connection test including:
- SSH endpoint resolution
- Key fingerprint validation
- IP restriction check
- Actual SSH connection attempt

### Force Reconnection

If an instance is stuck in a bad state, force a reconnection:

```bash
curl -s -X POST -H "Cookie: session=<token>" \
  {base}/api/v1/instances/{id}/ssh-reconnect | jq .
```

This disconnects the existing connection (if any) and establishes a new one with fresh parameters.

### Check Instance Is Running

Before troubleshooting SSH, verify the instance container/pod is actually running:

```bash
# Kubernetes
kubectl get pods -n claworc -l app=bot-{name}

# Docker
docker ps --filter "name=bot-{name}"
```

### Check Agent SSH Server

If the instance is running but SSH fails, verify sshd is running on the agent:

```bash
# Kubernetes
kubectl exec -n claworc deploy/bot-{name} -- systemctl status sshd

# Docker
docker exec bot-{name} systemctl status sshd
```

### Check Network Connectivity

Verify the control plane can reach the agent on port 22:

```bash
# Kubernetes (from within the cluster)
kubectl run -n claworc debug --rm -it --image=busybox -- \
  nc -zv bot-{name}.claworc.svc.cluster.local 22

# Docker
docker exec claworc-control-plane nc -zv bot-{name} 22
```

### Connection State Machine

```
Disconnected ──→ Connecting ──→ Connected
     ↑                              │
     │                              │ (health check failure)
     │                              ↓
     └─────── Failed ←── Reconnecting
              (retries              │
               exhausted)           │ (success)
                                    ↓
                                Connected
```

- **Reconnection**: Exponential backoff from 1s to 16s, up to 10 retries
- **Failed state**: Manual intervention required (reconnect API or fix underlying issue)

---

## 3. Manual SSH Key Rotation

### Rotate Keys via API

**Admin only.** Triggers zero-downtime key rotation for an instance:

```bash
curl -s -X POST -H "Cookie: session=<token>" \
  {base}/api/v1/instances/{id}/rotate-ssh-key | jq .
```

**Response:**
```json
{
  "success": true,
  "fingerprint": "SHA256:abc123...",
  "rotated_at": "2026-02-21T10:30:00Z",
  "message": "SSH key rotated successfully"
}
```

### Zero-Downtime Rotation Process

1. New ED25519 key pair generated
2. New public key appended to agent's `authorized_keys` (both keys valid temporarily)
3. Connection tested with the new key
4. Old public key removed from `authorized_keys` via the new connection
5. Database updated with new fingerprint
6. Old private key file deleted
7. SSH connection re-established with the new key
8. Tunnels restarted

### Check Key Fingerprint

Verify the current key fingerprint for an instance:

```bash
curl -s -H "Cookie: session=<token>" \
  {base}/api/v1/instances/{id}/ssh-fingerprint | jq .
```

### Automatic Key Rotation

A daily background job checks all instances against their `KeyRotationPolicy` (configurable in days per instance). When the key age exceeds the policy threshold, rotation is triggered automatically.

- Default automatic rotation policy: Disabled (0 days = no auto-rotation)
- Rotation loop runs once per day with a 1-minute initial delay after startup

### Key Storage Locations

| Item | Location | Permissions |
|------|----------|-------------|
| Private keys | `/app/data/ssh-keys/{name}.key` | `0600` |
| Public keys | Database (`ssh_public_key` field) | N/A |
| Fingerprints | Database (`ssh_key_fingerprint` field) | N/A |
| Agent authorized_keys | `/root/.ssh/authorized_keys` on agent | `0600` |

### Troubleshooting Key Rotation

If rotation fails:

1. **Check instance has active SSH connection** — Rotation requires an existing connection to append the new key
2. **Check `authorized_keys` on agent** — May have both old and new keys if rotation was interrupted:
   ```bash
   kubectl exec -n claworc deploy/bot-{name} -- cat /root/.ssh/authorized_keys
   ```
3. **Verify file permissions** — Private key must be `0600`:
   ```bash
   ls -la /app/data/ssh-keys/{name}.key
   ```
4. **Check audit logs** — Look for `key_rotation` events for the instance

---

## 4. Debugging Tunnel Issues

### View Active Tunnels

List all tunnels for an instance:

```bash
curl -s -H "Cookie: session=<token>" \
  {base}/api/v1/instances/{id}/tunnels | jq .
```

**Response fields per tunnel:**
- `service` — `vnc`, `gateway`, or `custom`
- `type` — `reverse`
- `local_port` — Ephemeral port on control plane
- `remote_port` — Agent port (3000 for VNC, 8080 for Gateway)
- `status` — `active` or `unhealthy`
- `started_at` — When tunnel was established
- `last_check` — Last health probe timestamp
- `last_error` — Error string if unhealthy
- `bytes_transferred` — Cumulative data through the tunnel

### Tunnel Health Monitoring

Tunnels are health-checked at two levels:

1. **Global check** — Every 60 seconds, TCP probes each tunnel's local port
2. **Per-instance monitor** — Every 10 seconds, verifies expected tunnels exist and are healthy

If a tunnel fails its TCP probe:
- Tunnel is closed
- Per-instance monitor recreates it with exponential backoff (1s–60s)

### Manual Tunnel Verification

Test if a tunnel's local port is accepting connections:

```bash
# Check VNC tunnel
nc -zv localhost {local_port}

# Test HTTP through Gateway tunnel
curl -s http://localhost:{local_port}/health
```

### Standard Tunnel Configuration

| Service | Agent Port | Purpose |
|---------|------------|---------|
| VNC | 3000 | noVNC web desktop access |
| Gateway | 8080 | OpenClaw web interface |

### Common Tunnel Issues

**Tunnel shows as unhealthy:**
- The remote service on the agent may not be running
- Check the agent service: `kubectl exec -n claworc deploy/bot-{name} -- systemctl status {service}`

**Tunnel not created:**
- Instance must have an active SSH connection first
- Check SSH status: the tunnel manager only creates tunnels for connected instances
- Verify the maintenance loop is running (check control plane logs for "tunnel maintenance" entries)

**Tunnel port not accessible:**
- The tunnel binds to `127.0.0.1` on the control plane
- Ensure you're connecting from the control plane host, not externally

---

## 5. Common Error Messages and Resolutions

### Connection Errors

| Error Message | Cause | Resolution |
|---------------|-------|------------|
| `SSH manager not initialized` | Control plane SSH subsystem not started | Check control plane startup logs; restart the control plane |
| `No SSH connection for instance: <err>` | No active SSH connection to the instance | Check instance is running; try force reconnect |
| `connect: maximum connections (N) reached` | Connection pool exhausted | Wait for connections to free up; check for connection leaks |
| `connect: context cancelled` | Connection attempt timed out or was cancelled | Check network connectivity to agent |

### Rate Limiting Errors

| Error Message | Cause | Resolution |
|---------------|-------|------------|
| `rate limit exceeded for <name>: N connection attempts in the last minute (max 10)` | Too many connection attempts | Wait 1 minute for the window to reset |
| `connection blocked for <name> due to N consecutive failures; retry after <time>` | 5+ consecutive failures triggered a 5-minute block | Wait for block to expire; investigate root cause |

### IP Restriction Errors

| Error Message | Cause | Resolution |
|---------------|-------|------------|
| `connection blocked: source IP <ip> is not in the allowed list` | Source IP not in instance's AllowedSourceIPs | Update allowed IPs via API or remove restriction |

### Key/Authentication Errors

| Error Message | Cause | Resolution |
|---------------|-------|------------|
| `SSH key fingerprint mismatch: expected X, got Y` | Key doesn't match stored fingerprint (possible MITM or agent restart) | Verify agent identity; rotate key if legitimate change |
| `Instance has no SSH key configured` | Instance missing SSH key in database | Recreate instance or manually generate and assign a key |
| `Key rotation failed: <err>` | Rotation process encountered an error | Check audit logs; verify SSH connection; retry |
| `Key rotation succeeded but failed to update database` | Key was rotated on agent but DB update failed | **Critical**: New key is active but not recorded; manual DB intervention needed |

### Tunnel Errors

| Error Message | Cause | Resolution |
|---------------|-------|------------|
| `TCP probe to <addr> failed` | Tunnel local port not responding | Check agent service; tunnel will auto-recreate |
| `no <service> tunnel found for instance <name>` | Expected tunnel doesn't exist | Check SSH connection; wait for maintenance loop (60s) |

---

## 6. Accessing SSH Audit Logs

### Query Audit Logs

**Admin only.** Retrieve audit logs with optional filters:

```bash
# All recent events
curl -s -H "Cookie: session=<token>" \
  "{base}/api/v1/ssh-audit-logs?limit=100" | jq .

# Filter by instance
curl -s -H "Cookie: session=<token>" \
  "{base}/api/v1/ssh-audit-logs?instance_name=my-instance&limit=50" | jq .

# Filter by event type
curl -s -H "Cookie: session=<token>" \
  "{base}/api/v1/ssh-audit-logs?event_type=connection_failed&limit=50" | jq .

# Filter by time range
curl -s -H "Cookie: session=<token>" \
  "{base}/api/v1/ssh-audit-logs?since=2026-02-20T00:00:00Z&until=2026-02-21T00:00:00Z" | jq .

# Filter by username
curl -s -H "Cookie: session=<token>" \
  "{base}/api/v1/ssh-audit-logs?username=admin&limit=50" | jq .

# Combine filters with pagination
curl -s -H "Cookie: session=<token>" \
  "{base}/api/v1/ssh-audit-logs?instance_id=5&event_type=key_rotation&limit=20&offset=0" | jq .
```

### Audit Event Types

| Event Type | Description | Typical Details |
|------------|-------------|-----------------|
| `connection_established` | SSH connection opened | Host, port, fingerprint |
| `connection_terminated` | SSH connection closed | Reason, duration |
| `connection_failed` | Connection attempt failed | Error message |
| `command_execution` | Command run via SSH exec | Command string, exit code |
| `file_operation` | File read/write via SSH | File path, operation type |
| `terminal_session_start` | Interactive terminal opened | Shell type, terminal size |
| `terminal_session_end` | Interactive terminal closed | Duration, reason |
| `key_rotation` | SSH key was rotated | New fingerprint |
| `fingerprint_mismatch` | Key fingerprint didn't match stored value | Expected vs. actual fingerprint |
| `ip_restricted` | Connection blocked by IP restriction | Blocked IP, allowed list |

### Audit Entry Fields

Each entry contains:
- `id` — Unique entry ID
- `instance_id` / `instance_name` — Instance identifiers
- `event_type` — Event category (see table above)
- `username` — User who triggered the event
- `source_ip` — Request origin IP (extracted from X-Forwarded-For, X-Real-Ip, or RemoteAddr)
- `details` — Human-readable event description
- `duration_ms` — Operation duration in milliseconds
- `created_at` — Event timestamp

### Audit Log Retention

- **Default retention:** 90 days
- **Automatic purge:** Daily background job removes entries older than retention period
- **Manual purge:**

```bash
# Purge using default retention (90 days)
curl -s -X POST -H "Cookie: session=<token>" \
  {base}/api/v1/ssh-audit-logs/purge | jq .

# Purge entries older than 30 days
curl -s -X POST -H "Cookie: session=<token>" \
  "{base}/api/v1/ssh-audit-logs/purge?days=30" | jq .
```

**Response:**
```json
{
  "deleted": 1523,
  "retention_days": 30
}
```

### Forensic Investigation Queries

**Find all failed connections for an instance in the last 24 hours:**
```bash
curl -s -H "Cookie: session=<token>" \
  "{base}/api/v1/ssh-audit-logs?instance_name=my-bot&event_type=connection_failed&since=$(date -u -v-1d +%Y-%m-%dT%H:%M:%SZ)" | jq .
```

**Find all key rotations:**
```bash
curl -s -H "Cookie: session=<token>" \
  "{base}/api/v1/ssh-audit-logs?event_type=key_rotation&limit=100" | jq .
```

**Find all IP restriction blocks:**
```bash
curl -s -H "Cookie: session=<token>" \
  "{base}/api/v1/ssh-audit-logs?event_type=ip_restricted&limit=100" | jq .
```

**Find all fingerprint mismatches (potential MITM indicators):**
```bash
curl -s -H "Cookie: session=<token>" \
  "{base}/api/v1/ssh-audit-logs?event_type=fingerprint_mismatch&limit=100" | jq .
```

---

## 7. Performance Tuning Recommendations

### Connection Tuning

| Parameter | Default | Description | Recommendation |
|-----------|---------|-------------|----------------|
| Keepalive interval | 30s | Time between SSH keepalive probes | Lower to 15s for faster failure detection in high-reliability environments |
| Health check timeout | 5s | Timeout for `echo ping` health check | Increase to 10s on high-latency networks |
| Max reconnect retries | 10 | Attempts before marking as failed | Increase for unreliable networks; decrease for faster failure reporting |
| Reconnect base delay | 1s | Initial backoff delay | Appropriate for most scenarios |
| Reconnect max delay | 16s | Maximum backoff delay | Increase to 30-60s if network recovery is slow |

### Tunnel Tuning

| Parameter | Default | Description | Recommendation |
|-----------|---------|-------------|----------------|
| Global health check interval | 60s | TCP probe frequency for all tunnels | Lower to 30s if tunnel failures must be detected quickly |
| Per-instance monitor interval | 10s | Tunnel existence check per instance | Appropriate for most scenarios |
| Tunnel probe timeout | 5s | TCP probe timeout | Increase on congested networks |
| Tunnel reconnect max delay | 60s | Maximum backoff for tunnel recreation | Appropriate for most scenarios |

### Key Management Tuning

| Parameter | Default | Description | Recommendation |
|-----------|---------|-------------|----------------|
| Key rotation policy | 0 (disabled) | Days between automatic rotations | Set to 90 days for compliance environments |
| Audit log retention | 90 days | Days to keep audit entries | Increase to 365 days for regulated environments |

### Resource Considerations

- **SSH multiplexing**: All tunnels and sessions share a single SSH connection per instance, minimizing resource usage
- **Memory**: Each active tunnel uses a small goroutine pair for bidirectional copy plus a counting wrapper
- **File descriptors**: One TCP socket per tunnel local port plus the SSH connection socket per instance
- **Disk**: Private keys are ~400 bytes each; audit logs grow based on activity volume

### Benchmark Reference

Under test conditions (loopback network):
- **Latency**: ~55 microseconds per HTTP request through tunnel
- **Throughput**: >27,000 requests/second
- **p99 latency**: ~170 microseconds
- **SSH exec**: 1–5ms (vs. 20–100ms for Kubernetes exec API)

Production numbers will vary based on network latency between control plane and agent.

---

## 8. Decision Trees for Common Scenarios

### Instance Not Accessible via VNC or Gateway

```
Instance not accessible?
├── Check instance status (is pod/container running?)
│   ├── Not running → Start the instance
│   └── Running → Continue
├── Check SSH status: GET /instances/{id}/ssh-status
│   ├── state = "disconnected" or "failed"
│   │   ├── Test connection: POST /instances/{id}/ssh-test
│   │   │   ├── IP restricted → Update allowed IPs
│   │   │   ├── Key auth failed → Rotate key or check authorized_keys
│   │   │   ├── Connection refused → Check agent sshd: systemctl status sshd
│   │   │   └── Timeout → Check network: nc -zv <agent-host> 22
│   │   └── Force reconnect: POST /instances/{id}/ssh-reconnect
│   ├── state = "connected" but healthy = false
│   │   ├── Check failed_checks counter
│   │   │   ├── Increasing → Agent may be overloaded
│   │   │   └── Stable → Transient issue; monitor
│   │   └── Force reconnect if persistent
│   └── state = "connected" and healthy = true → Check tunnels
├── Check tunnels: GET /instances/{id}/tunnels
│   ├── No tunnels listed → Wait for maintenance loop (60s) or reconnect
│   ├── Tunnel unhealthy
│   │   ├── Check last_error field
│   │   ├── Check agent service (VNC on 3000, Gateway on 8080)
│   │   └── Tunnel will auto-recreate; wait and re-check
│   └── Tunnel healthy → Issue is elsewhere (check frontend proxy)
```

### SSH Connection Keeps Dropping

```
Connection keeps dropping?
├── Check connection events: GET /instances/{id}/ssh-events
│   ├── Frequent "health_check_failed" events
│   │   ├── Agent overloaded → Check agent resource usage
│   │   ├── Network instability → Check link between control plane and agent
│   │   └── Agent sshd crashing → Check agent logs: journalctl -u sshd
│   ├── Frequent "disconnected" → "reconnecting" cycles
│   │   ├── Agent container restarting → Check pod restart count
│   │   └── Network partition → Check network infrastructure
│   └── "rate_limited" events
│       └── Too many reconnect attempts → Wait for cooldown; investigate root cause
├── Check audit logs for patterns
│   └── GET /ssh-audit-logs?instance_name={name}&event_type=connection_terminated
├── Check agent sshd config
│   └── Verify ClientAliveInterval (30s) and ClientAliveCountMax (3)
└── Consider increasing keepalive interval if network is slow
```

### Key Authentication Failing

```
Key auth failing?
├── Check if instance has SSH key configured
│   ├── No key → Instance may need recreation
│   └── Has key → Continue
├── Check fingerprint: GET /instances/{id}/ssh-fingerprint
│   ├── Fingerprint mismatch in audit logs
│   │   ├── Agent was recreated → Rotate key to re-sync
│   │   ├── Possible MITM → Investigate before proceeding
│   │   └── Key file corrupted → Rotate key
│   └── No mismatch → Continue
├── Check private key file exists and has correct permissions
│   ├── ls -la /app/data/ssh-keys/{name}.key
│   ├── Missing → Key was deleted; rotate to regenerate
│   └── Wrong permissions → chmod 0600 /app/data/ssh-keys/{name}.key
├── Check authorized_keys on agent
│   ├── kubectl exec ... -- cat /root/.ssh/authorized_keys
│   ├── Empty or wrong key → Re-deploy instance or manually add key
│   └── Multiple keys (rotation interrupted) → Rotate to clean up
└── Rotate key as last resort: POST /instances/{id}/rotate-ssh-key
```

### Audit Logs Not Appearing

```
Audit logs missing?
├── Check if audit logger is initialized
│   └── Look for "[ssh-audit] failed to write audit log" in control plane logs
├── Check database connectivity
│   └── Audit writes are non-blocking; failures are logged but don't stop operations
├── Check retention policy
│   ├── Entries may have been purged
│   └── GET /ssh-audit-logs?since=<recent-timestamp> to verify recent entries exist
└── Check query filters
    └── Ensure instance_id, event_type, and time range are correct
```

### IP Restriction Blocking Legitimate Connections

```
IP restriction blocking connections?
├── Check current allowed IPs: GET /instances/{id}/ssh-allowed-ips (admin)
├── Identify the source IP being blocked
│   └── Check audit logs: event_type=ip_restricted
├── Update allowed IPs: PUT /instances/{id}/ssh-allowed-ips (admin)
│   └── Body: { "allowed_ips": "10.0.0.5, 192.168.1.0/24" }
├── Or remove all restrictions
│   └── Body: { "allowed_ips": "" }
└── Reconnect after updating: POST /instances/{id}/ssh-reconnect
```

---

## Quick Reference: API Endpoints

| Method | Endpoint | Auth | Description |
|--------|----------|------|-------------|
| GET | `/api/v1/ssh-status` | User | Global SSH dashboard |
| GET | `/api/v1/ssh-metrics` | User | Aggregated SSH metrics |
| GET | `/api/v1/instances/{id}/ssh-status` | User | Instance SSH connection status |
| GET | `/api/v1/instances/{id}/ssh-events` | User | Connection event history |
| POST | `/api/v1/instances/{id}/ssh-test` | User | Test SSH connectivity |
| POST | `/api/v1/instances/{id}/ssh-reconnect` | User | Force reconnection |
| GET | `/api/v1/instances/{id}/ssh-fingerprint` | User | Key fingerprint info |
| GET | `/api/v1/instances/{id}/tunnels` | User | Active tunnel list |
| POST | `/api/v1/instances/{id}/rotate-ssh-key` | Admin | Rotate SSH keys |
| GET | `/api/v1/instances/{id}/ssh-allowed-ips` | Admin | Get IP restrictions |
| PUT | `/api/v1/instances/{id}/ssh-allowed-ips` | Admin | Update IP restrictions |
| GET | `/api/v1/ssh-audit-logs` | Admin | Query audit logs |
| POST | `/api/v1/ssh-audit-logs/purge` | Admin | Purge old audit entries |
