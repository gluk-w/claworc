# Installation Guide

Claworc can be installed in four ways:

1. [**Installer script**](#1-installer-script) — interactive setup for Docker or Kubernetes (recommended)
   - [Linux / macOS](#linux--macos)
   - [Windows](#windows)
2. [**Helm chart**](#2-manual-installation-with-helm) — manual deployment to a Kubernetes cluster
3. [**Docker Compose**](#3-manual-installation-with-docker-compose) — manual deployment on a single machine

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
| `config.databasePath` | SQLite database path inside the pod | `/app/data/claworc.db` |
| `service.nodePort` | NodePort for the dashboard itself | `30000` |
| `persistence.enabled` | Enable persistent storage for the database | `true` |
| `persistence.size` | PVC size | `1Gi` |

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

Create a data directory for the database and agent configs:

```bash
mkdir -p ~/.claworc/data/configs
```

Start the services:

```bash
CLAWORC_DATA_DIR=~/.claworc/data docker compose up -d
```

The dashboard is now available at **http://localhost:8000**.

### Configuration

The `docker-compose.yml` is configured through environment variables. You can set them inline, export them in your shell, or create a `.env` file in the repo root:

| Variable | Description | Default |
|----------|-------------|---------|
| `CLAWORC_DATA_DIR` | Host directory for database and configs (required) | — |

Example `.env` file:

```
CLAWORC_DATA_DIR=/home/user/.claworc/data
```

### Useful commands

```bash
docker compose logs -f        # View logs
docker compose down            # Stop
docker compose up -d           # Start again
docker compose down -v         # Stop and remove volumes
```

### Uninstall

```bash
docker compose down
# Remove agent containers (named bot-*)
docker ps -a --filter "name=bot-" --format '{{.Names}}' | xargs -r docker rm -f
# Remove data (optional)
rm -rf ~/.claworc/data
```

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
docker logs -f claworc-dashboard
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
docker ps --filter "name=claworc-dashboard"
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
docker inspect claworc-dashboard --format '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{println}}{{end}}'
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
docker rm -f claworc-dashboard
rm -f ~/.claworc/data/claworc.db
# Re-run install.sh or docker compose up
```

**Kubernetes:**

```bash
kubectl delete pvc claworc-data -n claworc
kubectl rollout restart deploy/claworc -n claworc
```
