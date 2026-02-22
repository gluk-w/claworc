# Claworc — AI Agent Orchestrator for OpenClaw

[OpenClaw](https://openclaw.ai) is an open-source AI agent that runs locally, 
connects to any LLM, and autonomously executes tasks using an operating system's tools. 
Claworc makes it safe and simple to run multiple OpenClaw instances across your organization 
from a single web dashboard.

![Dashboard](docs/dashboard.png)

Each instance runs in an isolated container with its own browser, terminal, and persistent storage. 
Claworc proxies all traffic through a single entry point with built-in authentication, 
solving OpenClaw's biggest operational challenges: security, access control, and multi-instance management.

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
- **Use the terminal** — open interactive SSH terminal sessions with session persistence and scrollback
- **Manage files** — browse, upload, download, and edit files in each instance's workspace over SSH
- **View logs** — stream live logs to monitor what's happening inside an instance
- **Configure models and API keys** — set global defaults so you don't have to re-enter API keys for every instance, or
  override them per instance with different models and keys
- **Monitor SSH connections** — see real-time connection status, health metrics, tunnel health, and event history per instance

## Access Control

Claworc has a multi-user interface with two roles:

- **Admins** can create, configure, and manage all instances
- **Users** have access only to the instances assigned to them

Biometric identification is supported for authentication.

![Login screen](docs/login.png)

## Architecture

Claworc uses **SSH** as the secure connectivity layer between the control plane and all agent instances.
A single ED25519 key pair is auto-generated on first startup and used to authenticate with every instance.
The control plane establishes one multiplexed SSH connection per instance, then creates tunnels for
Chrome/VNC access (port 3000) and the OpenClaw gateway (port 18789). Terminal sessions, file operations,
and log streaming also flow over SSH.

```
Browser ──▶ Control Plane ──[SSH tunnel]──▶ Agent :3000 (VNC)
                           ──[SSH tunnel]──▶ Agent :18789 (Gateway)
                           ──[SSH exec]────▶ Agent (terminal, files, logs)
```

Agent instances are never exposed directly — all traffic is proxied through the control plane.
Three layers of health monitoring (SSH keepalive, command execution, tunnel probing) with automatic
reconnection ensure connections stay alive. For full details, see [SSH Connectivity Architecture](docs/ssh-connectivity.md).

## Security

- **SSH key-based authentication only** — password auth is disabled on agents; a single global ED25519 key pair authenticates with all instances
- **Key rotation** — keys can be rotated with zero downtime via a safe multi-step process across all instances
- **No direct agent access** — agent SSH ports are not exposed externally; only the control plane connects
- **Per-instance source IP restrictions** — optional whitelist of allowed source IPs/CIDRs for SSH connections
- **Connection rate limiting** — sliding window (10 attempts/min) plus escalating failure blocks prevent connection storms
- **Audit logging** — all SSH events (connections, file operations, terminal sessions, key rotations) are logged to SQLite with configurable retention
- **Encrypted API keys** — API keys are encrypted at rest in SQLite using Fernet symmetric encryption
- **Multi-user access control** — admins and users with role-based permissions and biometric authentication support

## Deployment

Claworc runs on **Docker** for local or single-server setups, or on **Kubernetes** for production-scale deployments.
The control plane is a single binary with 20Mb footprint that serves both the web dashboard and the proxy layer
for instance access. [Read more](docs/install.md)

## Documentation

- [Getting Started](docs/getting-started.md) - First-time setup and orientation
- [Installation](docs/install.md) - Runs on Docker or Kubernetes
- [Features](docs/features.md) - Feature specifications and user workflows
- [Architecture](docs/architecture.md) - System architecture and design decisions
- [SSH Connectivity](docs/ssh-connectivity.md) - SSH architecture, tunnels, health monitoring, and key rotation
- [API](docs/api.md) - REST API endpoints and request/response formats
- [Data Model](docs/data-model.md) - Database schema and Kubernetes resource model
- [UI](docs/ui.md) - Frontend pages, components, and interaction patterns

## Coming Soon

- API token usage monitoring
- Skills management

## Open Source

Claworc is fully open source, self-hosted, and free. Contributions are welcome!

# Star History

[![Star History Chart](https://api.star-history.com/svg?repos=gluk-w/claworc&type=date&legend=top-left)](https://www.star-history.com/#gluk-w/claworc&type=date&legend=top-left)
