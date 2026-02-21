# Getting Started

This guide walks you through creating your first instance and understanding the SSH-based connectivity that powers all communication between Claworc and your agent instances.

## Prerequisites

- Claworc installed and running (see [Installation](install.md))
- Access to the dashboard (default: `http://localhost:8000` for Docker, `http://<node-ip>:30000` for Kubernetes)

## Creating an Instance

### 1. Open the Dashboard

Navigate to the Claworc dashboard in your browser. Log in with your admin credentials.

### 2. Create a New Instance

Click **Create Instance** and provide:

- **Display Name** — a human-readable name (e.g., "Research Bot"). This is converted to a Kubernetes-safe name automatically (`bot-research-bot`).
- **API Keys** (optional) — override global API keys for this instance, or leave blank to use the global defaults configured in Settings.

### 3. SSH Key Generation

When you create an instance, Claworc automatically:

1. **Generates an ED25519 key pair** unique to this instance
2. **Stores the private key** on disk at `/app/data/ssh-keys/{name}.key` with restrictive permissions (`0600`)
3. **Records the public key and fingerprint** in the database
4. **Deploys the public key** to the agent's `/root/.ssh/authorized_keys` once the container starts

No manual SSH configuration is needed. The control plane handles all key generation, distribution, and connection management transparently.

### 4. Wait for the Instance to Start

After creation, the instance goes through these states:

| State | Meaning |
|-------|---------|
| `creating` | Container/pod is being provisioned |
| `running` | Agent is up and SSH connection is being established |
| `stopped` | Instance scaled to zero (data preserved) |
| `error` | Something went wrong — check logs |

Once the instance reaches `running`, the control plane establishes an SSH connection and creates tunnels for desktop and chat access.

## Understanding SSH Connection Indicators

Every running instance shows its SSH connection status in the dashboard. These indicators appear on the instance detail page and the SSH dashboard.

### Connection States

| Indicator | Color | Meaning |
|-----------|-------|---------|
| **Connected** | Green | SSH connection is active and healthy |
| **Connecting** | Yellow | Initial connection in progress |
| **Reconnecting** | Yellow | Lost connection, attempting to reconnect with exponential backoff |
| **Disconnected** | Gray | No active connection (instance may be stopped) |
| **Failed** | Red | All reconnection attempts exhausted |

### Instance Detail View

On each instance's detail page, you'll find:

- **SSH Status** — current connection state with a colored badge and dot indicator
- **Health Metrics** — uptime, last health check, success/failure counts
- **Tunnel Details** — expandable section showing active tunnels (Desktop and Gateway) with their health status, local/remote ports, and bytes transferred
- **Connection Events** — timeline of SSH events (connections, disconnections, reconnections) with timestamps and color-coded severity

### SSH Dashboard

Navigate to the SSH Dashboard for a cluster-wide view:

- **Quick Stats** — cards showing connected, reconnecting, failed, and disconnected instance counts
- **Instance Table** — sortable list of all instances with SSH state, tunnel health (e.g., "2/2" meaning 2 healthy out of 2 tunnels), and uptime
- **Metrics** — charts for connection uptime distribution, health check success rates, and reconnection attempt counts

## Using Your Instance

Once an instance is running and the SSH connection shows "Connected":

### Browser Access

Click the **Desktop** tab to see the agent's Chrome browser in real time. This streams through an SSH tunnel — no direct port access to the agent is needed.

### Terminal Access

Click the **Terminal** tab to open an interactive shell session on the agent. This uses an SSH PTY session relayed over WebSocket.

### Chat

Use the **Chat** tab to send instructions to the AI agent and receive responses. Chat traffic flows through an SSH tunnel to the agent's Gateway service.

### File Management

The **Files** tab lets you browse, upload, and download files in the agent's workspace. File operations use SSH exec sessions under the hood.

### Logs

The **Logs** tab streams live logs from the agent via SSH exec sessions running `tail -F`.

## SSH Key Rotation

SSH keys can be rotated without downtime:

- **Automatic rotation** — enabled by default with a 90-day policy (configurable per-instance)
- **Manual rotation** — available from the instance settings or via the API:

```bash
curl -X POST http://localhost:8000/api/v1/instances/{id}/rotate-ssh-key
```

Rotation follows a safe sequence: generate new key, add it to the agent, verify it works, remove the old key. At no point is the instance unreachable.

## Troubleshooting

### Instance Won't Connect

1. Check that the instance status is `running` (the agent container must be up)
2. Look at the SSH connection state on the instance detail page
3. Check the Connection Events timeline for error details
4. Use the **Troubleshoot SSH** button on the instance detail page for diagnostics

### Tunnels Show "Unhealthy"

1. Verify the agent services are running (sshd, Selkies VNC, Gateway)
2. Check the tunnel details — the "Last Error" column may indicate the issue
3. Try forcing a reconnection:

```bash
curl -X POST http://localhost:8000/api/v1/instances/{id}/ssh-reconnect
```

### Need More Help?

- [SSH Troubleshooting Guide](troubleshooting/ssh-troubleshooting.md) — detailed diagnostic procedures for every failure scenario
- [SSH Operations Runbook](operations/ssh-operations.md) — monitoring, key rotation, and performance tuning
- [SSH Configuration Reference](configuration/ssh-configuration.md) — environment variables, schema, and deployment examples
