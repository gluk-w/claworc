# Claworc - Claw Orchestrator

Claworc is a web-based dashboard for managing multiple clawdbot (moltbot) instances running in Kubernetes. 
It provides a unified interface to create, configure, monitor, start, stop, and delete bot instances, each 
running in its own isolated pod with a Google Chrome browser accessible via VNC.

## What is Clawdbot?

Clawdbot (also known as moltbot or Openclaw) is a WhatsApp gateway CLI built on Baileys web with a Pi RPC agent. Each instance runs inside a container that includes:

- Dual TigerVNC + noVNC sessions (Chrome kiosk + xterm terminal)
- No desktop environment — fully isolated displays
- Google Chrome with full profile persistence
- Node.js runtime with the clawdbot npm package
- Python toolchain (pip, pipx, poetry, uv)
- Homebrew (linuxbrew) package manager

## Why Claworc?

Companies need to manage bots at scale, so every employee and every team could have their own instances. 
Claworc replaces this manual approach by:

- Managing 5-20 concurrent bot instances from a single dashboard
- Dynamically provisioning Kubernetes resources (Deployments, PVCs, Secrets, ConfigMaps, Services)
- Providing per-instance configuration editing with a JSON editor
- Offering global API key management with per-instance overrides
- Exposing each instance via dual VNC sessions (Chrome + Terminal)

## Tech Stack

- **Backend**: Python, FastAPI, SQLite, Kubernetes Python client
- **Frontend**: React, TypeScript, Vite, TailwindCSS
- **Infrastructure**: MicroK8s on single server (192.168.1.104)
- **Container Image**: Existing moltbot Docker image (Ubuntu 24.04, systemd init)
- **Dependency Management**: Poetry (Python), npm (JavaScript)

## Target Environment

- MicroK8s cluster on a single server at `192.168.1.104`
- Internal network tool -- no authentication required
- Claworc itself deployed as a pod in the `claworc` namespace
- Bot instances deployed in the same namespace with RBAC-scoped access

## Quick Reference

| Document | Description |
|----------|-------------|
| [Features](features.md) | Feature specifications and user workflows |
| [Architecture](architecture.md) | System architecture and design decisions |
| [API](api.md) | REST API endpoints and request/response formats |
| [Data Model](data-model.md) | Database schema and Kubernetes resource model |
| [UI](ui.md) | Frontend pages, components, and interaction patterns |
