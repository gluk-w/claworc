# Installation Guide

Claworc can be installed in three ways:

1. **Installer script** — interactive setup for Docker or Kubernetes (recommended)
2. **Helm chart** — manual deployment to a Kubernetes cluster
3. **Docker Compose** — manual deployment on a single machine

---

## 1. Installer Script (Linux / macOS)

The installer script auto-detects your environment and walks you through configuration.

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
  --create-namespace \
  --set config.nodeIp="<YOUR_NODE_IP>" \
  --set config.portStart=30100 \
  --set config.portEnd=30199
```

Replace `<YOUR_NODE_IP>` with the IP address of a cluster node that agents will be reachable on.

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
| `config.nodeIp` | Node IP used in VNC URLs | `192.168.1.104` |
| `config.portStart` | Start of NodePort range for agents | `30100` |
| `config.portEnd` | End of NodePort range for agents | `30199` |
| `config.databasePath` | SQLite database path inside the pod | `/app/data/claworc.db` |
| `image.repository` | Dashboard image | `glukw/claworc-dashboard` |
| `image.tag` | Dashboard image tag | `latest` |
| `service.nodePort` | NodePort for the dashboard itself | `30000` |
| `persistence.enabled` | Enable persistent storage for the database | `true` |
| `persistence.size` | PVC size | `1Gi` |

### Upgrade

```bash
helm upgrade claworc helm/ \
  --namespace claworc \
  --set config.nodeIp="<YOUR_NODE_IP>"
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
| `CLAWORC_NODE_IP` | IP address used in VNC URLs | `127.0.0.1` |
| `CLAWORC_PORT_START` | Start of host port range for agents | `30100` |
| `CLAWORC_PORT_END` | End of host port range for agents | `30199` |

Example `.env` file:

```
CLAWORC_DATA_DIR=/home/user/.claworc/data
CLAWORC_NODE_IP=192.168.1.50
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
