# Docker Backend

An alternative to Kubernetes for running OpenClaw bot instances on a single machine using Docker.

## Prerequisites

- **Docker Desktop** (macOS/Windows) or **Docker Engine** (Linux)
- Docker socket accessible to the dashboard process

## Quick Start

```bash
# Set node IP to localhost for Docker Desktop
export CLAWORC_NODE_IP=127.0.0.1

# Optional: point to a custom Docker daemon
# export CLAWORC_DOCKER_HOST=tcp://192.168.1.50:2375

# Run the dashboard (from dashboard/)
poetry run uvicorn backend.app:app --reload --port 8000
```

The backend auto-detects Docker when Kubernetes is unavailable. Check `GET /health` — the `orchestrator_backend` field should read `"docker"`.

## Configuration

| Environment Variable | Default | Description |
|---|---|---|
| `CLAWORC_NODE_IP` | `192.168.1.104` | IP/hostname used in VNC URLs. Set to `127.0.0.1` for Docker Desktop. |
| `CLAWORC_DOCKER_CONFIG_DIR` | `/app/data/configs` | Host directory for per-instance config files (bind-mounted into containers). |
| `CLAWORC_DOCKER_HOST` | *(auto-detect)* | Docker daemon URL override. Leave unset to use the default socket. |

## Docker Desktop Compatibility

- `docker.DockerClient.from_env()` auto-detects Docker Desktop on macOS, Windows, and Linux
- `--privileged` containers are supported (they run inside Docker Desktop's Linux VM)
- Port mappings (`-p`) expose on `localhost` — set `CLAWORC_NODE_IP=127.0.0.1`
- Named volumes are managed by Docker Desktop's VM storage
- cgroups v2 (Docker Desktop default) works with the agent's systemd init

## Resource Mapping

| K8s Resource | Docker Equivalent |
|---|---|
| 4 PVCs (clawdbot, homebrew, clawd, chrome) | 4 named volumes (`claworc-{name}-{suffix}`) |
| Secret (`{name}-keys`) | Container environment variables (`-e`) |
| ConfigMap (`clawdbot.json`) | Bind-mounted host file (`{config_dir}/{name}/clawdbot.json`) |
| Deployment (privileged, systemd) | `docker run --privileged` with equivalent mounts |
| Service (NodePort) | Port mapping (`-p port_chrome:6081 -p port_terminal:6082`) |

## Per-Instance Resources

For an instance named `bot-mybot`:

- **Container**: `bot-mybot`
- **Volumes**: `claworc-bot-mybot-clawdbot`, `claworc-bot-mybot-homebrew`, `claworc-bot-mybot-clawd`, `claworc-bot-mybot-chrome`
- **Config file**: `{config_dir}/bot-mybot/clawdbot.json`
- **Ports**: Allocated from the same port range as K8s NodePorts (even = Chrome VNC, odd = Terminal VNC)

## Backend Selection

On startup the registry tries backends in order:

1. **Kubernetes** — loads in-cluster config, then falls back to kubeconfig
2. **Docker** — connects via `docker.from_env()` and pings the daemon
3. **None** — no orchestrator available

The resolved backend is persisted in the `settings` SQLite table. To force Docker, set `orchestrator_backend = "docker"` in the settings table (the auto-detection will skip the K8s attempt).

## Limitations vs Kubernetes

- **Secret updates require container recreation** — Docker has no native secret-update mechanism for running containers. Updating API keys briefly stops the container, recreates it with new env vars, and restarts. Volumes persist across recreation so no data is lost.
- **No rolling updates** — container restarts cause a brief downtime window.
- **Single-host only** — all instances run on the same Docker daemon.
- **No resource quotas** — CPU/memory limits are applied per-container but there is no cluster-level admission control.

## Running the Dashboard in Docker

To run the dashboard itself in a container while managing sibling containers on the host:

```bash
docker run -d \
  --name claworc-dashboard \
  -p 8000:8000 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v claworc-data:/app/data \
  -e CLAWORC_NODE_IP=127.0.0.1 \
  -e CLAWORC_DOCKER_CONFIG_DIR=/app/data/configs \
  your-registry/claworc-dashboard:latest
```

The key is mounting the Docker socket (`-v /var/run/docker.sock:/var/run/docker.sock`). This gives the dashboard access to the host's Docker daemon so it can create and manage sibling containers (not Docker-in-Docker).

The `CLAWORC_DOCKER_CONFIG_DIR` must point to a path that is the **same on both the host and inside the dashboard container**, since config files are bind-mounted from the host into bot containers. Using a Docker volume (`claworc-data`) for `/app/data` achieves this because Docker resolves bind mounts from the volume's host path.

## Troubleshooting

**"Docker not available" in logs**
- Verify Docker is running: `docker info`
- Check socket permissions: `ls -la /var/run/docker.sock`
- On Linux, add the dashboard user to the `docker` group or run as root

**Container fails to start**
- The agent image requires `--privileged` mode. Ensure Docker Desktop has not disabled privileged containers.
- Check `docker logs bot-{name}` for systemd init errors.

**Port conflicts**
- If a port is already in use, instance creation will fail. The dashboard allocates ports from `CLAWORC_PORT_START` to `CLAWORC_PORT_END` (default 30100–30199). Ensure this range is free.

**Config file not visible in container**
- The config file is bind-mounted from the host. If running the dashboard in Docker, ensure the config directory path is consistent between the host and dashboard container (see "Running the Dashboard in Docker" above).
