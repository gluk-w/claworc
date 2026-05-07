# Claworc - secure and easy-to-use orchestrator for OpenClaw

[![Control Plane](https://github.com/gluk-w/claworc/actions/workflows/control-plane.yml/badge.svg?branch=main)](https://github.com/gluk-w/claworc/actions/workflows/control-plane.yml)                                                                                                                                                           
[![Agent](https://github.com/gluk-w/claworc/actions/workflows/agent.yml/badge.svg?branch=main)](https://github.com/gluk-w/claworc/actions/workflows/agent.yml)               
[![CodeQL](https://github.com/gluk-w/claworc/actions/workflows/codeql.yml/badge.svg?branch=main)](https://github.com/gluk-w/claworc/actions/workflows/codeql.yml)     

Claworc makes it safe and simple to run multiple [OpenClaw](https://openclaw.ai) instances across your organization
from a single web dashboard.

![Dashboard](docs/dashboard.png)

Each instance runs in an isolated container with its own browser, terminal, and persistent storage. 
Claworc proxies all traffic through a single entry point with hardened authentication, 
solving OpenClaw's biggest operational challenges: security, stability, access control, and multi-instance management.

**Use cases:** Give every team their own AI agent; spin up a shared agent for data analysis; or manage AI bots
for your clients from one place.

## What You Can Do

- [**Create and manage instances**](https://claworc.com/docs/instances) — spin up new isolated OpenClaw agents, start/stop them, or remove when done
- [**LLM Usage**](https://claworc.com/docs/models/overview) — manage LLM tokens from one place so you don't have to re-enter 
  API keys for every instance, view usage statistics by provider or instance. 
  Your real API keys never leave the secured storage - OpenClaw gets virtual keys.
- [**Automated backups**](https://claworc.com/docs/backups) give you a piece of mind when changing something.
- [**Skills Library**](https://claworc.com/docs/skills) stores your proprietary skills that can be easily deployed to any agent.
- [**Shared folders**](https://claworc.com/docs/shared-folders) allow your OpenClaw instances collaborate and reuse data.
- **Chat with agents** — send instructions and have a conversation with the AI agent in each instance
- **Watch the browser** — see what the agent is doing in Chrome in real time, or take control yourself
- **Use the terminal** — open interactive SSH terminal sessions with session persistence and scrollback
- **Manage files** — browse, upload, download, and edit files in each instance's workspace over SSH
- **View logs** — stream live logs to monitor what's happening inside an instance

## Security

![Login screen](docs/login.png)

OpenClaw instances are never directly exposed - all traffic is routed through the control plane. Each instance 
runs in a secured container, minimizing the blast radius to that container only. You can enable automatic backups 
for easy rollbacks.

Claworc has a multi-user interface with two roles:

- **Admins** can create, configure, and manage teams and instances
- **Managers** can control instances within a team
- **Users** have access only to the instances assigned to them

Biometric identification is supported for authentication.

## Deployment

Claworc runs on **Docker** for local or single-server setups, or on **Kubernetes** for production-scale deployments.
The control plane is a single binary with 20Mb footprint that serves both the web dashboard and the proxy layer
for instance access. [Read more](https://claworc.com/docs/installation)

## Links

- [Visit the website](https://claworc.com/)
- [Full documentation](https://claworc.com/docs)
- [Discord](https://discord.gg/eCgmvxR7vN)
- [Twitter / X](https://x.com/claworc)