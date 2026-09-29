# Installation Guide

Claworc can be installed in three ways:

1. [**Installer script**](#1-installer-script) — interactive setup for Docker or Kubernetes (recommended)
   - [Linux / macOS](#linux--macos)
   - [Windows](#windows)
2. [**Helm chart**](#2-manual-installation-with-helm) — manual deployment to a Kubernetes cluster
3. [**Docker Compose**](#3-manual-installation-with-docker-compose) — manual deployment on a single machine

After installing, see [Networking & Security](#networking--security) for ports, NetworkPolicies, and pod security requirements.

---

## 1. Installer Script

The installer script auto-detects your environment and walks you through configuration.

### Linux / macOS

```bash
curl -fsSL https://raw.githubusercontent.com/gluk-w/claworc/main/install.sh | bash
```

Or clone the repo first:

```bash
git clone https://github.com/gluk-w/claworc.git
cd claworc
bash install.sh
```

The script will ask you to choose between **Docker** and **Kubernetes** deployment, then prompt for the relevant settings (ports, data directory, node IP, etc.). It handles image pulling, container creation, and Helm installation automatically.

To upgrade an existing installation, run `install.sh` again — it detects the current deployment and offers to upgrade in place.

To uninstall:

```bash
bash uninstall.sh
```

### Windows

For Windows users, use the PowerShell installer script. Open **PowerShell** (not Command Prompt) and run:

```powershell
# Clone the repository
git clone https://github.com/gluk-w/claworc.git
cd claworc

# Run the installer
.\install.ps1
```

The PowerShell script provides the same interactive setup as the bash version, with support for both Docker and Kubernetes deployment modes.

**Note:** If you encounter an execution policy error, you may need to allow the script to run:

```powershell
Set-ExecutionPolicy -ExecutionPolicy RemoteSigned -Scope CurrentUser
```

---

## 2. Manual Installation with Helm

### Prerequisites

- A running Kubernetes cluster
- [kubectl](https://kubernetes.io/docs/tasks/tools/) configured with access to the cluster
- [Helm](https://helm.sh/docs/intro/install/) v3+

### Steps

Clone the repository (the Helm chart is in the `helm/` directory):

```bash
git clone https://github.com/gluk-w/claworc.git
cd claworc
```

Install the chart:

```bash
helm install claworc helm/ \
  --namespace claworc \
  --create-namespace
```

If your kubeconfig is not at the default path, add `--kubeconfig /path/to/kubeconfig`.

### Verify

```bash
kubectl get pods -n claworc
kubectl logs -f deploy/claworc -n claworc
```

The dashboard is exposed as a NodePort service on port **30000** by default.

### Configuration

You can override any value in `helm/values.yaml` with `--set` flags or a custom values file (`-f custom-values.yaml`). Key settings:

| Value | Description | Default |
|-------|-------------|---------|
| `config.dataPath` | Data directory inside the pod (SQLite DB, SSH keys) | `/app/data` |
| `config.k8sNamespace` | Namespace agent pods are created in | `claworc` |
| `database.url` / `database.existingSecret` | External PostgreSQL/MariaDB instead of SQLite (see [databases.md](databases.md)) | — |
| `service.nodePort` | NodePort for the dashboard itself | `30000` |
| `sshGateway.enabled` | Inbound SSH gateway (see [ssh-gateway.md](ssh-gateway.md)) | `true` |
| `persistence.enabled` | Enable persistent storage for the data directory | `true` |
| `persistence.size` | PVC size | `1Gi` |
| `instanceIngress.enabled` | Allow ingress to agent pods from an Ingress controller namespace | `false` |
| `extraEnv` | Extra `CLAWORC_*` env vars for the control plane | `{}` |

### Upgrade

```bash
helm upgrade claworc helm/ \
  --namespace claworc
```

### Uninstall

```bash
helm uninstall claworc -n claworc
kubectl delete namespace claworc
```

---

## 3. Manual Installation with Docker Compose

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) and Docker Compose installed and running

### Steps

Clone the repository:

```bash
git clone https://github.com/gluk-w/claworc.git
cd claworc
```

Start the services:

```bash
docker compose up -d
```

The dashboard is now available at **http://localhost:8000**, and the inbound SSH
gateway on port **2222**.

### Configuration

`docker-compose.yml` is an example: it mounts the Docker socket, stores all state in
the named volume `claworc-data` (mounted at `/app/data`), and attaches the control
plane to a Docker network named `claworc`. Add further `CLAWORC_*` variables under
`environment:` — see the Configuration section of the root `CLAUDE.md` or
`control-plane/internal/config/config.go` for the full list.

### Useful commands

```bash
docker compose logs -f        # View logs
docker compose down            # Stop
docker compose up -d           # Start again
docker compose down -v         # Stop and remove volumes (deletes DB and SSH keys!)
```

### Uninstall

```bash
docker compose down
# Remove agent containers (named bot-*)
docker ps -a --filter "name=bot-" --format '{{.Names}}' | xargs -r docker rm -f
# Remove data (optional; deletes the claworc-data volume)
docker compose down -v
```

---

## Networking & Security

### Data directory

All control-plane state lives in one directory (`CLAWORC_DATA_PATH`, default `/app/data`):

```
/app/data/
├── claworc.db       # SQLite database (unless database.url points elsewhere)
├── ssh_key          # control plane's ED25519 private key (0600)
├── ssh_key.pub      # its public key
└── backups/         # instance backups, unless CLAWORC_BACKUPS_PATH is set
```

The key pair is generated on first start. Losing the volume (`docker compose down -v`,
deleting the PVC, or `persistence.enabled: false` on Helm, which uses an `emptyDir`)
loses the database and keys; new keys are generated on the next start and re-installed
into agents automatically.

### How the control plane reaches agents

Every agent image (OpenClaw, Hermes, NanoClaw, custom) runs `sshd` on port 22. The control
plane writes its public key into the agent's `/root/.ssh/authorized_keys` via
`docker exec` / `kubectl exec` before connecting, then does everything else over SSH:
shim verbs, terminal, file browser, logs, and tunnels (including the reverse tunnel
that exposes the internal proxy on `127.0.0.1:40001` inside the agent). On-demand
browser pods also expose only sshd.

| From | To | Port | Purpose |
|---|---|---|---|
| Control plane | Agent and browser pods/containers | 22/TCP | SSH, exec, tunnels |
| Control plane | Kubernetes API server | 443/TCP | Orchestration (Kubernetes only) |
| Users | Control plane | 8000 (Docker) / 30000 NodePort (Helm) | Dashboard |
| Users | Control plane | 2222 | Inbound SSH gateway (optional) |

Agent sshd is hardened: key-only auth, `PermitRootLogin prohibit-password`,
`MaxAuthTries 3`, no X11 or agent forwarding, and `PermitListen` limited to the ports
Claworc uses (see `agent/<agent>/rootfs/etc/ssh/sshd_config.d/claworc.conf`).

**Docker:** the control plane needs the Docker socket to create sibling containers and
exec into them. Agents join the `claworc` Docker network, so no agent ports are
published on the host.

**Kubernetes:** the chart ships NetworkPolicies that only admit the control-plane pod on
port 22: `bot-instance-isolation` for agent pods (`managed-by: claworc`) and
`bot-browser-isolation` for browser pods (`claworc-role: browser`). If you enforce
egress policies, also allow the control plane egress to those pods on 22, to the API
server on 443, and DNS. RBAC (`helm/templates/rbac.yaml`) is namespace-scoped and
includes `pods/exec`, which key installation requires.

### Pod security

The agent container runs unprivileged (`privileged: false`,
`allowPrivilegeEscalation: false`, `fsGroup: 1000`). Agent pods do include a
**privileged init container** (`fix-home-selinux`) that relabels the home volume on
SELinux nodes, so the agent namespace must allow the `privileged` Pod Security
Standard. The control-plane pod
itself needs no privileges.

### Monitoring

- `GET /health` reports the orchestrator backend (`docker` or `kubernetes`) and is used
  by the Helm liveness/readiness probes.
- Per-instance SSH connection state is shown in the UI; history is at
  `GET /api/v1/instances/{id}/ssh-events`, and the admin audit trail at
  `GET /api/v1/audit-logs`.

---

## Troubleshooting

### Windows: bash script fails with "invalid option" error

If you're on Windows and trying to run the bash script (`install.sh`) instead of the PowerShell script, you may encounter errors like:

```
: invalid option nameet: pipefail
```

This is caused by Windows line endings (`\r\n`) in the script file. **Solution: Use the PowerShell installer** (`install.ps1`) instead, which is designed for Windows.

If you must use bash (e.g., in WSL or Git Bash), convert the line endings first:

```bash
dos2unix install.sh
bash install.sh
```

### Viewing logs

**Docker (standalone container):**

```bash
docker logs -f claworc
```

**Docker Compose:**

```bash
docker compose logs -f
```

**Kubernetes:**

```bash
kubectl logs -f deploy/claworc -n claworc
```

To view logs for a specific agent instance:

```bash
# Docker
docker logs -f bot-<instance-name>

# Kubernetes
kubectl logs -f deploy/bot-<instance-name> -n claworc
```

### Health check

The dashboard exposes a `/health` endpoint. Use it to verify the service is running:

```bash
curl http://localhost:8000/health
```

On Kubernetes (from inside the cluster or via port-forward):

```bash
kubectl port-forward svc/claworc 8000:8001 -n claworc
curl http://localhost:8000/health
```

### Dashboard not reachable

**Docker:** Make sure the container is running and the port is correct:

```bash
docker ps --filter "name=claworc"
```

**Kubernetes:** Check that the pod is ready and the NodePort service exists:

```bash
kubectl get pods -n claworc
kubectl get svc -n claworc
```

### Agent containers not starting

Agents are created by the dashboard through the Docker socket or the Kubernetes API. Check the dashboard logs for errors first (see above).

**Docker:** The dashboard container needs access to the Docker socket. Verify the volume mount:

```bash
docker inspect claworc --format '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{println}}{{end}}'
```

You should see `/var/run/docker.sock -> /var/run/docker.sock`.

**Kubernetes:** The dashboard pod needs RBAC permissions to create resources in its namespace. Verify the service account and role binding exist:

```bash
kubectl get serviceaccount -n claworc
kubectl get rolebinding -n claworc
```

### Resetting the installation

To start fresh without uninstalling:

**Docker:**

```bash
# Installer script (host data dir, default ~/.claworc/data):
docker rm -f claworc
rm -f ~/.claworc/data/claworc.db
# Docker Compose (named volume):
docker compose down -v
# Then re-run install.sh or docker compose up -d
```

**Kubernetes:**

```bash
kubectl delete pvc claworc-data -n claworc
kubectl rollout restart deploy/claworc -n claworc
```

### SSH connection to an agent fails

The connection indicator on the agent stays red or the dashboard logs show SSH errors.

1. Check the agent is running: `docker ps --filter "name=bot-"` or
   `kubectl get pods -n claworc -l managed-by=claworc`.
2. Check the key was installed:
   `docker exec bot-<name> cat /root/.ssh/authorized_keys` or
   `kubectl exec -n claworc deploy/bot-<name> -- cat /root/.ssh/authorized_keys`.
3. Check reachability from the control plane:
   `kubectl exec -n claworc deploy/claworc -- nc -zv <agent-pod-ip> 22`. A timeout on
   Kubernetes usually means a NetworkPolicy is blocking port 22.
4. Check sshd's log inside the agent: `docker exec bot-<name> cat /var/log/claworc/sshd.log`
   (Kubernetes: `kubectl exec -n claworc deploy/bot-<name> -- cat /var/log/claworc/sshd.log`).
