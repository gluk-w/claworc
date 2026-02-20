# Architecture

## System Overview

```
Browser
  |
  +---> Claworc Dashboard (192.168.1.204:8000)
  |       |
  |       +---> K8s API (via Python kubernetes client)
  |               |
  |               +---> Instance Pod "bot-alpha" (NodePorts 30100/30101)
  |               |       - Dual VNC: Chrome kiosk + xterm terminal
  |               |       - PVC: clawdbot-data (5Gi)
  |               |       - PVC: homebrew-data (10Gi)
  |               |       - PVC: clawd-data (5Gi)
  |               |       - PVC: chrome-data (5Gi)
  |               |       - Secret: API keys as env vars
  |               |       - ConfigMap: clawdbot.json
  |               |
  |               +---> Instance Pod "bot-bravo" (NodePorts 30102/30103)
  |               |       - (same structure)
  |               |
  |               +---> Instance Pod "bot-charlie" (NodePorts 30104/30105)
  |                       - (same structure)
  |
  +---> Chrome VNC:   http://192.168.1.104:<even-port>/vnc.html?autoconnect=true
  +---> Terminal VNC: http://192.168.1.104:<odd-port>/vnc.html?autoconnect=true
```

## Pod Architecture

Each bot instance runs as a **single pod** containing one container based on the existing moltbot Docker image. The container runs systemd as PID 1, which orchestrates **7 services** across two isolated X displays — no desktop environment or window manager:

```
Pod Container (privileged, systemd init)
  |
  |  Chrome VNC session (DISPLAY=:1)
  +-- xvnc-chrome.service        Xvnc :1 -rfbport 5901
  +-- novnc-chrome.service        noVNC proxy localhost:5901 → port 6081
  +-- chrome.service              google-chrome --kiosk on DISPLAY=:1
  |
  |  Terminal VNC session (DISPLAY=:2)
  +-- xvnc-term.service           Xvnc :2 -rfbport 5902
  +-- novnc-term.service          noVNC proxy localhost:5902 → port 6082
  +-- terminal.service            xterm -maximized on DISPLAY=:2
  |
  |  Application
  +-- openclaw-gateway.service    openclaw gateway on DISPLAY=:1
```

### Why Single Pod?

Chrome and clawdbot must share the same pod because:

1. **Shared DISPLAY**: The openclaw gateway needs access to `DISPLAY=:1` for Chrome browser automation
2. **systemd orchestration**: The image uses systemd to manage service dependencies across both VNC sessions
3. **Existing image reuse**: The current moltbot Docker image works as-is without modifications

Running Chrome in a separate sidecar container would require IPC mechanisms for display sharing and would break the systemd service dependency chain.

### Why Dual VNC / No Desktop Environment?

- **No XFCE4**: Removing the desktop environment eliminates unnecessary overhead and prevents users from accidentally closing Chrome or terminal windows
- **Isolated displays**: Each VNC session shows exactly one application — no Alt+Tab, no window switching
- **Chrome kiosk mode**: Fullscreen, no window decorations, no address bar manipulation
- **xterm terminal**: Minimal terminal emulator with no extra dependencies

## Container Image

The moltbot Docker image (`glukw/openclaw-vnc-chrome:latest`) is a multi-stage build based on Ubuntu 24.04:

**Builder stage**: Installs Node.js 22 and the `clawdbot` npm package globally.

**Runtime stage** includes:
- TigerVNC + noVNC v1.4.0 for remote desktop (dual sessions)
- xterm for terminal access (no desktop environment)
- Google Chrome with `--no-sandbox --kiosk` wrapper (required for container environment)
- Python 3 with pip, pipx, poetry, and uv
- Homebrew (linuxbrew) for additional package management
- systemd as init system with seven unit files

The container requires **privileged security context** for systemd operation and uses several special volume mounts (cgroup, /run as tmpfs, /dev/shm as 2Gi tmpfs).

## Kubernetes Resource Model

For each bot instance, Claworc creates the following resources in the `claworc` namespace:

| Resource | Name Pattern | Purpose |
|----------|-------------|---------|
| Deployment | `bot-{name}` | Pod management with Recreate strategy |
| Service | `bot-{name}` | ClusterIP service exposing HTTP (3000) and mTLS tunnel (3001) |
| PVC | `bot-{name}-clawdbot` | Clawdbot data |
| PVC | `bot-{name}-homebrew` | Homebrew packages |
| PVC | `bot-{name}-clawd` | Clawd working directory |
| PVC | `bot-{name}-chrome` | Google Chrome profile |
| Secret | `bot-{name}-keys` | API keys as environment variables |
| ConfigMap | `bot-{name}-config` | clawdbot.json configuration file |

## Networking

### Tunnel Architecture

All agent communication is routed through a multiplexed mTLS tunnel between the control plane and each agent. The control plane dials the agent's tunnel listener on port 3001, and all services (terminal, files, logs, desktop, chat) are multiplexed over this single connection using yamux.

Each agent exposes two ports via a ClusterIP service:
- **Port 3000**: HTTP proxy (internal, agent health checks and Neko gateway WebSocket)
- **Port 3001**: mTLS tunnel listener (control plane connects here)

### Service Configuration

```yaml
apiVersion: v1
kind: Service
metadata:
  name: bot-{name}
  namespace: claworc
spec:
  type: ClusterIP
  ports:
    - name: http
      port: 3000
      targetPort: 3000
      protocol: TCP
    - name: tunnel
      port: 3001
      targetPort: 3001
      protocol: TCP
  selector:
    app: bot-{name}
```

## Persistence

### Volume Mounts

Each pod has the following volume configuration:

```yaml
volumeMounts:
  # Persistent data
  - name: clawdbot-data
    mountPath: /root/.clawdbot
  - name: chrome-data
    mountPath: /root/.config/google-chrome
  - name: homebrew-data
    mountPath: /home/linuxbrew/.linuxbrew
  - name: clawd-data
    mountPath: /root/clawd
  # Config (from ConfigMap, separate from PVC)
  - name: config
    mountPath: /etc/clawdbot/clawdbot.json
    subPath: clawdbot.json
  # System mounts (required for systemd)
  - name: cgroup
    mountPath: /sys/fs/cgroup
  - name: run
    mountPath: /run                      # tmpfs (Memory)
  - name: tmp
    mountPath: /tmp
  - name: dshm
    mountPath: /dev/shm                  # tmpfs (Memory, 2Gi)
```

The key design decision is keeping the ConfigMap mount (`/etc/clawdbot/clawdbot.json`) separate from the PVC mount (`/root/.clawdbot`). This allows the orchestrator to update configuration by patching the ConfigMap without touching persistent data.

### Chrome Profile Persistence

Chrome's user profile directory is stored on a dedicated `chrome-data` PVC:

- PVC `bot-{name}-chrome` is mounted at `/root/.config/google-chrome`
- This gives Chrome its own persistent storage, separate from clawdbot data

This allows users to sign into their Google account in Chrome, install extensions, and save bookmarks -- all persisting across pod restarts and stop/start cycles.

## API Key Injection

API keys flow from the orchestrator to bot instances via Kubernetes Secrets:

```
SQLite (encrypted with Fernet)
  |
  v
Claworc resolves effective keys (instance override or global fallback)
  |
  v
K8s Secret "bot-{name}-keys"
  data:
    ANTHROPIC_API_KEY: <base64>
    OPENAI_API_KEY: <base64>
    BRAVE_API_KEY: <base64>
  |
  v
Pod env vars (envFrom secretRef)
  |
  v
clawdbot.json references ${ANTHROPIC_API_KEY} etc.
```

## RBAC

Claworc runs with a ServiceAccount scoped to the `claworc` namespace:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: claworc-manager
  namespace: claworc
rules:
  - apiGroups: ["", "apps"]
    resources:
      - deployments
      - services
      - persistentvolumeclaims
      - configmaps
      - secrets
      - pods
      - pods/log
    verbs:
      - create
      - get
      - list
      - watch
      - update
      - patch
      - delete
```

This limits the orchestrator to managing resources only within its own namespace.

## Claworc Deployment

Claworc itself is deployed as a Kubernetes pod with its own Helm chart:

- **Namespace**: `claworc`
- **Image**: Built from the project's Dockerfile (FastAPI backend serving React static files)
- **Service**: Exposed at `192.168.1.204:8000` (or via NodePort/LoadBalancer)
- **Storage**: SQLite database on a PVC for persistence
- **ServiceAccount**: With the RBAC role above for K8s API access

## Design Decision Rationale

| Decision | Rationale |
|----------|-----------|
| Python kubernetes client | Clean typed API; avoids shelling out to kubectl or Helm SDK complexity |
| SQLite | Simple, file-based, sufficient for 5-20 instances; no external DB dependency |
| Fernet encryption | Standard symmetric encryption for API keys at rest in SQLite |
| NodePort pairs 30100-30199 | MetalLB pool too small for LB-per-instance; consecutive pairs for Chrome/Terminal |
| Single pod (not sidecar) | Shared DISPLAY=:1, systemd service deps, existing image works as-is |
| Dual VNC / no WM | Isolated displays prevent accidental window management; Chrome kiosk + xterm only |
| ConfigMap separate from PVC | Enables config updates without touching persistent data |
| Dedicated Chrome PVC | Separate PVC for Chrome profile keeps storage concerns isolated |
| No authentication | Internal network tool; adding auth would add complexity without value |
| FastAPI + React | Modern stack, async-capable backend, rich frontend ecosystem |
| Poetry | Project-mandated Python dependency management |
