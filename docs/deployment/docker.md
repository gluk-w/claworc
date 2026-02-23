# Docker Deployment Guide

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and Docker Compose installed and running
- Docker socket accessible to the control plane process

## Installation

See the [Installation Guide](../install.md) for step-by-step instructions using the installer script or Docker Compose.

## Quick Start

```bash
git clone https://github.com/gluk-w/claworc.git
cd claworc
mkdir -p ~/.claworc/data/configs
CLAWORC_DATA_DIR=~/.claworc/data docker compose up -d
```

The dashboard is available at **http://localhost:8000**.

## Data Persistence

The control plane stores all persistent state in a single data directory (`CLAWORC_DATA_PATH`, default `/app/data`):

```
/app/data/
├── claworc.db          # SQLite database (instances, settings, audit logs)
├── ssh_key             # ED25519 private key (mode 0600)
├── ssh_key.pub         # ED25519 public key (mode 0644)
└── configs/            # Per-instance config files (bind-mounted into containers)
```

**SSH key files are stored in the same data volume as the database — no separate volume mount is needed.** The `docker-compose.yml` maps a single named volume (`claworc-data`) or host directory to `/app/data`, which persists the database, SSH keys, and config files together.

```yaml
# docker-compose.yml
services:
  control-plane:
    volumes:
      - claworc-data:/app/data    # persists DB + SSH keys + configs
```

On first startup, the control plane auto-generates the ED25519 key pair if the files don't exist. As long as the data volume persists, SSH keys are retained across container restarts and upgrades.

> **Warning:** Running `docker compose down -v` removes the named volume, deleting the database and SSH keys. The control plane will regenerate new keys on next startup. Existing agent containers will still have the old public key, but this is handled automatically — the control plane re-uploads the new public key before each connection.

## Network Configuration for SSH Connectivity

### How SSH Networking Works in Docker

The control plane and agent containers all run on the same Docker host. The control plane connects to agent containers via SSH (TCP port 22) using the Docker bridge network:

```
┌──────────────────────────────────────────────────────┐
│                      Docker Host                      │
│                                                       │
│  ┌─────────────────┐        ┌──────────────────────┐ │
│  │  Control Plane   │──SSH──▶│  Agent: bot-alpha    │ │
│  │  claworc:8000    │  :22   │  sshd on port 22     │ │
│  └─────────────────┘        └──────────────────────┘ │
│         │                           │                 │
│         │                   ┌──────────────────────┐ │
│         │──────────SSH────▶│  Agent: bot-bravo     │ │
│                      :22   │  sshd on port 22      │ │
│                            └──────────────────────┘ │
│                                                       │
│  Docker bridge network (172.17.0.0/16)                │
└──────────────────────────────────────────────────────┘
```

The control plane discovers each agent's IP address and port via the Docker API — no manual network configuration is needed. SSH port 22 on agent containers does **not** need to be published to the host because the control plane connects over the Docker bridge network.

### Docker Desktop vs Docker Engine

| Environment | Network Behavior |
|-------------|-----------------|
| **Docker Engine (Linux)** | Control plane connects to agent containers via Docker bridge network IPs. No extra configuration needed. |
| **Docker Desktop (macOS/Windows)** | Containers run inside a Linux VM. Bridge networking works transparently within the VM. Set `CLAWORC_NODE_IP=127.0.0.1`. |

### Firewall Considerations

Since SSH traffic stays within the Docker bridge network, no host firewall rules are needed for SSH connectivity between the control plane and agents. You only need to ensure:

- **Port 8000** is accessible on the host for the dashboard UI
- The **Docker socket** (`/var/run/docker.sock`) is mounted into the control plane container

### Docker Socket Access

The control plane needs access to the Docker socket to:

1. Create and manage agent containers
2. Execute commands inside agent containers (for SSH public key upload via `docker exec`)
3. Discover agent container IP addresses for SSH connections

```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
```

On Linux, ensure the user running the control plane container has permission to access the Docker socket (typically requires membership in the `docker` group or running as root).

## Running the Dashboard in Docker

When running the control plane itself in a Docker container:

```bash
docker run -d \
  --name claworc-dashboard \
  -p 8000:8000 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v claworc-data:/app/data \
  -e CLAWORC_NODE_IP=127.0.0.1 \
  -e CLAWORC_DOCKER_CONFIG_DIR=/app/data/configs \
  glukw/claworc:latest
```

Key points:
- The Docker socket mount gives the control plane access to create sibling containers (not Docker-in-Docker)
- The `claworc-data` volume persists the database, SSH keys, and per-instance configs
- `CLAWORC_DOCKER_CONFIG_DIR` must be a path consistent between the host and dashboard container (Docker resolves bind mounts from the volume's host path)

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `CLAWORC_DATA_PATH` | `/app/data` | Data directory for database, SSH keys, and configs |
| `CLAWORC_NODE_IP` | `192.168.1.104` | IP for VNC URLs. Set to `127.0.0.1` for Docker Desktop. |
| `CLAWORC_DOCKER_CONFIG_DIR` | `/app/data/configs` | Host directory for per-instance config files |
| `CLAWORC_DOCKER_HOST` | *(auto-detect)* | Docker daemon URL override |
| `CLAWORC_TERMINAL_HISTORY_LINES` | `1000` | Terminal scrollback buffer size (0 to disable) |
| `CLAWORC_TERMINAL_SESSION_TIMEOUT` | `30m` | Idle detached terminal session timeout |

## SSH-Specific Deployment Checklist (Docker)

Use this checklist when deploying or upgrading Claworc with Docker:

- [ ] **Data volume exists** — Verify the named volume or host directory is configured (`docker volume ls | grep claworc`)
- [ ] **Data directory is writable** — The control plane must be able to write `ssh_key`, `ssh_key.pub`, and `claworc.db`
- [ ] **SSH keys generated** — After first startup, check control plane logs for "Generated new SSH key pair" or verify via `/health`
- [ ] **Docker socket is mounted** — The control plane needs `/var/run/docker.sock` for container management and SSH key upload via `docker exec`
- [ ] **Docker socket permissions** — On Linux, verify the container user can access the Docker socket
- [ ] **Bridge network connectivity** — Agent containers are reachable from the control plane on the Docker bridge network (automatic, no config needed)
- [ ] **Health endpoint responds** — `curl http://localhost:8000/health` returns `orchestrator_backend: "docker"`
- [ ] **SSH connections establish** — Create a test instance and verify the SSH connection indicator turns green in the UI
- [ ] **Tunnels are functional** — After SSH connects, verify Chrome (VNC) and terminal access work through the UI

## Troubleshooting

### SSH connection fails to agent container

1. Check control plane logs: `docker logs -f claworc-dashboard`
2. Verify the agent container is running: `docker ps --filter "name=bot-"`
3. Test SSH key upload: `docker exec bot-<name> cat /root/.ssh/authorized_keys`
4. Test network connectivity: `docker exec claworc-dashboard nc -zv <agent-container-ip> 22`

### Agent container not reachable

If the control plane cannot connect to an agent's SSH port:

1. Verify the agent is on the same Docker network: `docker inspect bot-<name> --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'`
2. Check if the agent's sshd is running: `docker exec bot-<name> ps aux | grep sshd`
3. Check sshd logs: `docker exec bot-<name> cat /var/log/claworc/sshd.log`

### SSH keys lost after volume removal

If you ran `docker compose down -v` or deleted the data volume, new SSH keys are generated on next startup. The control plane automatically re-uploads the new public key to agents before connecting — no manual intervention needed.

### Docker socket permission denied

```
Got permission denied while trying to connect to the Docker daemon socket
```

On Linux, either:
- Add the container user to the `docker` group
- Run the container as root
- Adjust socket permissions: `chmod 666 /var/run/docker.sock` (less secure)

See also: [Docker Backend](../docker.md) for Docker-specific backend behavior and limitations, and [SSH Connectivity Architecture](../ssh-connectivity.md) for detailed SSH troubleshooting.
