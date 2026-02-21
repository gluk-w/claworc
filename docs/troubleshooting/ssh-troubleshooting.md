---
type: reference
title: SSH Troubleshooting Guide
created: 2026-02-21
tags:
  - ssh
  - troubleshooting
  - operations
  - diagnostics
related:
  - "[[ssh-connectivity]]"
  - "[[ssh-operations]]"
  - "[[ssh-configuration]]"
  - "[[api]]"
---

# SSH Troubleshooting Guide

This guide covers common SSH connectivity issues in Claworc, with step-by-step diagnostic procedures, expected outputs, and solutions. For general SSH operations, see [[ssh-operations]]. For architecture details, see [[ssh-connectivity]].

---

## Table of Contents

1. [SSH Connection Failed](#1-ssh-connection-failed)
2. [Tunnel Not Working](#2-tunnel-not-working)
3. [Key Authentication Failed](#3-key-authentication-failed)
4. [File Operations Failing](#4-file-operations-failing)
5. [Terminal Not Responding](#5-terminal-not-responding)
6. [Performance Issues](#6-performance-issues)
7. [Enabling Debug Logging](#7-enabling-debug-logging)
8. [Diagnostic Commands Reference](#8-diagnostic-commands-reference)
9. [FAQ](#9-faq)

---

## 1. SSH Connection Failed

### Symptoms

- Instance shows `disconnected` or `failed` connection state in the dashboard.
- API returns `"connection_state": "failed"` from `GET /api/v1/instances/{id}/ssh-status`.
- Logs show repeated `[ssh] reconnection gave up for %s` messages.

### Diagnostic Steps

**Step 1: Check connection state via API**

```bash
curl -s http://localhost:8080/api/v1/instances/{id}/ssh-status | jq .
```

Expected output when healthy:
```json
{
  "connection_state": "connected",
  "health": {
    "healthy": true,
    "uptime_seconds": 3600,
    "successful_checks": 120,
    "failed_checks": 0
  }
}
```

If `connection_state` is `"failed"` or `"disconnected"`, proceed to Step 2.

**Step 2: Run a connection test**

```bash
curl -s -X POST http://localhost:8080/api/v1/instances/{id}/ssh-test | jq .
```

Check the `error` field in the response for the specific failure reason.

**Step 3: Check recent events for error details**

```bash
curl -s "http://localhost:8080/api/v1/instances/{id}/ssh-events?limit=20" | jq .events
```

Look for `health_check_failed`, `disconnected`, or `reconnecting` events.

**Step 4: Verify the agent pod/container is running**

For Kubernetes:
```bash
kubectl get pods -n claworc -l app=bot-{instance-name}
kubectl logs -n claworc deployment/bot-{instance-name} -c agent --tail=50
```

For Docker:
```bash
docker ps --filter name=bot-{instance-name}
docker logs bot-{instance-name} --tail=50
```

**Step 5: Verify sshd is running on the agent**

```bash
# Kubernetes
kubectl exec -n claworc deployment/bot-{instance-name} -- systemctl status sshd

# Docker
docker exec bot-{instance-name} systemctl status sshd
```

### Common Causes and Solutions

| Cause | Log Pattern | Solution |
|-------|-------------|----------|
| Agent pod not running | No pod found in `kubectl get pods` | Check pod events: `kubectl describe pod` |
| sshd not started | `systemctl status sshd` shows inactive | Restart sshd: `systemctl restart sshd` |
| Network unreachable | `connect to %s: dial tcp: i/o timeout` | Check network policies and service connectivity |
| Port blocked | `connect to %s: connection refused` | Verify sshd is listening on port 22; check firewall rules |
| DNS resolution failure | `connect to %s: lookup ... no such host` | Check Kubernetes DNS and service definitions |
| Rate limited | `connection blocked for %s due to %d consecutive failures` | Wait for block to expire (5 minutes) or investigate root cause |
| Max connections reached | `connect: maximum connections (%d) reached` | Check for connection leaks; close stale connections |

### Recovery Actions

**Force reconnection:**
```bash
curl -s -X POST http://localhost:8080/api/v1/instances/{id}/ssh-reconnect | jq .
```

**If rate limited**, wait for the block duration to expire (default: 5 minutes). You can check rate limit status in the connection events:
```bash
curl -s "http://localhost:8080/api/v1/instances/{id}/ssh-events?limit=10" | jq '.events[] | select(.type == "rate_limited")'
```

---

## 2. Tunnel Not Working

### Symptoms

- VNC viewer shows a blank screen or fails to connect.
- Gateway/web access returns connection errors.
- Dashboard shows tunnel status as `"closed"` or unhealthy.
- Logs show `[tunnel-health] %s %s tunnel (port %d) unhealthy`.

### Diagnostic Steps

**Step 1: Check tunnel status**

```bash
curl -s http://localhost:8080/api/v1/instances/{id}/tunnels | jq .
```

Expected output when healthy:
```json
{
  "tunnels": [
    {
      "service": "vnc",
      "status": "active",
      "local_port": 49152,
      "remote_port": 3000,
      "healthy": true
    },
    {
      "service": "gateway",
      "status": "active",
      "local_port": 49153,
      "remote_port": 8080,
      "healthy": true
    }
  ]
}
```

If `status` is `"closed"` or `healthy` is `false`, proceed to Step 2.

**Step 2: Verify the underlying SSH connection is active**

```bash
curl -s http://localhost:8080/api/v1/instances/{id}/ssh-status | jq .connection_state
```

Tunnels cannot work without an active SSH connection. If disconnected, resolve the SSH connection first (see [Section 1](#1-ssh-connection-failed)).

**Step 3: Check if the remote service is running on the agent**

```bash
# Check VNC (port 3000)
kubectl exec -n claworc deployment/bot-{instance-name} -- ss -tlnp | grep 3000

# Check Gateway (port 8080)
kubectl exec -n claworc deployment/bot-{instance-name} -- ss -tlnp | grep 8080
```

If the port is not listening, the service inside the agent has stopped.

**Step 4: Check tunnel health logs**

Look for tunnel health check entries in the control plane logs:
```
[tunnel-health] vnc bot-myinstance tunnel (port 49152) unhealthy: TCP probe to 127.0.0.1:49152 failed
[tunnel-health] check complete: 1 healthy, 1 unhealthy
```

### Common Causes and Solutions

| Cause | Log Pattern | Solution |
|-------|-------------|----------|
| Remote service not running | `SSH dial to %s:%s failed` | Start the service on the agent (e.g., `systemctl start noVNC`) |
| SSH connection lost | `get SSH client: %w` | Fix SSH connection first (Section 1) |
| Local port conflict | `listen on local port: %w` | Restart tunnels to get new ephemeral ports |
| Agent port not listening | TCP probe fails | Check agent service status; restart if needed |
| Tunnel closed unexpectedly | `tunnel %s for %s is closed` | Auto-reconnect should handle this; force reconnect if not |

### Recovery Actions

**Force tunnel recreation by reconnecting SSH:**
```bash
curl -s -X POST http://localhost:8080/api/v1/instances/{id}/ssh-reconnect | jq .
```

This closes the existing SSH connection, re-establishes it, and restarts all tunnels.

**Verify tunnels after reconnection:**
```bash
curl -s http://localhost:8080/api/v1/instances/{id}/tunnels | jq '.tunnels[] | {service, status, healthy}'
```

---

## 3. Key Authentication Failed

### Symptoms

- SSH connection test returns `"SSH connection failed: ssh: handshake failed: ssh: unable to authenticate"`.
- Logs show `connect to %s: ssh: handshake failed`.
- Key rotation fails with `"rotate key: test new key failed"`.
- Fingerprint mismatch warning in logs: `[ssh] host key fingerprint changed for %s`.

### Diagnostic Steps

**Step 1: Verify key fingerprint matches**

```bash
curl -s http://localhost:8080/api/v1/instances/{id}/ssh-fingerprint | jq .
```

If `verified` is `false`, the stored fingerprint doesn't match the computed one. This could indicate key corruption or tampering.

**Step 2: Check if the private key file exists on the control plane**

The default key storage path is `/app/data/ssh-keys/{instance-name}`. Verify:
```bash
# Inside the control plane container
ls -la /app/data/ssh-keys/
```

Files should have `0600` permissions and be owned by the process user.

**Step 3: Check if the public key is in the agent's authorized_keys**

```bash
kubectl exec -n claworc deployment/bot-{instance-name} -- cat /root/.ssh/authorized_keys
```

Compare the key in `authorized_keys` with the public key stored in the database.

**Step 4: Verify sshd authentication settings on the agent**

```bash
kubectl exec -n claworc deployment/bot-{instance-name} -- sshd -T | grep -E 'pubkeyauthentication|passwordauthentication|authorizedkeysfile'
```

Expected:
```
pubkeyauthentication yes
passwordauthentication no
authorizedkeysfile .ssh/authorized_keys
```

**Step 5: Check for fingerprint mismatch events**

```bash
curl -s "http://localhost:8080/api/v1/ssh-audit-logs?event_type=fingerprint_mismatch&instance_id={id}" | jq .
```

A fingerprint mismatch can indicate a pod restart (new host key) or a potential man-in-the-middle attack.

### Common Causes and Solutions

| Cause | Log Pattern | Solution |
|-------|-------------|----------|
| Private key file missing | `read private key from %s: no such file or directory` | Rotate key to generate a new pair |
| Key file corrupted | `load private key: file %s does not contain a valid PEM block` | Rotate key to regenerate |
| Key not in authorized_keys | `ssh: handshake failed: ssh: unable to authenticate` | Re-deploy the public key to the agent |
| Wrong file permissions | `read private key from %s: permission denied` | Fix: `chmod 0600 /app/data/ssh-keys/{name}` |
| Agent sshd misconfigured | `ssh: handshake failed` | Verify sshd_config has `PubkeyAuthentication yes` |
| Pod restarted (host key changed) | `host key fingerprint changed for %s` | Expected after pod restart; TOFU will accept new host key |

### Recovery Actions

**Rotate the SSH key (admin only):**
```bash
curl -s -X POST http://localhost:8080/api/v1/instances/{id}/rotate-ssh-key | jq .
```

This generates a new ED25519 key pair, deploys it to the agent, verifies connectivity, removes the old key, and reconnects. The process is zero-downtime.

**If rotation fails because there's no active connection**, you may need to manually deploy a key:

1. Generate a new key pair using the control plane.
2. Copy the public key to the agent's `/root/.ssh/authorized_keys` via `kubectl exec` or `docker exec`.
3. Restart the instance from the dashboard to trigger a fresh connection.

---

## 4. File Operations Failing

### Symptoms

- File browser in the UI shows errors or empty results.
- File uploads/downloads fail silently.
- API returns errors from file operation endpoints.
- Logs show `[sshfiles] cmd=%q exit=%d` with non-zero exit codes.

### Diagnostic Steps

**Step 1: Verify SSH connection is active**

```bash
curl -s http://localhost:8080/api/v1/instances/{id}/ssh-status | jq .connection_state
```

File operations require an active SSH connection. If disconnected, fix SSH first.

**Step 2: Check file operation logs**

Look for `[sshfiles]` log entries on the control plane:
```
[sshfiles] cmd="ls -la /path" exit=1 duration=15ms
[sshfiles] cmd="cat /path/file" exit=1 duration=12ms stderr="No such file or directory"
```

The `exit` code and `stderr` output indicate the specific failure.

**Step 3: Test SSH command execution directly**

```bash
curl -s -X POST http://localhost:8080/api/v1/instances/{id}/ssh-test | jq .command_test
```

If `command_test` is `false`, the SSH session cannot execute commands even though the connection is up.

**Step 4: Verify target paths exist on the agent**

```bash
kubectl exec -n claworc deployment/bot-{instance-name} -- ls -la /path/to/target
```

### Common Causes and Solutions

| Cause | Log Pattern | Solution |
|-------|-------------|----------|
| SSH connection lost | `create SSH session: %w` | Reconnect SSH (Section 1) |
| File/directory not found | `exit=1` with "No such file or directory" | Verify path exists on agent |
| Permission denied | `exit=1` with "Permission denied" | Check file ownership and permissions on agent |
| Disk full on agent | `exit=1` with "No space left on device" | Free disk space on agent; check PVC capacity |
| Session creation failed | `create SSH session: %w` | May indicate SSH connection is degraded; reconnect |
| Write pipe failed | `write to stdin: %w` | Possible network interruption; retry the operation |
| Command timeout | `wait for command: %w` | Large file or slow agent; check agent resource usage |

### Recovery Actions

**For persistent session failures**, force a reconnect:
```bash
curl -s -X POST http://localhost:8080/api/v1/instances/{id}/ssh-reconnect | jq .
```

**For disk space issues**, check PVC usage:
```bash
kubectl exec -n claworc deployment/bot-{instance-name} -- df -h
```

---

## 5. Terminal Not Responding

### Symptoms

- Terminal tab in the UI shows a blank screen or frozen cursor.
- Typed characters don't appear.
- Terminal session closes unexpectedly.
- Logs show `[sshterminal] session closed` or `[session-mgr] session %s stdout ended`.

### Diagnostic Steps

**Step 1: Verify SSH connection is active**

```bash
curl -s http://localhost:8080/api/v1/instances/{id}/ssh-status | jq .connection_state
```

Terminal sessions require an active SSH connection.

**Step 2: Check terminal session logs**

Look for `[sshterminal]` and `[session-mgr]` log entries:
```
[sshterminal] interactive session started shell=/bin/bash
[session-mgr] created session abc123 for instance 1 (user 1, shell /bin/bash)
[session-mgr] session abc123 stdout ended: EOF
[session-mgr] closed session abc123
```

**Step 3: Verify the shell exists on the agent**

```bash
kubectl exec -n claworc deployment/bot-{instance-name} -- which bash
kubectl exec -n claworc deployment/bot-{instance-name} -- which zsh
```

**Step 4: Check for idle session cleanup**

Detached terminal sessions are cleaned up after 30 minutes of inactivity. Look for:
```
[session-mgr] cleaning up idle session %s (detached since %s)
```

### Common Causes and Solutions

| Cause | Log Pattern | Solution |
|-------|-------------|----------|
| SSH connection lost | `create SSH session: %w` | Reconnect SSH |
| Shell not found | `start shell %q: %w` | Verify shell path exists on agent |
| PTY allocation failed | `request PTY: %w` | Agent may have too many open PTYs; check `ulimit` |
| Session idle timeout | `cleaning up idle session` | Reconnect to terminal; sessions auto-clean after 30 min |
| Agent shell crashed | `stdout ended: %v` | Check agent process list; restart shell service |
| Terminal resize failed | `resize terminal: %w` | Usually transient; refresh the browser tab |
| WebSocket disconnected | Browser shows "disconnected" | Check browser network tab; reconnect WebSocket |

### Recovery Actions

**Open a new terminal session** by navigating away from the Terminal tab and back. This creates a fresh SSH session and PTY.

**If all terminal sessions fail**, the SSH connection may be degraded:
```bash
curl -s -X POST http://localhost:8080/api/v1/instances/{id}/ssh-reconnect | jq .
```

---

## 6. Performance Issues

### Symptoms

- Slow file operations (uploads/downloads take longer than expected).
- High latency reported in SSH connection tests.
- VNC viewer feels sluggish.
- Terminal input has noticeable delay.
- Frequent tunnel reconnections.

### Diagnostic Steps

**Step 1: Measure SSH connection latency**

```bash
curl -s -X POST http://localhost:8080/api/v1/instances/{id}/ssh-test | jq .latency_ms
```

Baseline latency should be under 5ms for local/same-cluster connections. Latency above 50ms may indicate network issues.

**Step 2: Check health check metrics**

```bash
curl -s http://localhost:8080/api/v1/instances/{id}/ssh-status | jq .health
```

A high `failed_checks` count relative to `successful_checks` indicates intermittent connectivity.

**Step 3: Check tunnel metrics**

```bash
curl -s http://localhost:8080/api/v1/instances/{id}/tunnels | jq '.tunnels[] | {service, bytes_transferred, healthy}'
```

**Step 4: Check agent resource usage**

```bash
kubectl exec -n claworc deployment/bot-{instance-name} -- top -bn1 | head -20
kubectl exec -n claworc deployment/bot-{instance-name} -- free -h
kubectl exec -n claworc deployment/bot-{instance-name} -- df -h
```

**Step 5: Review global SSH metrics**

```bash
curl -s http://localhost:8080/api/v1/ssh-metrics | jq .
```

Check `reconnection_counts` for instances with frequent reconnections and `health_rates` for instances with low success rates.

### Optimization Tips

| Issue | Indicator | Solution |
|-------|-----------|----------|
| High latency | `latency_ms > 50` | Check network path; ensure control plane and agents are in the same cluster/region |
| Frequent reconnections | Many `reconnecting` events | Check agent resource usage; increase keepalive interval if network is stable |
| Slow file transfers | Large `duration` in `[sshfiles]` logs | Check agent disk I/O; consider PVC storage class |
| VNC lag | High latency + tunnel bytes low | Check VNC service resource usage on agent |
| Health check timeouts | `health check timed out for %s` | Agent under heavy load; check CPU/memory |

### Key Performance Characteristics

- SSH exec command execution: ~1-5ms (vs. 20-100ms for Kubernetes exec)
- File writes via stdin piping: no base64 overhead (vs. 33% for K8s approach)
- Tunnel overhead: ~55 microseconds per HTTP request
- Tunnel throughput: >27,000 req/s with 10 concurrent clients on loopback
- WebSocket round-trip: ~55 microseconds vs. direct connection

---

## 7. Enabling Debug Logging

### Log Prefixes

All SSH components use structured log prefixes for easy filtering:

| Prefix | Component | What It Covers |
|--------|-----------|----------------|
| `[ssh]` | SSH Manager | Connection lifecycle, keepalive, reconnection |
| `[sshkeys]` | Key Management | Key generation, rotation, file operations |
| `[tunnel]` | Tunnel Manager | Tunnel creation, closure, reconnection |
| `[tunnel-health]` | Tunnel Health | Health check results, unhealthy tunnels |
| `[sshfiles]` | File Operations | Command execution, exit codes, durations |
| `[sshlogs]` | Log Streaming | Stream start/stop, line counts, errors |
| `[sshterminal]` | Terminal Sessions | Session lifecycle, shell startup |
| `[session-mgr]` | Session Manager | Managed session create/close, idle cleanup |
| `[ssh-audit]` | Audit Logger | Audit events, purge operations |

### Filtering Logs

**View all SSH-related logs:**
```bash
# Kubernetes
kubectl logs -n claworc deployment/claworc-control-plane --tail=200 | grep '\[ssh'

# Docker
docker logs claworc-control-plane --tail=200 2>&1 | grep '\[ssh'
```

**Filter by specific component:**
```bash
# Connection issues only
kubectl logs -n claworc deployment/claworc-control-plane | grep '\[ssh\]'

# Tunnel issues only
kubectl logs -n claworc deployment/claworc-control-plane | grep '\[tunnel'

# File operation issues
kubectl logs -n claworc deployment/claworc-control-plane | grep '\[sshfiles\]'

# Key management
kubectl logs -n claworc deployment/claworc-control-plane | grep '\[sshkeys\]'
```

**Filter by instance name:**
```bash
kubectl logs -n claworc deployment/claworc-control-plane | grep 'bot-myinstance'
```

**Filter by severity (errors and warnings):**
```bash
kubectl logs -n claworc deployment/claworc-control-plane | grep -iE '(error|warn|fail)' | grep '\[ssh'
```

### Key Log Messages to Watch

**Connection health indicators:**
```
# Healthy - periodic keepalive succeeds silently
[ssh] connected to bot-myinstance at 10.0.1.5:22

# Unhealthy - keepalive or health check failing
[ssh] keepalive failed for bot-myinstance: timeout, triggering reconnection
[ssh] health check failed for bot-myinstance: unexpected output, triggering reconnection

# Recovery
[ssh] reconnecting bot-myinstance (attempt 1/10, reason: connection lost)
[ssh] reconnected bot-myinstance after 1 attempt(s)

# Terminal failure
[ssh] reconnection gave up for bot-myinstance: reconnection failed after 10 attempts
```

**Tunnel health indicators:**
```
# Healthy
[tunnel] VNC tunnel for bot-myinstance ready on local port 49152
[tunnel-health] check complete: 4 healthy, 0 unhealthy

# Unhealthy
[tunnel-health] vnc bot-myinstance tunnel (port 49152) unhealthy: TCP probe failed
[tunnel] reconnecting vnc tunnel for bot-myinstance (attempt 1)
[tunnel] reconnected vnc tunnel for bot-myinstance after 1 attempt(s)
```

**Security-relevant logs:**
```
[ssh] host key fingerprint changed for bot-myinstance — expected SHA256:abc, got SHA256:xyz (may indicate pod restart or MITM)
[ssh] rate limit: instance bot-myinstance is blocked for 5m0s
[ssh-audit] fingerprint_mismatch instance=bot-myinstance user=admin ip=10.0.0.1
[ssh-audit] ip_restricted instance=bot-myinstance user=admin ip=203.0.113.5
```

---

## 8. Diagnostic Commands Reference

### Quick Health Check

```bash
# Check all instances at once
curl -s http://localhost:8080/api/v1/ssh-status | jq '.instances[] | {name: .instance_name, state: .connection_state, healthy: .health.healthy, tunnels: .tunnel_count, healthy_tunnels: .healthy_tunnels}'
```

### Per-Instance Diagnostics

```bash
INSTANCE_ID=1

# Connection state
curl -s http://localhost:8080/api/v1/instances/$INSTANCE_ID/ssh-status | jq .

# Connection test with latency
curl -s -X POST http://localhost:8080/api/v1/instances/$INSTANCE_ID/ssh-test | jq .

# Recent events
curl -s "http://localhost:8080/api/v1/instances/$INSTANCE_ID/ssh-events?limit=20" | jq .

# Tunnel status
curl -s http://localhost:8080/api/v1/instances/$INSTANCE_ID/tunnels | jq .

# Key fingerprint verification
curl -s http://localhost:8080/api/v1/instances/$INSTANCE_ID/ssh-fingerprint | jq .
```

### Audit Log Queries

```bash
# Recent connection failures
curl -s "http://localhost:8080/api/v1/ssh-audit-logs?event_type=connection_failed&limit=20" | jq .

# Fingerprint mismatches (potential security events)
curl -s "http://localhost:8080/api/v1/ssh-audit-logs?event_type=fingerprint_mismatch" | jq .

# IP restriction events
curl -s "http://localhost:8080/api/v1/ssh-audit-logs?event_type=ip_restricted" | jq .

# All events for a specific instance
curl -s "http://localhost:8080/api/v1/ssh-audit-logs?instance_id=$INSTANCE_ID&limit=50" | jq .

# Events in a time range
curl -s "http://localhost:8080/api/v1/ssh-audit-logs?since=2026-02-21T00:00:00Z&until=2026-02-21T23:59:59Z" | jq .
```

### Agent-Side Diagnostics

```bash
INSTANCE_NAME=bot-myinstance

# Check sshd status
kubectl exec -n claworc deployment/$INSTANCE_NAME -- systemctl status sshd

# Check sshd configuration
kubectl exec -n claworc deployment/$INSTANCE_NAME -- sshd -T

# Check authorized keys
kubectl exec -n claworc deployment/$INSTANCE_NAME -- cat /root/.ssh/authorized_keys

# Check listening ports
kubectl exec -n claworc deployment/$INSTANCE_NAME -- ss -tlnp

# Check agent resource usage
kubectl exec -n claworc deployment/$INSTANCE_NAME -- free -h
kubectl exec -n claworc deployment/$INSTANCE_NAME -- df -h

# Check sshd logs on agent
kubectl exec -n claworc deployment/$INSTANCE_NAME -- journalctl -u sshd --no-pager -n 50
```

### Recovery Commands

```bash
INSTANCE_ID=1

# Force reconnection
curl -s -X POST http://localhost:8080/api/v1/instances/$INSTANCE_ID/ssh-reconnect | jq .

# Rotate SSH key (admin only)
curl -s -X POST http://localhost:8080/api/v1/instances/$INSTANCE_ID/rotate-ssh-key | jq .

# Restart agent sshd
kubectl exec -n claworc deployment/bot-myinstance -- systemctl restart sshd
```

---

## 9. FAQ

### General

**Q: How do I know if SSH is working for an instance?**

Check the connection state: `curl -s http://localhost:8080/api/v1/instances/{id}/ssh-status | jq .connection_state`. A value of `"connected"` with `health.healthy: true` means everything is working.

**Q: What happens when an SSH connection drops?**

The SSH Manager automatically detects the failure via keepalive probes (every 30 seconds) and triggers reconnection with exponential backoff (1s, 2s, 4s, 8s, 16s). It retries up to 10 times before marking the connection as `failed`. Tunnels are automatically recreated after reconnection succeeds.

**Q: How often are health checks performed?**

SSH keepalive probes run every 30 seconds. Tunnel health checks (TCP probes) run every 60 seconds globally. Per-instance tunnel monitors check every 10 seconds for closed tunnels.

**Q: What SSH key algorithm is used?**

ED25519. Each instance gets its own unique key pair generated at instance creation time.

### Connection Issues

**Q: My instance keeps reconnecting. What's wrong?**

Frequent reconnections usually indicate network instability or agent resource exhaustion. Check: (1) agent CPU/memory usage, (2) network path between control plane and agent, (3) recent events for patterns. If reconnections happen at regular intervals, the keepalive probe is timing out, which suggests the agent is overloaded.

**Q: I see "rate limited" in the events. How do I fix this?**

The rate limiter activates after 5 consecutive connection failures (5-minute block) or more than 10 connection attempts per minute. This is a safety mechanism. Fix the underlying connection issue first — the block will lift automatically. Do not attempt to bypass the rate limiter.

**Q: What does "host key fingerprint changed" mean?**

This occurs when the agent's SSH host key doesn't match what was previously seen (Trust On First Use / TOFU). It commonly happens after a pod restart, since the agent generates a new host key on startup. If this happens unexpectedly without a pod restart, investigate further as it could indicate a man-in-the-middle attack.

### Key Management

**Q: How do I rotate SSH keys?**

Use the API (admin only): `curl -s -X POST http://localhost:8080/api/v1/instances/{id}/rotate-ssh-key | jq .`. This is a zero-downtime operation — the new key is deployed and verified before the old one is removed.

**Q: What if key rotation fails midway?**

If rotation fails during the new key test phase, the old key remains active and the connection is not disrupted. The error message from the rotation endpoint will indicate what went wrong. You can safely retry the rotation.

**Q: Where are SSH private keys stored?**

On the control plane: `/app/data/ssh-keys/{instance-name}` with `0600` permissions. On the agent: the public key is in `/root/.ssh/authorized_keys`.

### Tunnels

**Q: How do I know which tunnels are active?**

Use: `curl -s http://localhost:8080/api/v1/instances/{id}/tunnels | jq .tunnels`. Each tunnel shows its service type, ports, health status, and bytes transferred.

**Q: Tunnels show healthy but VNC/Gateway doesn't work. What's wrong?**

The tunnel health check only verifies the SSH port-forwarding channel is working (TCP probe). If the tunnel is healthy but the service isn't accessible, the problem is likely the service inside the agent (VNC or Gateway process) rather than the tunnel itself. Check if the service is running on the expected port inside the agent.

**Q: Can I manually restart tunnels without reconnecting SSH?**

Currently, tunnel recreation requires an SSH reconnection. Use the `ssh-reconnect` endpoint, which closes all tunnels, reconnects SSH, and restarts tunnels.

### Security

**Q: How do I restrict which IPs can connect to an instance?**

Set IP restrictions (admin only):
```bash
curl -s -X PUT http://localhost:8080/api/v1/instances/{id}/ssh-allowed-ips \
  -H "Content-Type: application/json" \
  -d '{"allowed_ips": "10.0.0.0/8,192.168.1.0/24"}' | jq .
```

An empty `allowed_ips` string removes all restrictions (allow all).

**Q: How do I view audit logs for security investigations?**

Query the audit log endpoint with filters:
```bash
curl -s "http://localhost:8080/api/v1/ssh-audit-logs?event_type=fingerprint_mismatch&since=2026-02-20T00:00:00Z" | jq .
```

Audit logs are retained for 90 days by default and can be purged manually.

**Q: Is password authentication enabled on the agent?**

No. The agent's sshd is hardened with `PasswordAuthentication no`. Only public key authentication is supported.
