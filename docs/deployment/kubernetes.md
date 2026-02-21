# Kubernetes Deployment Guide

## Prerequisites

- A running Kubernetes cluster (v1.24+)
- [kubectl](https://kubernetes.io/docs/tasks/tools/) configured with cluster access
- [Helm](https://helm.sh/docs/intro/install/) v3+
- A StorageClass that supports `ReadWriteOnce` PVCs

## Installation

See the [Installation Guide](../install.md) for step-by-step instructions using the installer script or Helm.

## Data Persistence

The control plane stores all persistent state in a single data directory (`CLAWORC_DATA_PATH`, default `/app/data`):

```
/app/data/
├── claworc.db          # SQLite database (instances, settings, audit logs)
├── ssh_key             # ED25519 private key (mode 0600)
├── ssh_key.pub         # ED25519 public key (mode 0644)
└── configs/            # Per-instance config files (Docker backend only)
```

**SSH key files live alongside the SQLite database.** The PVC backing the data directory persists both the database and SSH keys — no separate volume mount is needed for SSH keys. On first startup, the control plane auto-generates the ED25519 key pair if the files don't exist.

The Helm chart creates a single PVC for this directory:

```yaml
# helm/values.yaml
persistence:
  enabled: true
  size: 1Gi
  storageClass: ""     # uses cluster default
  accessMode: ReadWriteOnce
```

> **Important:** If persistence is disabled (`persistence.enabled: false`), an `emptyDir` volume is used instead. This means SSH keys and the database are lost on pod restart — the control plane will generate new keys and start with an empty database.

## Network Policies for SSH Traffic

The control plane establishes SSH connections (TCP port 22) from its pod to each agent instance pod. Both the control plane and agent pods run in the same namespace (`claworc` by default).

If you use Kubernetes NetworkPolicies, you must allow:

### Egress from Control Plane

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: claworc-ssh-egress
  namespace: claworc
spec:
  podSelector:
    matchLabels:
      app.kubernetes.io/name: claworc
  policyTypes:
    - Egress
  egress:
    # Allow SSH to agent pods
    - to:
        - podSelector:
            matchLabels:
              managed-by: claworc
      ports:
        - protocol: TCP
          port: 22
    # Allow access to Kubernetes API server (for orchestrator operations)
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
      ports:
        - protocol: TCP
          port: 443
    # Allow DNS resolution
    - to: []
      ports:
        - protocol: UDP
          port: 53
        - protocol: TCP
          port: 53
```

### Ingress to Agent Pods

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: claworc-agent-ingress
  namespace: claworc
spec:
  podSelector:
    matchLabels:
      managed-by: claworc
  policyTypes:
    - Ingress
  ingress:
    # Allow SSH from control plane only
    - from:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: claworc
      ports:
        - protocol: TCP
          port: 22
```

> **Note:** The exact label selectors depend on your Helm chart configuration. Adjust `app.kubernetes.io/name` and `managed-by` labels to match your deployment.

### Required Ports Summary

| Direction | From | To | Port | Protocol | Purpose |
|-----------|------|-----|------|----------|---------|
| Egress | Control plane pod | Agent pods | 22 | TCP | SSH connections and tunnels |
| Egress | Control plane pod | K8s API server | 443 | TCP | Orchestrator operations (create/delete pods) |
| Ingress | Control plane pod | Agent pods | 22 | TCP | SSH server on agent |

## Security Contexts

### Control Plane

The control plane Helm deployment does not define explicit security contexts — it runs with the default pod security settings. The control plane needs:

- **Network access**: Outbound TCP to agent pods on port 22 and to the Kubernetes API server
- **File system access**: Read/write to the data volume for SQLite database and SSH key files
- **No privileged mode required**

### Agent Pods

Agent pods are created dynamically by the control plane's orchestrator and require a **privileged security context** because they run systemd as PID 1:

```yaml
securityContext:
  privileged: true
```

The agent's SSH server runs inside this privileged container:

- **OpenSSH server** (`sshd`) is managed by s6-overlay as a long-running service
- **Host keys**: Ed25519 and RSA host keys are generated on container startup (DSA and ECDSA keys are explicitly removed)
- **Authorized keys**: The control plane uploads its public key to `/root/.ssh/authorized_keys` before each connection via `kubectl exec`
- **Hardened configuration**:
  - `PasswordAuthentication no` — only key-based auth
  - `PermitRootLogin prohibit-password` — root can log in but only with key
  - `MaxAuthTries 3` — limits brute-force attempts
  - `LoginGraceTime 30` — 30-second window to authenticate
  - `X11Forwarding no` — X11 forwarding disabled
  - `AllowAgentForwarding no` — agent forwarding disabled
  - `AllowTcpForwarding yes` — required for SSH tunnels (VNC, gateway)

### Pod Security Standards

If your cluster enforces [Pod Security Standards](https://kubernetes.io/docs/concepts/security/pod-security-standards/):

- The **control plane** pod is compatible with the `restricted` profile (no privileged access needed)
- **Agent pods** require the `privileged` profile because they use `privileged: true` for systemd

If your namespace enforces `baseline` or `restricted` pod security, you'll need to either:
1. Deploy agent pods in a separate namespace with `privileged` enforcement, or
2. Create a `PodSecurityPolicy` (deprecated) or use admission controller exceptions for agent pods

## RBAC

The Helm chart creates a ServiceAccount, Role, and RoleBinding scoped to the `claworc` namespace. The Role grants the control plane permission to manage Deployments, Services, PVCs, ConfigMaps, Secrets, and Pods within the namespace. See the [Architecture docs](../architecture.md#rbac) for the full role definition.

## SSH-Specific Deployment Checklist (Kubernetes)

Use this checklist when deploying or upgrading Claworc on Kubernetes:

- [ ] **PVC is provisioned** — Verify the data PVC exists and is bound (`kubectl get pvc -n claworc`)
- [ ] **Data directory is writable** — The control plane must be able to write `ssh_key`, `ssh_key.pub`, and `claworc.db` to the data volume
- [ ] **SSH keys generated** — After first startup, verify keys exist in the control plane logs or check `/health`
- [ ] **Network policies allow SSH** — If using NetworkPolicies, ensure TCP port 22 egress from control plane to agent pods is allowed
- [ ] **Agent pods can start privileged** — Verify Pod Security Standards or admission controllers allow `privileged: true` for agent pods
- [ ] **RBAC is configured** — Control plane ServiceAccount can create/exec into pods in the target namespace
- [ ] **kubectl exec works** — The control plane uses `kubectl exec` to upload public keys to agents; verify RBAC grants `pods/exec` permission
- [ ] **Health endpoint responds** — `curl http://<control-plane>:8000/health` returns orchestrator backend and instance counts
- [ ] **SSH connections establish** — Create a test instance and verify the SSH connection indicator turns green in the UI
- [ ] **Tunnels are functional** — After SSH connects, verify Chrome (VNC) and terminal access work through the UI

## Monitoring

### Health Endpoint

The control plane exposes `GET /health` which includes:

- `orchestrator_backend`: `"kubernetes"` or `"docker"`
- Instance counts by status

### SSH Connection Health

Per-instance SSH connection health is visible via:

- **UI**: Connection status indicators on the instance list and detail pages
- **API**: `GET /api/v1/instances/{id}/ssh-events` — connection event history
- **API**: `GET /api/v1/audit-logs` — persistent audit trail

### Key Metrics to Monitor

| Metric | Source | What to Watch |
|--------|--------|---------------|
| Connection state | SSH events API | Instances stuck in `Reconnecting` or `Failed` |
| Health check failures | SSH events API | Repeated `health_check_failed` events |
| Tunnel recreation count | Tunnel metrics | High tunnel recreation rate indicates instability |
| Key rotation age | Settings API | `ssh_key_last_rotation` approaching policy threshold |

## Troubleshooting

### SSH connection fails after pod restart

The agent container generates new SSH host keys on every start. The control plane re-uploads its public key before each connection attempt, so agent restarts are handled automatically. If connections still fail:

1. Check control plane logs for SSH errors: `kubectl logs -f deploy/claworc -n claworc`
2. Verify the agent pod is running: `kubectl get pods -n claworc -l managed-by=claworc`
3. Test SSH key upload manually: `kubectl exec -n claworc deploy/bot-<name> -- cat /root/.ssh/authorized_keys`

### SSH connection blocked by NetworkPolicy

If the control plane logs show connection timeouts to agent pods:

1. Verify NetworkPolicies: `kubectl get networkpolicy -n claworc`
2. Test connectivity: `kubectl exec -n claworc deploy/claworc -- nc -zv <agent-pod-ip> 22`
3. Ensure egress policy allows TCP port 22 to agent pods (see [Network Policies](#network-policies-for-ssh-traffic) above)

### SSH keys lost after PVC deletion

If the data PVC is deleted, the control plane generates a new key pair on next startup. Existing agent instances will still have the old public key in their `authorized_keys`. The control plane handles this automatically — it re-uploads the new public key before each connection.

See also: [SSH Connectivity Architecture](../ssh-connectivity.md) for detailed troubleshooting of connection states and events.
