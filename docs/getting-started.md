# Getting Started

This guide walks you through your first time using Claworc after installation. For installation instructions, see [Installation](install.md).

## First Startup

When Claworc starts for the first time, it automatically:

1. **Creates the SQLite database** (`claworc.db`) in the data directory
2. **Generates a global SSH key pair** — an ED25519 key pair is written to `ssh_key` and `ssh_key.pub` in the same data directory. This key pair authenticates the control plane with all agent instances. No manual key management is needed.
3. **Generates a Fernet encryption key** for encrypting API keys at rest

All three artifacts live in the same data directory (default `/app/data`, configurable via `CLAWORC_DATA_PATH`):

```
{CLAWORC_DATA_PATH}/
├── claworc.db        # SQLite database
├── ssh_key           # ED25519 private key (auto-generated)
└── ssh_key.pub       # ED25519 public key (auto-generated)
```

In Docker, this directory is backed by a volume mount. In Kubernetes, it is backed by a PVC. Either way, the SSH keys and database persist across restarts — no separate volume or configuration is required.

## Creating Your First Instance

1. Open the Claworc dashboard in your browser
2. Log in with the default admin credentials (set during installation)
3. Click **Create Instance** and give it a name (e.g., "My Agent")
4. Optionally configure API keys (Anthropic, OpenAI, Brave) or use the global defaults from Settings
5. Click **Create**

Claworc will spin up an isolated container with Chrome, a terminal, and the OpenClaw agent. Once the container is running, the control plane automatically:

- Uploads its SSH public key to the instance's `/root/.ssh/authorized_keys`
- Establishes a multiplexed SSH connection
- Creates tunnels for Chrome/VNC and the OpenClaw gateway
- Starts health monitoring

This happens transparently — no SSH configuration is needed on your part.

## SSH Connection Indicators

The instance detail page (Overview tab) shows real-time SSH connection status. Here is what each indicator means:

### Connection State

A colored dot next to the instance name indicates the current SSH connection state:

| Indicator | State | Meaning |
|-----------|-------|---------|
| Green dot | **Connected** | SSH connection is active and healthy |
| Yellow dot | **Connecting** | Initial SSH connection is being established |
| Yellow dot | **Reconnecting** | Connection was lost; automatic reconnection is in progress |
| Gray dot | **Disconnected** | No SSH connection (instance may be stopped) |
| Red dot | **Failed** | All reconnection attempts exhausted; manual intervention may be needed |

### Health Metrics

Below the connection state, you will see:

- **Uptime** — how long the current SSH connection has been active
- **Last Health Check** — when the last successful health check ran
- **Health Checks** — count of successful vs. failed checks (e.g., "42 ok / 0 failed")

### Tunnel Status

Active SSH tunnels are shown as inline badges:

- **Green badge** — tunnel is healthy (local TCP port is accepting connections)
- **Red badge** — tunnel has an error (will be automatically recreated by the reconciliation loop)

Click **Tunnel Details** to expand a table showing each tunnel's local port, remote port, health check counts, and last check timestamp.

### Connection Events

Click **Connection Events** to see a timeline of SSH lifecycle events:

- `connected` / `reconnected` (green) — connection established
- `key_uploaded` (blue) — public key was uploaded to the instance
- `disconnected` / `reconnecting` (yellow) — connection lost or reconnecting
- `reconnect_failed` / `health_check_failed` (red) — failure events

Events can be filtered by type using the dropdown. This timeline is useful for understanding connection history and diagnosing issues.

### Troubleshoot Dialog

Click the **Troubleshoot** button next to the SSH status to access:

- **Connection Test** — run an end-to-end SSH connectivity test with latency measurement
- **Manual Reconnect** — force a reconnection attempt
- **SSH Public Key Fingerprint** — view the global SSH fingerprint and full public key for verification

## Verifying SSH Keys

The global SSH public key fingerprint is displayed in two places:

1. **Settings page** — under the SSH Public Key Fingerprint section
2. **Troubleshoot dialog** — on any instance's Overview tab

Use this fingerprint to verify that the control plane is using the expected key pair, especially after key rotation.

## Troubleshooting

### Instance shows "Disconnected" but the container is running

The control plane may not have been able to upload its SSH key or establish a connection. Check:

1. **Network connectivity** — ensure the control plane can reach the agent's SSH port (port 22)
2. **Container health** — verify the agent's SSH server is running (`sshd` service inside the container)
3. **Connection events** — check the event timeline on the Overview tab for error details
4. Use the **Troubleshoot** button to run a manual connection test

### Instance shows "Failed" state

This means all automatic reconnection attempts (up to 10, with exponential backoff) have been exhausted. Common causes:

- The agent container crashed and hasn't restarted
- A network partition between the control plane and agent
- SSH server configuration issues inside the agent

To recover:
1. Verify the agent container is running
2. Use the **Troubleshoot** dialog to attempt a manual reconnect
3. If the container has issues, try restarting the instance from the dashboard

### Tunnels showing "error" status

Tunnel errors mean the local TCP listener for a tunnel has failed. The reconciliation loop (every 60 seconds) will automatically attempt to recreate failed tunnels. If errors persist:

1. Check the Connection Events for related error details
2. Verify the target service (VNC on port 3000, gateway on port 8080) is running inside the agent
3. Try a manual reconnect from the Troubleshoot dialog

### SSH key issues after a pod restart

When an agent container restarts, it loses its `/root/.ssh/authorized_keys` file. The control plane handles this automatically — it re-uploads the public key before every reconnection attempt. If you still experience issues, check the Connection Events for `key_uploaded` events to confirm the key upload succeeded.

For detailed SSH architecture documentation, see [SSH Connectivity Architecture](ssh-connectivity.md).

For operational troubleshooting and deployment guides, see [Installation](install.md).
