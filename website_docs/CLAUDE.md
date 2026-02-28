# Claworc Documentation For End Users

## About this project

- This is a documentation site built on [Mintlify](https://mintlify.com)
- Pages are MDX files with YAML frontmatter
- Configuration lives in `docs.json`
- Run `mint dev` to preview locally
- Run `mint broken-links` to check links

## Terminology

| Preferred | Avoid | Notes |
|-----------|-------|-------|
| **instance** | "bot", "agent container", "pod" | The unit of deployment — one OpenClaw agent with its own browser and terminal |
| **dashboard** | "control plane UI", "admin panel" | The Claworc web interface |
| **control plane** | "server", "backend" | The Go backend + React frontend as a combined system |
| **OpenClaw** | "the agent software", "clawdbot" | The AI agent software that runs inside each instance |
| **Chrome session** | "VNC browser", "noVNC" | The VNC-based browser access in the dashboard |
| **Terminal session** | "VNC terminal", "shell" | The VNC-based terminal access in the dashboard |
| **SSH terminal** | "SSH session", "interactive terminal" | The lightweight SSH terminal (not VNC) on the instance detail page |
| **clawdbot.json** | "config file", "agent config" | The JSON configuration file for the OpenClaw agent |
| **API key** | "API token", "secret" | The key injected into `clawdbot.json` for the AI model provider |
| **admin** | "administrator", "superuser" | The elevated role with full access |
| **user** | "regular user", "member" | The limited role with access to assigned instances only |
| **assign** | "grant access", "share" | Giving a user access to specific instances |
| **SSH tunnel** | "connection", "proxy" | The SSH-based tunnel Claworc uses to proxy traffic to instances |
| **connection state** | "status", "health" | The SSH connection lifecycle state (Connected, Disconnected, etc.) |
| **rotate** (keys) | "regenerate", "refresh" | Replacing the global SSH key pair with a new one |
| **persistent volume** | "volume", "storage" | Kubernetes PVC or Docker named volume backing instance data |
| **orchestrator** | "backend", "provider" | The infrastructure layer — either Kubernetes or Docker |

## Style preferences

<!-- Add any project-specific style rules below -->

- Use active voice and second person ("you")
- Keep sentences concise — one idea per sentence
- Use sentence case for headings
- Bold for UI elements: Click **Settings**
- Code formatting for file names, commands, paths, and code references

## Content boundaries

<!-- Define what should and shouldn't be documented -->
<!-- Example: Don't document internal admin features -->
