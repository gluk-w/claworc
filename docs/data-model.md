# Data Model

## Database

Claworc uses **SQLite** as its database. SQLite was chosen because:

- The expected scale is 5-20 instances -- well within SQLite's capacity
- No external database dependency to configure or maintain
- Single-file database, easy to back up (PVC snapshot)
- Async access via `aiosqlite` for non-blocking FastAPI handlers

The database file is stored on a PVC mounted to the Claworc pod for persistence.

## Schema

### instances

Stores the configuration and metadata for each bot instance.

```sql
CREATE TABLE instances (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,              -- K8s-safe name: "bot-alpha"
    display_name TEXT NOT NULL,             -- Human-readable name: "Bot Alpha"
    nodeport_chrome INTEGER UNIQUE NOT NULL, -- Allocated even port: 30100, 30102, ...
    nodeport_terminal INTEGER UNIQUE NOT NULL, -- Derived odd port: nodeport_chrome + 1
    status TEXT NOT NULL DEFAULT 'creating',-- creating, running, stopped, error
    cpu_request TEXT DEFAULT '500m',
    cpu_limit TEXT DEFAULT '2000m',
    memory_request TEXT DEFAULT '1Gi',
    memory_limit TEXT DEFAULT '4Gi',
    storage_clawdbot TEXT DEFAULT '5Gi',
    storage_homebrew TEXT DEFAULT '10Gi',
    storage_clawd TEXT DEFAULT '5Gi',
    anthropic_api_key TEXT,                 -- Fernet-encrypted, NULL = use global
    openai_api_key TEXT,                    -- Fernet-encrypted, NULL = use global
    brave_api_key TEXT,                     -- Fernet-encrypted, NULL = use global
    clawdbot_config TEXT,                   -- JSON string (clawdbot.json content)
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

**Field Details**:

| Field | Description |
|-------|-------------|
| `name` | Derived from `display_name` by lowercasing, replacing spaces with hyphens, and prefixing with `bot-`. Must be unique and K8s-valid. |
| `nodeport_chrome` | Auto-allocated even port from range 30100-30198. Released on instance deletion. |
| `nodeport_terminal` | Always `nodeport_chrome + 1` (odd port). Allocated as a pair. |
| `status` | Database status, enriched with live K8s pod status when returned via API. |
| `anthropic_api_key` / `openai_api_key` / `brave_api_key` | Encrypted with Fernet. NULL means the instance inherits the global key from the settings table. |
| `clawdbot_config` | The raw JSON content of the clawdbot.json configuration file. Stored as a TEXT field. |
| `cpu_request` / `cpu_limit` / `memory_request` / `memory_limit` | Kubernetes resource specifications. Stored as text in K8s format (e.g., "500m", "2Gi"). |
| `storage_*` | PVC size specifications for each of the three persistent volumes. |

### settings

Key-value store for global settings.

```sql
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,                    -- Fernet-encrypted for API keys
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

**Known Keys**:

| Key | Description | Encrypted |
|-----|-------------|-----------|
| `anthropic_api_key` | Global Anthropic API key | Yes |
| `openai_api_key` | Global OpenAI API key | Yes |
| `brave_api_key` | Global Brave Search API key | Yes |
| `default_cpu_request` | Default CPU request for new instances | No |
| `default_cpu_limit` | Default CPU limit for new instances | No |
| `default_memory_request` | Default memory request | No |
| `default_memory_limit` | Default memory limit | No |
| `default_storage_clawdbot` | Default clawdbot PVC size | No |
| `default_storage_homebrew` | Default homebrew PVC size | No |
| `default_storage_clawd` | Default clawd PVC size | No |
| `fernet_key` | The Fernet encryption key itself | No (this IS the key) |

## Encryption

API keys are encrypted at rest using **Fernet symmetric encryption** from the `cryptography` Python library.

- A Fernet key is generated on first run and stored in the `settings` table
- All API key fields in both `instances` and `settings` tables are encrypted before storage
- Keys are decrypted only when needed: to populate K8s Secrets or to display masked values in the API
- The Fernet key itself should ideally be stored as a K8s Secret and injected as an environment variable, rather than in the SQLite database (implementation detail)

## Kubernetes Resources per Instance

In addition to the SQLite records, each instance maps to the following Kubernetes resources:

### Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: bot-{name}
  namespace: claworc
  labels:
    app: bot-{name}
    managed-by: claworc
spec:
  replicas: 1                    # 0 when stopped
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app: bot-{name}
  template:
    metadata:
      labels:
        app: bot-{name}
    spec:
      containers:
      - name: moltbot
        image: glukw/openclaw-vnc-chrome:latest:latest
        imagePullPolicy: Always
        securityContext:
          privileged: true
        ports:
        - containerPort: 6081
          name: novnc-chrome
        - containerPort: 6082
          name: novnc-term
        env:
        - name: VNC_RESOLUTION
          value: "1920x1080"
        - name: VNC_DEPTH
          value: "24"
        envFrom:
        - secretRef:
            name: bot-{name}-keys
        resources:
          requests:
            cpu: {cpu_request}
            memory: {memory_request}
          limits:
            cpu: {cpu_limit}
            memory: {memory_limit}
        volumeMounts:
        - name: clawdbot-data
          mountPath: /root/.clawdbot
        - name: chrome-data
          mountPath: /root/.config/google-chrome
        - name: homebrew-data
          mountPath: /home/linuxbrew/.linuxbrew
        - name: clawd-data
          mountPath: /root/clawd
        - name: config
          mountPath: /etc/clawdbot/clawdbot.json
          subPath: clawdbot.json
        - name: cgroup
          mountPath: /sys/fs/cgroup
        - name: run
          mountPath: /run
        - name: tmp
          mountPath: /tmp
        - name: dshm
          mountPath: /dev/shm
        livenessProbe:
          tcpSocket:
            port: 6081
          initialDelaySeconds: 60
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /
            port: 6081
          initialDelaySeconds: 30
          periodSeconds: 10
      volumes:
      - name: clawdbot-data
        persistentVolumeClaim:
          claimName: bot-{name}-clawdbot
      - name: homebrew-data
        persistentVolumeClaim:
          claimName: bot-{name}-homebrew
      - name: clawd-data
        persistentVolumeClaim:
          claimName: bot-{name}-clawd
      - name: chrome-data
        persistentVolumeClaim:
          claimName: bot-{name}-chrome
      - name: cgroup
        hostPath:
          path: /sys/fs/cgroup
          type: Directory
      - name: run
        emptyDir:
          medium: Memory
      - name: tmp
        emptyDir: {}
      - name: dshm
        emptyDir:
          medium: Memory
          sizeLimit: 2Gi
      imagePullSecrets:
      - name: ghcr-secret
```

### PersistentVolumeClaims

Four PVCs per instance:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: bot-{name}-clawdbot
  namespace: claworc
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: {storage_clawdbot}   # default 5Gi
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: bot-{name}-homebrew
  namespace: claworc
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: {storage_homebrew}   # default 10Gi
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: bot-{name}-clawd
  namespace: claworc
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: {storage_clawd}      # default 5Gi
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: bot-{name}-chrome
  namespace: claworc
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: {storage_chrome}      # default 5Gi
```

### Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: bot-{name}-keys
  namespace: claworc
type: Opaque
data:
  ANTHROPIC_API_KEY: {base64_encoded}
  OPENAI_API_KEY: {base64_encoded}
  BRAVE_API_KEY: {base64_encoded}
```

Values are the effective key for each provider: the instance override if set, otherwise the global key.

### ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: bot-{name}-config
  namespace: claworc
data:
  clawdbot.json: |
    {
      "key": "value",
      "api_key": "${ANTHROPIC_API_KEY}"
    }
```

### Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: bot-{name}-vnc
  namespace: claworc
spec:
  type: NodePort
  ports:
  - name: chrome
    port: 6081
    targetPort: 6081
    nodePort: {nodeport_chrome}        # 30100, 30102, 30104, ...
    protocol: TCP
  - name: terminal
    port: 6082
    targetPort: 6082
    nodePort: {nodeport_terminal}      # 30101, 30103, 30105, ...
    protocol: TCP
  selector:
    app: bot-{name}
```

## Name Generation

The K8s-safe `name` is derived from `display_name`:

1. Convert to lowercase
2. Replace spaces and underscores with hyphens
3. Remove characters that are not alphanumeric or hyphens
4. Trim leading/trailing hyphens
5. Prefix with `bot-`
6. Truncate to 63 characters (K8s label limit)
7. Verify uniqueness against existing instances

Examples:
- "Bot Alpha" -> `bot-alpha`
- "My Test Bot #3" -> `bot-my-test-bot-3`
- "Production WhatsApp" -> `bot-production-whatsapp`

## NodePort Allocation

The port allocator manages consecutive even/odd pairs in the range 30100-30199 (max 50 instances):

1. Query all allocated `nodeport_chrome` values from the `instances` table
2. Find the lowest unallocated even port in the range (30100, 30102, 30104, ...)
3. If no port pairs available, return an error (409 Conflict)
4. Assign `nodeport_chrome` = even port, `nodeport_terminal` = even port + 1, atomically (within transaction)
5. On instance deletion, the port pair is implicitly freed when the row is deleted
