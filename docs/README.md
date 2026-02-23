# Claworc - OpenClaw Orchestrator

Claworc is a web-based dashboard for managing multiple OpenClaw instances running in Kubernetes or Docker. 
It provides a unified interface to create, configure, monitor, start, stop, and delete bot instances, each 
running in its own isolated pod with a Google Chrome browser accessible via VNC.

## Why Claworc?

Companies need to manage AI agents at scale, so every employee and every team could have their own instances. 
Claworc replaces this manual approach by:

- Managing OpenClaw instances from a single dashboard
- Dynamically provisioning Kubernetes resources (Deployments, PVCs, Secrets, ConfigMaps, Services)
- Providing per-instance configuration editing with a JSON editor
- Offering global API key management with per-instance overrides
- Exposing each instance via dual VNC sessions (Chrome + Terminal)

## Quick Reference

| Document | Description |
|----------|-------------|
| [Features](features.md) | Feature specifications and user workflows |
| [Architecture](architecture.md) | System architecture and design decisions |
| [API](api.md) | REST API endpoints and request/response formats |
| [Data Model](data-model.md) | Database schema and Kubernetes resource model |
| [Authentication](auth.md) | Authentication, authorization, and user management |
| [UI](ui.md) | Frontend pages, components, and interaction patterns |
| [SSH Connectivity](ssh-connectivity.md) | SSH architecture, tunnels, health monitoring, and key rotation |
| [Kubernetes Deployment](deployment/kubernetes.md) | Kubernetes deployment guide with SSH network policies and security contexts |
| [Docker Deployment](deployment/docker.md) | Docker deployment guide with SSH network configuration |
