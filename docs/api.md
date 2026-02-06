# REST API

## Base URL

```
http://192.168.1.204:8000/api/v1
```

No authentication required (internal network tool).

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

## Error Responses

All error responses follow this format:

```json
{
  "detail": "Human-readable error message"
}
```

| Status Code | Meaning |
|-------------|---------|
| 400 | Invalid request (bad JSON, validation error) |
| 404 | Instance not found |
| 409 | Conflict (e.g., instance name already exists, no available NodePorts) |
| 500 | Internal server error (K8s API failure, database error) |
