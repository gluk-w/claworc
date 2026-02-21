# REST API

## Base URL

```
http://192.168.1.204:8000/api/v1
```

All endpoints (except `/health`) require session-based authentication via the `RequireAuth` middleware. Some endpoints additionally require admin role via `RequireAdmin`.

## Authentication

All protected endpoints require a valid session cookie obtained through the login flow (`/api/v1/auth/login`). Unauthenticated requests receive `401 Unauthorized`. Non-admin users accessing admin-only endpoints receive `403 Forbidden`.

Access to per-instance endpoints is further controlled by instance assignment: non-admin users can only access instances explicitly assigned to them.

## Endpoints

### Instances

#### List Instances

```
GET /api/v1/instances
```

Returns all instances with their current Kubernetes pod status.

**Response** `200 OK`:
```json
[
  {
    "id": 1,
    "name": "bot-alpha",
    "display_name": "Bot Alpha",
    "nodeport_chrome": 30100,
    "nodeport_terminal": 30101,
    "status": "running",
    "cpu_request": "500m",
    "cpu_limit": "2000m",
    "memory_request": "1Gi",
    "memory_limit": "4Gi",
    "storage_clawdbot": "5Gi",
    "storage_homebrew": "10Gi",
    "storage_clawd": "5Gi",
    "storage_chrome": "5Gi",
    "has_anthropic_override": true,
    "has_openai_override": false,
    "has_brave_override": false,
    "created_at": "2026-02-05T10:30:00Z",
    "updated_at": "2026-02-05T10:30:00Z"
  }
]
```

The `status` field is enriched with live Kubernetes pod state, not just the database value.

---

#### Create Instance

```
POST /api/v1/instances
```

Creates a new bot instance with all required Kubernetes resources.

**Request Body**:
```json
{
  "display_name": "Bot Alpha",
  "cpu_request": "500m",
  "cpu_limit": "2000m",
  "memory_request": "1Gi",
  "memory_limit": "4Gi",
  "storage_clawdbot": "5Gi",
  "storage_homebrew": "10Gi",
  "storage_clawd": "5Gi",
  "storage_chrome": "5Gi",
  "anthropic_api_key": null,
  "openai_api_key": "sk-override-key-here",
  "brave_api_key": null
}
```

Only `display_name` is required. All other fields have defaults.

**Response** `201 Created`:
```json
{
  "id": 1,
  "name": "bot-alpha",
  "display_name": "Bot Alpha",
  "nodeport_chrome": 30100,
  "nodeport_terminal": 30101,
  "status": "creating",
  "vnc_chrome_url": "http://192.168.1.104:30100/vnc.html?autoconnect=true",
  "vnc_terminal_url": "http://192.168.1.104:30101/vnc.html?autoconnect=true"
}
```

**Creation Flow**:
1. Generate K8s-safe name from display_name
2. Allocate next available NodePort pair (even/odd from 30100-30199)
3. Insert record into SQLite
4. Create PVCs (clawdbot-data, homebrew-data, clawd-data, chrome-data)
5. Create Secret with effective API keys
6. Create ConfigMap with clawdbot.json
7. Create Deployment (1 replica, Recreate strategy)
8. Create NodePort Service
9. Update status to `running`

---

#### Get Instance Detail

```
GET /api/v1/instances/{id}
```

Returns full instance details including live pod status from Kubernetes.

**Response** `200 OK`:
```json
{
  "id": 1,
  "name": "bot-alpha",
  "display_name": "Bot Alpha",
  "nodeport_chrome": 30100,
  "nodeport_terminal": 30101,
  "status": "running",
  "cpu_request": "500m",
  "cpu_limit": "2000m",
  "memory_request": "1Gi",
  "memory_limit": "4Gi",
  "storage_clawdbot": "5Gi",
  "storage_homebrew": "10Gi",
  "storage_clawd": "5Gi",
  "storage_chrome": "5Gi",
  "has_anthropic_override": true,
  "has_openai_override": false,
  "has_brave_override": false,
  "vnc_chrome_url": "http://192.168.1.104:30100/vnc.html?autoconnect=true",
  "vnc_terminal_url": "http://192.168.1.104:30101/vnc.html?autoconnect=true",
  "created_at": "2026-02-05T10:30:00Z",
  "updated_at": "2026-02-05T10:30:00Z"
}
```

---

#### Delete Instance

```
DELETE /api/v1/instances/{id}
```

Deletes the instance and all associated Kubernetes resources.

**Response** `204 No Content`

**Deletion Flow**:
1. Delete Deployment
2. Delete Service
3. Delete PVCs (clawdbot-data, homebrew-data, clawd-data, chrome-data)
4. Delete Secret
5. Delete ConfigMap
6. Release NodePort pair back to the pool
7. Remove record from SQLite

---

#### Start Instance

```
POST /api/v1/instances/{id}/start
```

Scales the Deployment to 1 replica. PVCs are already preserved from when the instance was stopped.

**Response** `200 OK`:
```json
{
  "status": "running"
}
```

---

#### Stop Instance

```
POST /api/v1/instances/{id}/stop
```

Scales the Deployment to 0 replicas. All PVCs are preserved.

**Response** `200 OK`:
```json
{
  "status": "stopped"
}
```

---

#### Restart Instance

```
POST /api/v1/instances/{id}/restart
```

Triggers a rollout restart on the Deployment.

**Response** `200 OK`:
```json
{
  "status": "running"
}
```

---

### Configuration

#### Get Instance Config

```
GET /api/v1/instances/{id}/config
```

Returns the clawdbot.json configuration for the instance.

**Response** `200 OK`:
```json
{
  "config": "{ \"key\": \"value\", \"api_key\": \"${ANTHROPIC_API_KEY}\" }"
}
```

The config is returned as a JSON string (the raw content of clawdbot.json). API key placeholders like `${ANTHROPIC_API_KEY}` are preserved -- they are resolved at runtime by the clawdbot process.

---

#### Update Instance Config

```
PUT /api/v1/instances/{id}/config
```

Updates the clawdbot.json configuration. Triggers a pod restart.

**Request Body**:
```json
{
  "config": "{ \"key\": \"new_value\" }"
}
```

**Response** `200 OK`:
```json
{
  "config": "{ \"key\": \"new_value\" }",
  "restarted": true
}
```

**Update Flow**:
1. Validate JSON syntax
2. Update config in SQLite
3. Patch Kubernetes ConfigMap
4. Trigger pod restart
5. Return updated config

---

### Logs

#### Stream Instance Logs

```
GET /api/v1/instances/{id}/logs
```

Streams pod logs in real-time via Server-Sent Events (SSE).

**Query Parameters**:
- `tail` (optional, default 100): Number of most recent log lines to return initially
- `follow` (optional, default true): Whether to stream new lines as they arrive

**Response** `200 OK` with `Content-Type: text/event-stream`:
```
data: 2026-02-05T10:30:00Z Starting TigerVNC server...
data: 2026-02-05T10:30:01Z VNC server running on :1
data: 2026-02-05T10:30:02Z Starting Chrome kiosk on :1...
data: 2026-02-05T10:30:03Z Starting xterm on :2...
data: 2026-02-05T10:30:05Z Starting openclaw gateway...
```

---

### Settings

#### Get Global Settings

```
GET /api/v1/settings
```

Returns global settings with API keys masked.

**Response** `200 OK`:
```json
{
  "anthropic_api_key": "****abcd",
  "openai_api_key": "****efgh",
  "brave_api_key": "****ijkl",
  "default_cpu_request": "500m",
  "default_cpu_limit": "2000m",
  "default_memory_request": "1Gi",
  "default_memory_limit": "4Gi",
  "default_storage_clawdbot": "5Gi",
  "default_storage_homebrew": "10Gi",
  "default_storage_clawd": "5Gi",
  "default_storage_chrome": "5Gi"
}
```

API keys show only the last 4 characters. The full key is never returned by the API.

---

#### Update Global Settings

```
PUT /api/v1/settings
```

Updates global settings. Only provided fields are updated.

**Request Body**:
```json
{
  "anthropic_api_key": "sk-ant-new-key-here",
  "default_cpu_limit": "3000m"
}
```

**Response** `200 OK`:
```json
{
  "anthropic_api_key": "****here",
  "openai_api_key": "****efgh",
  "brave_api_key": "****ijkl",
  "default_cpu_request": "500m",
  "default_cpu_limit": "3000m",
  "default_memory_request": "1Gi",
  "default_memory_limit": "4Gi",
  "default_storage_clawdbot": "5Gi",
  "default_storage_homebrew": "10Gi",
  "default_storage_clawd": "5Gi",
  "default_storage_chrome": "5Gi"
}
```

When a global API key is updated, the system should propagate the change to all instances that don't have an override for that key (by updating their Kubernetes Secrets).

---

### Health

#### Health Check

```
GET /health
```

**Response** `200 OK`:
```json
{
  "status": "healthy",
  "kubernetes": "connected",
  "database": "connected"
}
```

### SSH Connectivity

All control-plane-to-agent communication uses SSH. The following endpoints provide visibility into SSH connection state, tunnels, key management, and audit logging.

#### Get SSH Connection Status

```
GET /api/v1/instances/{id}/ssh-status
```

Returns the SSH connection status for an instance, including connection state, health metrics, active tunnels, and recent state transitions.

**Authorization**: Authenticated user with access to the instance.

**Response** `200 OK`:
```json
{
  "connection_state": "connected",
  "health": {
    "connected_at": "2026-02-21T10:30:00Z",
    "last_health_check": "2026-02-21T10:35:00Z",
    "uptime_seconds": 300,
    "successful_checks": 45,
    "failed_checks": 2,
    "healthy": true
  },
  "tunnels": [
    {
      "service": "vnc",
      "local_port": 12345,
      "remote_port": 3000,
      "created_at": "2026-02-21T10:30:00Z",
      "last_check": "2026-02-21T10:35:00Z",
      "last_successful_check": "2026-02-21T10:35:00Z",
      "last_error": "",
      "bytes_transferred": 1024000,
      "healthy": true
    }
  ],
  "recent_events": [
    {
      "from": "disconnected",
      "to": "connecting",
      "timestamp": "2026-02-21T10:30:00Z"
    }
  ]
}
```

The `connection_state` field can be: `disconnected`, `connecting`, `connected`, `reconnecting`, or `failed`. The `health` field is `null` when no connection metrics are available. Up to 10 most recent state transitions are returned.

**Errors**: `400` invalid ID, `404` not found, `403` access denied.

---

#### Test SSH Connection

```
POST /api/v1/instances/{id}/ssh-test
```

Tests SSH connectivity to the agent by establishing a connection, running a test command, and reporting tunnel health. Performs SSH key fingerprint verification and source IP restriction checks before connecting.

**Authorization**: Authenticated user with access to the instance.

**Response** `200 OK` (success):
```json
{
  "success": true,
  "latency_ms": 145,
  "tunnel_status": [
    {
      "service": "vnc",
      "healthy": true
    },
    {
      "service": "gateway",
      "healthy": false,
      "error": "connection refused"
    }
  ],
  "command_test": true
}
```

**Response** `200 OK` (failure):
```json
{
  "success": false,
  "latency_ms": 5000,
  "tunnel_status": [],
  "command_test": false,
  "error": "SSH connection failed: connection timeout"
}
```

Note: Test failures return `200` with `success: false` rather than an HTTP error code. Security violations (fingerprint mismatch, IP restriction) are logged as audit events.

**Errors**: `400` invalid ID or no SSH key configured, `404` not found, `403` access denied, `503` orchestrator or SSH manager unavailable.

---

#### Force SSH Reconnection

```
POST /api/v1/instances/{id}/ssh-reconnect
```

Closes the existing SSH connection and establishes a new one, then restarts all tunnels for the instance. Verifies SSH key fingerprint and source IP restrictions before reconnecting.

**Authorization**: Authenticated user with access to the instance.

**Response** `200 OK` (success):
```json
{
  "success": true,
  "message": "SSH connection re-established successfully"
}
```

**Response** `200 OK` (failure):
```json
{
  "success": false,
  "message": "SSH key integrity check failed: fingerprint mismatch"
}
```

**Errors**: `400` invalid ID, `404` not found, `403` access denied, `503` orchestrator or SSH manager unavailable.

---

#### Get SSH Connection Events

```
GET /api/v1/instances/{id}/ssh-events
```

Returns the SSH connection event history for an instance. Events include connections, disconnections, health check failures, reconnection attempts, and outcomes.

**Authorization**: Authenticated user with access to the instance.

**Query Parameters**:
- `limit` (optional, default 50, max 100): Number of events to return.

**Response** `200 OK`:
```json
{
  "events": [
    {
      "instance_name": "bot-alpha",
      "type": "connection",
      "details": "Connection established successfully",
      "timestamp": "2026-02-21T10:30:00Z"
    },
    {
      "instance_name": "bot-alpha",
      "type": "reconnecting",
      "details": "Connection lost, attempting reconnection",
      "timestamp": "2026-02-21T10:25:00Z"
    }
  ]
}
```

Event types: `connection`, `disconnection`, `reconnecting`, `health_check`, `key_rotation`, `fingerprint_mismatch`, `ip_restricted`.

**Errors**: `400` invalid ID, `404` not found, `403` access denied.

---

#### Get Tunnel Status

```
GET /api/v1/instances/{id}/tunnels
```

Returns the status of all active SSH tunnels for an instance.

**Authorization**: Authenticated user with access to the instance.

**Response** `200 OK`:
```json
{
  "tunnels": [
    {
      "service": "vnc",
      "type": "reverse",
      "local_port": 12345,
      "remote_port": 3000,
      "status": "active",
      "started_at": "2026-02-21T10:30:00Z",
      "last_check": "2026-02-21T10:35:00Z",
      "last_error": ""
    },
    {
      "service": "gateway",
      "type": "reverse",
      "local_port": 12346,
      "remote_port": 8080,
      "status": "closed",
      "started_at": "2026-02-21T10:30:00Z",
      "last_check": "2026-02-21T10:34:50Z",
      "last_error": "connection refused"
    }
  ]
}
```

The `status` field is either `active` or `closed`.

**Errors**: `400` invalid ID, `404` not found, `403` access denied.

---

#### Get SSH Key Fingerprint

```
GET /api/v1/instances/{id}/ssh-fingerprint
```

Returns the SSH public key fingerprint for an instance and verifies it against the stored expected value.

**Authorization**: Authenticated user with access to the instance.

**Response** `200 OK`:
```json
{
  "fingerprint": "SHA256:aAbBcCdDeEfFgGhHiIjJkKlLmMnNoOpPqQrRsStT",
  "algorithm": "ssh-ed25519",
  "verified": true
}
```

When `verified` is `false`, the computed fingerprint does not match the stored expected value, indicating possible key tampering.

**Errors**: `400` invalid ID or no SSH key configured, `404` not found, `403` access denied, `500` failed to parse public key.

---

#### Rotate SSH Key (Admin Only)

```
POST /api/v1/instances/{id}/rotate-ssh-key
```

Rotates the SSH key pair for an instance. The rotation process:
1. Verifies the current key fingerprint
2. Generates a new ED25519 key pair
3. Appends the new public key to the agent's `authorized_keys`
4. Verifies the new key works by connecting with it
5. Updates the database with the new key info
6. Reconnects SSH with the new key
7. Restarts tunnels
8. Deletes the old private key file

Requires an active SSH connection to the instance.

**Authorization**: Admin only.

**Response** `200 OK` (success):
```json
{
  "success": true,
  "fingerprint": "SHA256:aAbBcCdDeEfFgGhHiIjJkKlLmMnNoOpPqQrRsStT",
  "rotated_at": "2026-02-21T10:30:00Z",
  "message": "SSH key rotation completed successfully"
}
```

**Response** `200 OK` (failure):
```json
{
  "success": false,
  "message": "Key rotation failed: failed to append new key to authorized_keys"
}
```

Successful rotations are logged as audit events.

**Errors**: `400` invalid ID or no SSH key configured, `404` not found, `403` access denied, `500` rotation succeeded but database update failed, `503` orchestrator/SSH manager unavailable or no active connection.

---

#### Get Allowed Source IPs (Admin Only)

```
GET /api/v1/instances/{id}/ssh-allowed-ips
```

Returns the current allowed source IP list for SSH connections to an instance.

**Authorization**: Admin only.

**Response** `200 OK`:
```json
{
  "instance_id": 1,
  "allowed_ips": "192.168.1.0/24,10.0.0.0/8",
  "normalized_list": "192.168.1.0/24,10.0.0.0/8"
}
```

An empty `allowed_ips` string means all source IPs are allowed.

**Errors**: `400` invalid ID, `404` not found.

---

#### Update Allowed Source IPs (Admin Only)

```
PUT /api/v1/instances/{id}/ssh-allowed-ips
```

Updates the allowed source IP list for SSH connections to an instance.

**Authorization**: Admin only.

**Request Body**:
```json
{
  "allowed_ips": "192.168.1.0/24,10.0.0.0/8,2001:db8::/32"
}
```

Each entry must be a valid IPv4/IPv6 address or CIDR range. Set to empty string to allow all IPs.

**Response** `200 OK`:
```json
{
  "instance_id": 1,
  "allowed_ips": "192.168.1.0/24,10.0.0.0/8,2001:db8::/32",
  "normalized_list": "192.168.1.0/24,10.0.0.0/8,2001:db8::/32"
}
```

**Errors**: `400` invalid ID or invalid IP/CIDR format, `404` not found, `500` database update failed.

---

### SSH Dashboard

#### Global SSH Status

```
GET /api/v1/ssh-status
```

Returns an overview of SSH connection status across all instances the current user has access to. Admins see all instances; non-admin users see only their assigned instances.

**Authorization**: Authenticated user.

**Response** `200 OK`:
```json
{
  "instances": [
    {
      "instance_id": 1,
      "instance_name": "bot-alpha",
      "display_name": "Bot Alpha",
      "instance_status": "running",
      "connection_state": "connected",
      "health": {
        "connected_at": "2026-02-21T10:30:00Z",
        "last_health_check": "2026-02-21T10:35:00Z",
        "uptime_seconds": 300,
        "successful_checks": 45,
        "failed_checks": 2,
        "healthy": true
      },
      "tunnel_count": 2,
      "healthy_tunnels": 2
    }
  ],
  "total_count": 1,
  "connected": 1,
  "reconnecting": 0,
  "failed": 0,
  "disconnected": 0
}
```

**Errors**: `500` database query failed.

---

#### SSH Metrics

```
GET /api/v1/ssh-metrics
```

Returns aggregated SSH metrics for visualization, including uptime distribution, health check success rates, and reconnection counts. Scoped to instances the current user can access.

**Authorization**: Authenticated user.

**Response** `200 OK`:
```json
{
  "uptime_buckets": [
    { "label": "< 1h", "count": 3 },
    { "label": "1–6h", "count": 5 },
    { "label": "6–24h", "count": 2 },
    { "label": "1–7d", "count": 1 },
    { "label": "> 7d", "count": 0 }
  ],
  "health_rates": [
    {
      "instance_name": "bot-alpha",
      "display_name": "Bot Alpha",
      "success_rate": 0.95,
      "total_checks": 100
    }
  ],
  "reconnection_counts": [
    {
      "instance_name": "bot-beta",
      "display_name": "Bot Beta",
      "count": 5
    }
  ]
}
```

**Errors**: `500` database query failed.

---

### SSH Audit Logs (Admin Only)

#### Query Audit Logs

```
GET /api/v1/ssh-audit-logs
```

Returns paginated SSH audit log entries with optional filtering.

**Authorization**: Admin only.

**Query Parameters**:
| Parameter | Type | Description |
|-----------|------|-------------|
| `instance_id` | uint | Filter by instance ID |
| `instance_name` | string | Filter by instance name |
| `event_type` | string | Filter by event type |
| `username` | string | Filter by username |
| `since` | string | RFC3339 timestamp, only entries after this time |
| `until` | string | RFC3339 timestamp, only entries before this time |
| `limit` | int | Max entries to return (default 50, max 1000) |
| `offset` | int | Pagination offset (default 0) |

**Response** `200 OK`:
```json
{
  "entries": [
    {
      "id": 1,
      "instance_id": 1,
      "instance_name": "bot-alpha",
      "event_type": "connection",
      "username": "admin",
      "source_ip": "192.168.1.100",
      "details": "SSH connection established successfully",
      "duration_ms": 0,
      "created_at": "2026-02-21T10:30:00Z"
    }
  ],
  "total": 125,
  "limit": 50,
  "offset": 0
}
```

**Audit event types**:
| Event Type | Description |
|------------|-------------|
| `connection` | Successful SSH connection |
| `connection_failed` | SSH connection attempt failed |
| `disconnection` | SSH connection closed |
| `reconnection` | SSH reconnection attempt |
| `key_rotation` | SSH key pair was rotated |
| `fingerprint_mismatch` | SSH key fingerprint verification failed |
| `ip_restricted` | Source IP restriction violation |
| `health_check` | Health check event |
| `tunnel_status` | Tunnel status change |

**Errors**: `400` invalid query parameters, `500` database query failed, `503` audit system not initialized.

---

#### Purge Audit Logs

```
POST /api/v1/ssh-audit-logs/purge
```

Manually triggers deletion of old audit log entries.

**Authorization**: Admin only.

**Query Parameters**:
- `days` (optional): Number of days to retain. Uses the configured default if omitted.

**Response** `200 OK`:
```json
{
  "deleted": 1250,
  "retention_days": 90
}
```

**Errors**: `400` invalid days parameter, `500` purge operation failed, `503` audit system not initialized.

---

## Error Responses

All error responses follow this format:

```json
{
  "detail": "Human-readable error message"
}
```

| Status Code | Meaning |
|-------------|---------|
| 400 | Invalid request (bad JSON, validation error, missing SSH key) |
| 401 | Unauthenticated (no valid session) |
| 403 | Forbidden (insufficient permissions or instance not assigned) |
| 404 | Instance or resource not found |
| 409 | Conflict (e.g., instance name already exists, no available NodePorts) |
| 500 | Internal server error (K8s API failure, database error) |
| 503 | Service unavailable (SSH manager, orchestrator, or audit system not initialized) |
