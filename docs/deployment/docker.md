# Docker Deployment Guide

This guide covers deploying Claworc with Docker, including SSH-based connectivity between the control plane and agent containers.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and Docker Compose installed
- Docker socket accessible to the control plane process

## Architecture Overview

```
 ┌─────────────────────────────────────────────────────┐
 │                Docker Host                           │
 │                                                     │
 │  ┌───────────────────────┐   SSH (port 22)          │
 │  │   Control Plane       ├──────────────────┐       │
 │  │   (claworc)           │                  │       │
 │  │                       │          ┌───────┴─────┐ │
 │  │   /app/data           │          │  bot-foo    │ │
 │  │   ├── claworc.db      │          │  (agent)    │ │
 │  │   └── ssh-keys/       │          │             │ │
 │  └───────┬───────────────┘          │  sshd :22   │ │
 │          │                          │  VNC :3000  │ │
 │    ┌─────┴──────┐                   │  GW  :8080  │ │
 │    │ claworc-   │                   └─────────────┘ │
 │    │ data vol   │                                   │
 │    └────────────┘    ── claworc bridge network ──   │
 └─────────────────────────────────────────────────────┘
```

The control plane is the SSH **client**. Each agent container runs an SSH **server** (sshd). All communication — tunnels, file operations, log streaming, terminal sessions — travels over SSH on the Docker bridge network.

## Installation

### Docker Compose (Recommended)

```bash
git clone https://github.com/gluk-w/claworc.git
cd claworc
docker compose up -d
```

The dashboard is available at **http://localhost:8000**.

### Standalone Container

```bash
docker run -d \
  --name claworc \
  -p 8000:8000 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v claworc-data:/app/data \
  glukw/claworc:latest
```

## Volume Mounts for SSH Keys

### Control Plane Data Volume

The control plane stores both the SQLite database and SSH private keys in a single data volume:

| Path | Contents | Permissions |
|------|----------|-------------|
| `/app/data/claworc.db` | SQLite database (instance metadata, SSH public keys, fingerprints, audit logs) | 0644 |
| `/app/data/ssh-keys/` | ED25519 private key files, one per instance | Directory: 0700 |
| `/app/data/ssh-keys/{name}.key` | Instance private key (PEM-encoded) | 0600 |

**Docker Compose volume:**

```yaml
services:
  control-plane:
    image: glukw/claworc:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - claworc-data:/app/data    # Database + SSH keys

volumes:
  claworc-data:                    # Named volume, persists across restarts
```

**Named volume vs bind mount:**

| Method | Pros | Cons |
|--------|------|------|
| Named volume (`claworc-data:`) | Docker manages lifecycle; portable; works on all platforms | Harder to browse from host |
| Bind mount (`./data:/app/data`) | Easy host access for backups; visible in file explorer | Platform-specific paths; permission issues on Linux |

If using a bind mount, ensure the directory has correct ownership:

```bash
mkdir -p ./data/ssh-keys
chmod 700 ./data/ssh-keys
```

### Agent Container Volumes

Each agent instance creates 3 named volumes for application data:

| Volume Name | Container Mount | Purpose |
|------------|----------------|---------|
| `claworc-{name}-homebrew` | `/home/linuxbrew/.linuxbrew` | Homebrew package cache |
| `claworc-{name}-clawd` | `/config/clawd` | OpenClaw data |
| `claworc-{name}-chrome` | `/config/chrome-data` | Chrome browser profile |

Agent volumes do **not** store SSH keys. The agent's SSH authorized keys are written to the container filesystem (`/root/.ssh/authorized_keys`) at startup via `docker exec`.

## Network Configuration for SSH Connectivity

### Bridge Network

The control plane automatically creates a `claworc` bridge network and attaches all agent containers to it:

```
claworc (bridge)
├── claworc       (control plane)  → 172.19.0.2
├── bot-alice     (agent)          → 172.19.0.3
└── bot-bob       (agent)          → 172.19.0.4
```

SSH connections use container IP addresses on this network. The control plane discovers the agent IP by inspecting the container's network settings.

### How SSH Traffic Flows

1. Control plane calls `docker inspect bot-{name}` to get the container's IP address on the `claworc` network.
2. Control plane connects to `{container-ip}:22` using the instance's ED25519 private key.
3. SSH tunnels are established for VNC (port 3000) and Gateway (port 8080), bound to `127.0.0.1` with ephemeral local ports.
4. All file ops, log streaming, and terminal sessions share the same SSH connection.

### Port Exposure

SSH port 22 is **not exposed** to the Docker host. It is only accessible within the `claworc` bridge network between sibling containers. No `-p 22:22` mapping is needed or created.

The only port exposed to the host is the control plane dashboard:

```yaml
ports:
  - "8000:8000"    # Dashboard UI and API
```

### Docker Socket Requirement

The control plane needs the Docker socket to:
- Create and manage agent containers
- Execute commands inside agents (`docker exec` for SSH key deployment)
- Inspect container network settings for SSH endpoint discovery

```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
```

## SSH Connection Flow in Docker

1. **Instance creation:** Control plane generates an ED25519 key pair, stores the private key at `/app/data/ssh-keys/{name}.key`, and saves the public key in SQLite.
2. **Container startup:** Agent container starts with s6-overlay, which launches sshd. Host keys are generated on first boot.
3. **Key deployment:** Control plane waits up to 120s for the container to be running, then uses `docker exec` to write the public key to `/root/.ssh/authorized_keys` (permissions 0600) inside the agent.
4. **SSH connection:** Control plane connects to the agent's container IP on port 22, authenticating with the private key.
5. **Tunnel establishment:** SSH tunnels forward VNC (port 3000) and Gateway (port 8080) to ephemeral local ports on the control plane.

## Agent SSH Server Configuration

The agent image includes a hardened sshd configuration at `/etc/ssh/sshd_config.d/claworc.conf`:

| Setting | Value | Purpose |
|---------|-------|---------|
| `Port` | `22` | Standard SSH port |
| `PubkeyAuthentication` | `yes` | Only authentication method |
| `PasswordAuthentication` | `no` | Passwords completely disabled |
| `PermitRootLogin` | `prohibit-password` | Root can log in via key only |
| `MaxAuthTries` | `3` | Limit brute-force attempts |
| `LoginGraceTime` | `30` | 30s to complete authentication |
| `MaxSessions` | `10` | Concurrent sessions per connection |
| `MaxStartups` | `10:30:60` | Rate-limit new connections |
| `AllowTcpForwarding` | `local` | Only local forwards allowed |
| `PermitOpen` | `localhost:3000 localhost:8080` | Restrict forwarding targets |

The agent runs with `--privileged` (required for systemd as PID 1). The sshd process is managed by s6-overlay and logs to `/var/log/sshd.log`.

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CLAWORC_DATABASE_PATH` | `/app/data/claworc.db` | SQLite database path (also determines SSH key location) |
| `CLAWORC_DOCKER_HOST` | *(auto-detect)* | Docker daemon URL override |
| `CLAWORC_AUTH_DISABLED` | `false` | Disable authentication (development only) |

SSH-specific parameters (keepalive, timeouts, retries) are compiled into the binary as constants. See the [SSH Configuration Reference](../configuration/ssh-configuration.md) for the full list.

## Monitoring SSH Health

### Dashboard API

```bash
# Check SSH status for an instance
curl -s http://localhost:8000/api/v1/instances/{id}/ssh-status

# Test SSH connectivity
curl -s http://localhost:8000/api/v1/instances/{id}/ssh-test

# View active tunnels
curl -s http://localhost:8000/api/v1/instances/{id}/tunnels

# View SSH audit logs
curl -s http://localhost:8000/api/v1/instances/{id}/ssh-events
```

### Container Logs

```bash
# Control plane SSH logs
docker logs claworc 2>&1 | grep '\[ssh'

# Agent sshd logs
docker exec bot-{name} cat /var/log/sshd.log

# Verify SSH port is listening in agent
docker exec bot-{name} ss -tlnp | grep ':22'
```

## Troubleshooting

### SSH Connection Fails

**Symptom:** Instance shows "Disconnected" or "Failed" SSH status.

**Check agent is running:**
```bash
docker ps --filter "name=bot-{name}"
```

**Check sshd is running inside agent:**
```bash
docker exec bot-{name} pgrep -a sshd
```

**Check authorized_keys was deployed:**
```bash
docker exec bot-{name} cat /root/.ssh/authorized_keys
```

**Check network connectivity:**
```bash
# Get agent IP
docker inspect bot-{name} --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'

# Test SSH port from control plane container
docker exec claworc sh -c 'echo | nc -w 3 <agent-ip> 22'
```

### Agent Not on Bridge Network

If the agent container is not on the `claworc` network:

```bash
# Check network
docker network inspect claworc

# Manually attach (should not be needed in normal operation)
docker network connect claworc bot-{name}
```

### Permission Errors on Bind Mounts

If using bind mounts instead of named volumes and seeing permission errors:

```bash
# Fix SSH key directory permissions
chmod 700 ./data/ssh-keys
chmod 600 ./data/ssh-keys/*.key
```

### Docker Desktop Considerations

- Docker Desktop runs containers in a Linux VM. The `claworc` bridge network is internal to this VM.
- SSH traffic between containers stays within the VM and never reaches the host network.
- Named volumes are stored in the VM's disk image. Use Docker Desktop's volume management or `docker cp` for backups.

## Backup and Recovery

### Backing Up SSH Keys and Database

```bash
# Using docker cp
docker cp claworc:/app/data ./backup-$(date +%Y%m%d)

# Using a named volume backup
docker run --rm \
  -v claworc-data:/data:ro \
  -v $(pwd)/backup:/backup \
  alpine tar czf /backup/claworc-data-$(date +%Y%m%d).tar.gz -C /data .
```

### Restoring from Backup

```bash
# Stop control plane
docker stop claworc

# Restore data
docker run --rm \
  -v claworc-data:/data \
  -v $(pwd)/backup:/backup \
  alpine sh -c 'rm -rf /data/* && tar xzf /backup/claworc-data-YYYYMMDD.tar.gz -C /data'

# Start control plane
docker start claworc
```

After restoration, the control plane will automatically reconnect SSH sessions using the restored keys.

## SSH Deployment Checklist

Use this checklist before going to production:

### Infrastructure
- [ ] Docker volume created for control plane data (`claworc-data`)
- [ ] Docker socket mounted into control plane container
- [ ] `claworc` bridge network created (automatic on first run)
- [ ] Backup strategy in place for `claworc-data` volume (covers both database and SSH keys)

### Network
- [ ] SSH port 22 is **not** exposed to the host (no `-p` mapping for SSH)
- [ ] Agent containers attached to `claworc` bridge network
- [ ] Control plane can reach agent containers by IP on the bridge network

### Security
- [ ] Agent containers use hardened sshd configuration (default from agent image)
- [ ] No password authentication on agent sshd
- [ ] SSH forwarding restricted to `localhost:3000` and `localhost:8080`
- [ ] Docker socket access is limited to trusted users
- [ ] If using bind mounts: directory permissions are 0700, key files are 0600

### Monitoring
- [ ] Control plane logs are collected (look for `[ssh]`, `[tunnel]` prefixes)
- [ ] SSH audit logging is enabled (default, 90-day retention)
- [ ] Health endpoint monitored (`http://localhost:8000/health`)
- [ ] Container restart policy is `unless-stopped` or `always`

### Operations
- [ ] Key rotation policy configured (default: 90 days, 0 to disable)
- [ ] Understand reconnection behavior (exponential backoff: 1s to 16s, 10 retries)
- [ ] Rate limiting parameters reviewed (10 attempts/min, 5-min block after 5 failures)
- [ ] Reviewed [SSH Operations Runbook](../operations/ssh-operations.md)
- [ ] Reviewed [SSH Troubleshooting Guide](../troubleshooting/ssh-troubleshooting.md)

## Related Documentation

- [SSH Connectivity Architecture](../architecture/ssh-connectivity.md)
- [SSH Configuration Reference](../configuration/ssh-configuration.md)
- [SSH Operations Runbook](../operations/ssh-operations.md)
- [SSH Troubleshooting Guide](../troubleshooting/ssh-troubleshooting.md)
- [Docker Backend Reference](../docker.md)
- [Installation Guide](../install.md)
