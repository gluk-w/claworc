# Claworc — AI Agent Orchestrator for OpenClaw

[OpenClaw](https://openclaw.ai) is an open-source AI agent that runs locally, 
connects to any LLM, and autonomously executes tasks using an operating system's tools. 
Claworc makes it safe and simple to run multiple OpenClaw instances across your organization 
from a single web dashboard.

![Dashboard](docs/dashboard.png)

Each instance runs in an isolated container with its own browser, terminal, and persistent storage.
All communication between the control plane and agents uses **SSH tunnels** — no agent ports are
exposed directly. Claworc provides a single entry point with built-in authentication, solving
OpenClaw's biggest operational challenges: security, access control, and multi-instance management.

**Use case:** Give every team member their own AI agent, stand up a shared agent for data analysis, or run 
an internal IT support bot — then manage them all from one place.

## What Is an Instance?

An instance is a self-contained AI workspace. When you create one, Claworc spins up an isolated container that includes:

- **An AI agent** powered by the LLM of your choice — Claude, GPT, DeepSeek, or any supported model
- **A full Chrome browser** that the agent operates and you can watch or control live through your own browser
- **A terminal** for command-line operations
- **Persistent storage** for files, browser profiles, and installed packages — survives restarts and redeployments

Instances are fully isolated from each other, each with its own file system. They are monitored by systemd 
and automatically restarted if they crash.

## What You Can Do

- **Create and manage instances** — spin up new agent workspaces, start/stop them, or remove them when done
- **Chat with agents** — send instructions and have a conversation with the AI agent in each instance
- **Watch the browser** — see what the agent is doing in Chrome in real time, or take control yourself
- **Manage files** — browse and manage the files in each instance's workspace
- **View logs** — stream live logs to monitor what's happening inside an instance
- **Monitor SSH connections** — real-time dashboard showing connection health, tunnel status, and reconnection events for every instance
- **Configure models and API keys** — set global defaults so you don't have to re-enter API keys for every instance, or
  override them per instance with different models and keys

## Access Control

Claworc has a multi-user interface with two roles:

- **Admins** can create, configure, and manage all instances
- **Users** have access only to the instances assigned to them

Biometric identification is supported for authentication.

![Login screen](docs/login.png)

## Architecture

The control plane communicates with every agent instance exclusively through **SSH**. Each agent runs an
sshd server; the control plane connects as an SSH client. Desktop (VNC) and chat (Gateway) traffic flow
through SSH port-forwarding tunnels, while file operations, log streaming, and terminal access use direct
SSH exec/PTY sessions.

```
Browser ──HTTP/WS──► Control Plane ──SSH──► Agent Pod (sshd :22)
                          │                     ├─ Desktop (tunnel → :3000)
                     SSHManager                 ├─ Gateway  (tunnel → :8080)
                     TunnelManager              ├─ Files, Logs (exec)
                          │                     └─ Terminal (PTY)
                     localhost:N ◄──tunnel──►
```

No agent ports are exposed externally. The control plane binds ephemeral localhost ports via SSH tunnels
and proxies browser traffic through them.

## SSH Key Management

- **ED25519 key-per-instance** — each instance gets a unique key pair at creation time
- **Private keys stored on disk** (`/app/data/ssh-keys/`) with `0600` permissions, never exposed via the API
- **Automatic key rotation** — configurable per-instance policy (default 90 days) with zero-downtime rotation
- **Audit logging** — all SSH events recorded with 90-day retention

For full details see the [SSH Connectivity Architecture](docs/architecture/ssh-connectivity.md).

## Deployment

Claworc runs on **Docker** for local or single-server setups, or on **Kubernetes** for production-scale deployments.
The control plane is a single binary with 20Mb footprint that serves both the web dashboard and the SSH connectivity
layer for instance access. [Read more](docs/install.md)

## Documentation

- [Installation](docs/install.md) - Runs on Docker or Kubernetes
- [Getting Started](docs/getting-started.md) - Creating instances, SSH connections, and first steps
- [Features](docs/features.md) - Feature specifications and user workflows
- [Architecture](docs/architecture.md) - System architecture and design decisions
- [SSH Connectivity](docs/architecture/ssh-connectivity.md) - SSH tunnel architecture, key management, and security model
- [API](docs/api.md) - REST API endpoints and request/response formats
- [Data Model](docs/data-model.md) - Database schema and Kubernetes resource model
- [UI](docs/ui.md) - Frontend pages, components, and interaction patterns
- [SSH Operations](docs/operations/ssh-operations.md) - Monitoring, troubleshooting, and key rotation runbook
- [SSH Troubleshooting](docs/troubleshooting/ssh-troubleshooting.md) - Diagnostic procedures and FAQ

## Coming Soon

- API token usage monitoring
- Skills management

## Open Source

Claworc is fully open source, self-hosted, and free. Contributions are welcome!

# Star History

[![Star History Chart](https://api.star-history.com/svg?repos=gluk-w/claworc&type=date&legend=top-left)](https://www.star-history.com/#gluk-w/claworc&type=date&legend=top-left)
