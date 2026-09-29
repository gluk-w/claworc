# Agent Images

Container images for the agents Claworc manages. Each one runs its agent under
s6-overlay with an SSH server, and exposes it to the control plane through the agent
shim.

- `openclaw/`: `claworc/openclaw`, the default agent (OpenClaw gateway plus a VNC
  browser desktop)
- `hermes/`: `claworc/hermes`, NousResearch Hermes Agent (a Python CLI that runs once
  per chat turn)
- `nanoclaw/`: `claworc/nanoclaw`, NanoClaw on Bun and the Claude Agent SDK
- `template/`: copy-me starting point for a custom agent image
- `browser/`: standalone browser images (`claworc/<browser>-browser`)
- `tests/`: integration suites that run against the built images

## Agent shim

Every agent image exposes Claworc's control surface (chat, config editing, LLM
routing, skills, health, restart) as a set of **bash verbs** under
`/opt/claworc/shim/`. The control plane runs them over SSH. Each verb sources the
shared `lib/shimlib.sh`. Heavier logic lives in `lib/`, written in whatever runtime the
image already has (Node for OpenClaw, Bun for NanoClaw, Python for Hermes).

- **Contract and implementation guide:** [`docs/shim.md`](../docs/shim.md) covers the
  verbs, exit codes, the chat JSONL schema, the `shimlib.sh` helpers, and how the
  shipped images split bash and `lib/`.
- **Building a new agent image:** copy `template/`. It implements every verb, and its
  `lib/shimlib.sh` is the canonical copy that other images must ship byte-for-byte.
- **Conformance:** `shim-selftest` checks an image from the inside. `tests/`
  (`shim-contract.ts` plus one `<agent>.test.ts` per image) are the authoritative
  suites.

## OpenClaw image

Docker image that provides a ready-to-use OpenClaw environment with a browser accessible via VNC.

### What's Inside

- **Debian Bookworm** minimal with s6-overlay v3 as PID 1
- **Chromium** with DevTools Protocol enabled for OpenClaw browser automation
- **OpenClaw** gateway running as an s6-overlay service
- **VNC access** via TigerVNC + noVNC (websockify bridge)
- **Openbox** window manager
- **SSH server** for remote access and port forwarding
- **Dev tools**: Node.js 24, Python 3, Poetry, Git

### Architecture

All services are managed by s6-overlay:

| Service        | Port  | Description                    |
|----------------|-------|--------------------------------|
| sshd           | 22    | SSH server for remote access   |
| svc-agent   | 18789 | OpenClaw gateway               |
| svc-xvnc       | 5900  | TigerVNC X server              |
| svc-novnc      | 3000  | noVNC websockify bridge        |
| svc-desktop    | -     | Openbox + Chromium             |

### Persistent Data

The entire `/home/claworc` directory is a single persistent volume containing:
- `.openclaw/` - OpenClaw configuration
- `chrome-data/` - Chromium user data and CDP

Homebrew lives at `/home/linuxbrew/.linuxbrew` (separate volume).

## Architectures

All images support **AMD64** and **ARM64** platforms.
