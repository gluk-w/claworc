# Kubernetes Deployment Guide

This guide covers deploying Claworc to Kubernetes with SSH-based connectivity between the control plane and agent instances.

## Prerequisites

- Kubernetes cluster (v1.24+)
- [kubectl](https://kubernetes.io/docs/tasks/tools/) configured with cluster access
- [Helm](https://helm.sh/docs/intro/install/) v3+
- A StorageClass that supports `ReadWriteOnce` PVCs

## Architecture Overview

```
                     ┌──────────────────────────────────────────┐
                     │              claworc namespace            │
                     │                                          │
                     │  ┌──────────────┐    SSH (port 22)       │
                     │  │ Control Plane ├──────────────────┐     │
                     │  │   (claworc)   │                  │     │
                     │  └──────┬───────┘                  │     │
                     │         │                          │     │
                     │    ┌────┴─────┐            ┌───────┴───┐ │
                     │    │ claworc- │            │  bot-foo   │ │
                     │    │ data PVC │            │  (agent)   │ │
                     │    │          │            │            │ │
                     │    │ /app/data│            │  sshd :22  │ │
                     │    │ ├─ claworc.db         │  VNC :3000 │ │
                     │    │ └─ ssh-keys/          │  GW  :8080 │ │
                     │    └──────────┘            └────────────┘ │
                     └──────────────────────────────────────────┘
```

The control plane is the SSH **client**. Each agent instance runs an SSH **server** (sshd). All communication between the control plane and agents — tunnels, file operations, log streaming, terminal sessions — travels over SSH within the cluster network.

## Installation

### Quick Install

```bash
git clone https://github.com/gluk-w/claworc.git
cd claworc
helm install claworc helm/ \
  --namespace claworc \
  --create-namespace
```

### Custom Values

Create a `custom-values.yaml` to override defaults:

```yaml
config:
  databasePath: /app/data/claworc.db
  k8sNamespace: claworc

persistence:
  enabled: true
  size: 5Gi                    # Increase for many instances
  storageClass: "gp3"          # Use your cluster's StorageClass
  accessMode: ReadWriteOnce

resources:
  requests:
    cpu: 200m
    memory: 256Mi
  limits:
    cpu: "1"
    memory: 1Gi
```

```bash
helm install claworc helm/ \
  --namespace claworc \
  --create-namespace \
  -f custom-values.yaml
```

## PVC Requirements for SSH Key Storage

### Control Plane Data PVC

The Helm chart creates a single PVC (`claworc-data`) that stores both the SQLite database and SSH private keys:

| Path | Contents | Permissions |
|------|----------|-------------|
| `/app/data/claworc.db` | SQLite database (instance metadata, SSH public keys, fingerprints, audit logs) | 0644 |
| `/app/data/ssh-keys/` | ED25519 private key files, one per instance | Directory: 0700 |
| `/app/data/ssh-keys/{name}.key` | Instance private key (PEM-encoded) | 0600 |

**Sizing recommendations:**

| Instances | Recommended PVC Size | Rationale |
|-----------|---------------------|-----------|
| 1-10 | 1Gi (default) | Keys are ~400 bytes each; database is small |
| 10-50 | 2Gi | Allow headroom for audit logs in SQLite |
| 50+ | 5Gi+ | Audit log retention (default 90 days) can grow |

**Key requirements:**
- **Access mode:** `ReadWriteOnce` — only one control plane pod accesses the volume
- **Reclaim policy:** `Retain` recommended in production to prevent accidental key loss
- **Backup:** Both the database and `ssh-keys/` directory must be backed up together. The database references key paths, and keys without matching DB records are orphaned.

### Agent Instance PVCs

Each agent instance creates 3 PVCs for application data:

| PVC Name | Mount Path | Purpose |
|----------|-----------|---------|
| `{name}-homebrew` | `/home/linuxbrew/.linuxbrew` | Homebrew package cache |
| `{name}-openclaw` | `/config/.openclaw` | OpenClaw data |
| `{name}-chrome` | `/config/chrome-data` | Chrome browser profile |

These PVCs do **not** store SSH keys. The agent's SSH authorized keys are written to the container filesystem (`/root/.ssh/authorized_keys`) at startup via `kubectl exec`.

## Network Policies for SSH Traffic

### Default Network Policy

The Helm chart includes a `NetworkPolicy` that restricts ingress to bot instances:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: bot-instance-isolation
  namespace: claworc
spec:
  podSelector:
    matchLabels:
      managed-by: claworc
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: claworc
      ports:
        - port: 6081     # Chrome VNC (legacy, kept for compatibility)
        - port: 18789    # Terminal (legacy, kept for compatibility)
```

### Adding SSH Port to Network Policy

The default policy does not explicitly list port 22 because SSH traffic stays within the cluster network. However, for defense-in-depth, you should add SSH port 22 to the allowed ports:

```yaml
# custom-networkpolicy.yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: bot-instance-ssh
  namespace: claworc
spec:
  podSelector:
    matchLabels:
      managed-by: claworc
  policyTypes:
    - Ingress
  ingress:
    - from:
        - podSelector:
            matchLabels:
              app.kubernetes.io/name: claworc
      ports:
        - port: 22
          protocol: TCP
        - port: 3000
          protocol: TCP
        - port: 6081
          protocol: TCP
        - port: 8080
          protocol: TCP
        - port: 18789
          protocol: TCP
```

Apply alongside the Helm release:

```bash
kubectl apply -f custom-networkpolicy.yaml -n claworc
```

### Blocking External SSH Access

To ensure agent SSH servers are never reachable from outside the cluster:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-external-ssh
  namespace: claworc
spec:
  podSelector:
    matchLabels:
      managed-by: claworc
  policyTypes:
    - Ingress
  ingress:
    # Only allow traffic from within the namespace
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: claworc
```

## Security Contexts for SSH

### Agent Containers

Agent containers run with `privileged: true` because they use systemd as PID 1 and require cgroup access:

```yaml
securityContext:
  privileged: true
```

The agent's sshd process runs within this privileged container with the following hardened configuration:

| Setting | Value | Purpose |
|---------|-------|---------|
| `PubkeyAuthentication` | `yes` | Only authentication method |
| `PasswordAuthentication` | `no` | Passwords completely disabled |
| `PermitRootLogin` | `prohibit-password` | Root can log in via key only |
| `MaxAuthTries` | `3` | Limit brute-force attempts |
| `LoginGraceTime` | `30` | 30s to complete authentication |
| `MaxSessions` | `10` | Concurrent sessions per connection |
| `MaxStartups` | `10:30:60` | Rate-limit new connections |
| `AllowTcpForwarding` | `local` | Only local forwards allowed |
| `PermitOpen` | `localhost:3000 localhost:8080` | Restrict forwarding targets |
| `X11Forwarding` | `no` | Disabled |
| `AllowAgentForwarding` | `no` | Disabled |

### Agent SSH Host Keys

On first container start, the agent generates ED25519 host keys:

1. `ssh-keygen -A` generates all key types
2. DSA and ECDSA keys are removed, keeping only ED25519 and RSA
3. Host key fingerprints are verified via Trust On First Use (TOFU) and stored in the database

### Control Plane Security

The control plane pod does not run sshd. It is the SSH client and stores private keys with strict filesystem permissions:

- SSH key directory: `0700`
- Private key files: `0600`
- Keys are never exposed via the API (tagged `json:"-"` in the data model)

## SSH Connection Flow in Kubernetes

1. **Instance creation:** Control plane generates an ED25519 key pair, stores the private key at `/app/data/ssh-keys/{name}.key`, and saves the public key in SQLite.
2. **Key deployment:** Control plane waits up to 120s for the agent pod to be running, then uses `kubectl exec` to write the public key to `/root/.ssh/authorized_keys` (permissions 0600).
3. **SSH connection:** Control plane connects to the agent via the ClusterIP service on port 22, authenticating with the private key.
4. **Tunnel establishment:** SSH tunnels are created for VNC (agent port 3000) and Gateway (agent port 8080), binding to ephemeral local ports on `127.0.0.1`.
5. **Multiplexed operations:** A single SSH connection per instance carries all tunnels, file operations, log streaming, and terminal sessions.

### Service Discovery

The control plane resolves agent SSH endpoints using the Kubernetes service:

- **Service name:** `{name}-vnc` (ClusterIP type)
- **SSH port:** 22 (mapped in the service spec)
- **DNS:** `{name}-vnc.claworc.svc.cluster.local:22`

## RBAC Requirements

The Helm chart creates a Role with these permissions (required for SSH key deployment via `kubectl exec`):

| API Group | Resource | Verbs | SSH Relevance |
|-----------|----------|-------|---------------|
| `""` | `pods` | `get`, `list`, `watch` | Find agent pods for SSH connections |
| `""` | `pods/exec` | `create` | Deploy SSH authorized_keys to agents |
| `apps` | `deployments` | `create`, `get`, `list`, `patch`, `delete` | Manage agent deployments |
| `""` | `services` | `create`, `get`, `list`, `delete` | Create ClusterIP services with SSH port |
| `""` | `persistentvolumeclaims` | `create`, `get`, `list`, `delete` | Manage agent data PVCs |
| `""` | `nodes` | — | Not required (ClusterIP service, no NodePort SSH) |

## Monitoring SSH Health

### Health Endpoint

The control plane `/health` endpoint includes SSH connection status:

```bash
kubectl port-forward svc/claworc 8000:8001 -n claworc
curl -s http://localhost:8000/health | jq
```

### SSH-Specific Endpoints

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

### Kubernetes-Native Monitoring

```bash
# Check control plane logs for SSH events
kubectl logs deploy/claworc -n claworc | grep '\[ssh'

# Check agent sshd logs
kubectl exec -n claworc deploy/bot-{name} -- cat /var/log/sshd.log

# Verify SSH port is open on agent
kubectl exec -n claworc deploy/bot-{name} -- ss -tlnp | grep ':22'
```

## Production Hardening

### Dedicated StorageClass

Use a StorageClass with encryption at rest for the control plane PVC:

```yaml
persistence:
  storageClass: "encrypted-gp3"
  size: 5Gi
```

### PVC Backup

Back up the control plane PVC regularly. Both the database and SSH keys are critical:

```bash
# Example: snapshot-based backup
kubectl get pvc claworc-data -n claworc -o jsonpath='{.spec.volumeName}'
# Create a VolumeSnapshot using your CSI driver
```

### Resource Limits

The control plane manages one SSH connection per instance. For clusters with many instances, increase resource limits:

```yaml
resources:
  requests:
    cpu: 500m
    memory: 512Mi
  limits:
    cpu: "2"
    memory: 2Gi
```

### Pod Disruption Budget

Prevent accidental eviction of the control plane (which would drop all SSH connections):

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: claworc
  namespace: claworc
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app.kubernetes.io/name: claworc
```

## SSH Deployment Checklist

Use this checklist before going to production:

### Infrastructure
- [ ] StorageClass supports `ReadWriteOnce` PVCs
- [ ] Control plane PVC sized appropriately for instance count
- [ ] PVC reclaim policy set to `Retain`
- [ ] PVC backup strategy in place (covers both database and SSH keys)

### Network
- [ ] NetworkPolicy restricts SSH (port 22) to control plane pods only
- [ ] Agent SSH port (22) is not exposed via NodePort or LoadBalancer
- [ ] Cluster DNS resolves service names correctly (`{name}-vnc.claworc.svc`)

### Security
- [ ] RBAC role includes `pods/exec` permission (for SSH key deployment)
- [ ] Agent containers use hardened sshd configuration (default from agent image)
- [ ] No password authentication on agent sshd
- [ ] SSH forwarding restricted to `localhost:3000` and `localhost:8080`
- [ ] Control plane PVC permissions enforced (0700 for ssh-keys directory)

### Monitoring
- [ ] Control plane logs are collected (look for `[ssh]`, `[tunnel]` prefixes)
- [ ] SSH audit logging is enabled (default, 90-day retention)
- [ ] Health endpoint monitored (`/health`)
- [ ] Alerting on SSH connection failures

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
- [Installation Guide](../install.md)
