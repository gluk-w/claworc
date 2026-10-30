# Claworc — AI Agent Orchestrator

Claworc is a web dashboard for running a fleet of AI agents in Kubernetes or Docker.
Each agent instance runs in its own container/pod; users get a chat, an on-demand
Chromium browser (VNC), a terminal, a file browser, and logs for collaborating with it.

Supported agents:

| Agent | Image | Notes |
|---|---|---|
| OpenClaw | `claworc/openclaw` | Default. Also serves its own Control UI. |
| Hermes | `claworc/hermes` | NousResearch Hermes Agent. |
| NanoClaw | `claworc/nanoclaw` | NanoClaw on Bun + the Claude Agent SDK. |
| Custom | any image | Anything implementing the [agent shim contract](shim.md). |

The control plane is agent-agnostic: chat, webhooks, Kanban, config editing, LLM
routing, skills, and restart all go through the image's **shim** — a set of bash
verbs under `/opt/claworc/shim/` executed over SSH. See [shim.md](shim.md) and
[adding-an-agent.md](adding-an-agent.md).

> Terminology: the UI says **Agent**; code, API paths, and the database say **Instance**.

## Documents

### Getting started

| Document | Description |
|---|---|
| [Installation](install.md) | Installer script, Helm, Docker Compose; networking, security, and troubleshooting |
| [Development](development.md) | Local dev setup, Make targets, building images |
| [Style guide](style-guide.md) | UI conventions for the frontend |

### Agents

| Document | Description |
|---|---|
| [Agent shim contract](shim.md) | Verbs, exit codes, chat event schema, `meta` capabilities, env vars |
| [Adding an agent](adding-an-agent.md) | Building a new agent image and registering it |
| [Environment variables](environment-variables.md) | Reserved, global, and per-instance env vars; skill `required_env_vars` |
| [On-demand browser](ondemand-browser.md) | Browser pods spawned on first CDP use (design doc) |

### Features

| Document | Description |
|---|---|
| [Webhooks](webhooks.md) | Synchronous HTTP → agent chat bridge, public and private keys |
| [Kanban](kanban.md) | Global task board with moderator-routed dispatch to agents |
| [Connections](connections.md) | Composio OAuth connections brokered to agents |
| [Shared folders](shared-folders.md) | Volumes mapped into multiple instances |
| [Backups](backups.md) | Instance backup and restore |
| [File browser](file-browser.md) | In-dashboard file manager |
| [Task manager](task-manager.md) | Status tracking for long-running operations (create, restart, backup, …) |
| [Teams](teams.md) | Grouping instances and users |
| [SSH gateway](ssh-gateway.md) | `ssh <user>+<instance>@host` access |
| [Analytics](analytics.md) | Opt-in anonymous deployment telemetry |

### Control plane internals

| Document | Description |
|---|---|
| [Internal proxy](internal-proxy.md) | Loopback server for LLM, Connections, and webhook routes |
| [Virtual keys](virtual-keys.md) | LLM proxy virtual keys and how routing reaches the agent |
| [LLM catalog](llm-catalog.md) | Provider/model catalog caching |
| [Authentication](auth.md) | Auth, roles, and user management |
| [Databases](databases.md) | SQLite (default) and PostgreSQL backends |
| [Migrations](migrations.md) | When and how to write DB migrations |
| [Backend integration tests](integration-test-backend.md) | Running the integration suite |
