# Internal Proxy

## Overview

The **internal proxy** (`control-plane/internal/internalproxy`) is a single HTTP
server the control plane runs on `127.0.0.1:<port>` (default `40001`,
`CLAWORC_LLM_GATEWAY_PORT`). It is **never exposed publicly** — OpenClaw instances
reach it only over a per-instance SSH agent-listener tunnel (label
`InternalProxy`).

Its job is to let an instance talk to external services **without ever holding
the real credentials**. Every request from an instance carries a Claworc-issued
token; the proxy validates it, strips it, injects the real upstream credential,
and forwards the request. Real provider/API keys stay on the control plane and
never enter a container, log, or backup that an instance can read.

```
OpenClaw container
 └─ openclaw / skills  (Claworc-issued token, never the real key)
     │  SSH agent-listener tunnel  →  127.0.0.1:<port> on the control plane
     ▼
Control plane — internal proxy (127.0.0.1:40001)
   "/"             → LLM virtual-key proxy   (see virtual-keys.md)
   "/connections/" → Composio broker         (see connections.md)
   "/webhooks/"    → inter-agent webhook trigger
     ▼
Upstream (LLM provider · Composio · …) with the REAL credential injected
```

## Route map

Routes other than the catch-all are installed before `Start` via
`internalproxy.RegisterRoute(pattern, handler)` (net/http `ServeMux` semantics —
a trailing-slash subtree like `/connections/` wins over the catch-all `/`).

| Route | Purpose | Auth presented by the instance | Doc |
|-------|---------|--------------------------------|-----|
| `/` | LLM gateway proxy to model providers | `claworc-vk-*` virtual key | [virtual-keys.md](./virtual-keys.md) |
| `/connections/` | Composio REST broker for OAuth toolkits | `claworc-cs-*` connection secret | [connections.md](./connections.md) |
| `/webhooks/` | Private inter-agent webhook trigger | private webhook API key | — |

## Auth schemes

The proxy supports independent credential schemes per route subtree, each mapping
an instance-scoped token to the real upstream credential:

- **`claworc-vk-*` (LLM virtual keys)** — per-instance, per-provider tokens stored
  in the `llm_gateway_keys` table. Resolve to the provider's encrypted API key (or
  OAuth material). See [virtual-keys.md](./virtual-keys.md).
- **`claworc-cs-*` (connection secret)** — one per instance, injected as the
  `CLAWORC_CONNECTION_SECRET` env var. Stored Fernet-encrypted on the instance row
  with an indexed SHA-256 hash (`connection_secret_hash`) so the proxy can resolve
  the owning instance without decrypting every row. Resolves to the global Composio
  API key. See [connections.md](./connections.md).

## Tunnel

A single `InternalProxy` agent-listener tunnel per instance carries all of the
above. It is created/reconciled by `TunnelManager` (see
`control-plane/internal/sshproxy/tunnel.go`): the control plane uses the existing
per-instance SSH connection to make the agent listen on the proxy port and
forward connections back to `127.0.0.1:<port>` on the control plane.

## Key reference

| File | Description |
|------|-------------|
| `internal/internalproxy/gateway.go` | LLM proxy: auth, key resolution, forwarding, logging |
| `internal/internalproxy/keys.go` | LLM virtual-key generation / lifecycle |
| `internal/internalproxy/composio.go` | `/connections/` Composio broker |
| `internal/internalproxy/composio_client.go` | Control-plane Composio REST client (wizard) |
| `internal/internalproxy/connection_keys.go` | `CLAWORC_CONNECTION_SECRET` generation / resolution |
| `internal/sshproxy/tunnel.go` | `InternalProxy` agent-listener tunnel |
| `internal/config/config.go` | `InternalProxyPort` (`CLAWORC_LLM_GATEWAY_PORT`) |
