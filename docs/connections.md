# Connections (Composio)

> Part of the [Internal Proxy](./internal-proxy.md). Connections are served on the
> `/connections/` route of the shared `internal/internalproxy` server, alongside
> the [LLM gateway](./virtual-keys.md).

## Overview

**Connections** let an OpenClaw agent use OAuth-backed third-party services
(Gmail, Google Analytics, and anything else [Composio](https://composio.dev)
supports) **without the agent ever holding any credential**.

- The control plane holds a single, global **Composio API key** (encrypted,
  configured in Settings → API Keys).
- The OAuth connect flow runs entirely on the control plane.
- The agent reaches Composio only through the `/connections/` broker on the
  internal proxy, authenticated by a per-instance `CLAWORC_CONNECTION_SECRET`.
  The broker injects the real Composio API key and a server-derived `user_id`.

No OAuth tokens are stored on the control plane — Composio holds them, keyed by a
stable per-instance `user_id`.

## Concepts

| Composio term | Meaning here |
|---------------|--------------|
| **Toolkit** | A connectable service (Gmail, Google Analytics, …). Listed via `GET /toolkits?managed_by=composio`. |
| **Auth config** | Per-toolkit OAuth blueprint using Composio-managed auth. One is created+cached per toolkit (`composio_auth_configs` table). |
| **Connected account** | A user's authorized link to a toolkit. One Composio `user_id` per Claworc instance: `claworc-inst-<instance UUID>`. |

A connection row (`composio_connections` table) records `instance_id`,
`toolkit_slug`, `name`, the opaque Composio connected-account id, `status`
(`INITIATED` → `ACTIVE`/`FAILED`/`EXPIRED`), and an account label for display.

## Connect flow

```
UI (Add connection wizard)            Control plane                 Composio
  pick toolkit ───────────────────►  POST /instances/{id}/connections
                                       ensure CLAWORC_CONNECTION_SECRET
                                       ensure auth_config (cached) ──► POST /auth_configs
                                       link(user_id, auth_config) ──► POST /connected_accounts/link
                                     ◄── { connected_account_id, redirect_url }   (NO db row yet)
  window.open(redirect_url) ──────────────────────────────────────► hosted OAuth consent
  user authorizes ◄───────────────── redirect to /connections/callback (our origin)
  callback postMessage → wizard
  confirm ─────────────────────────►  POST /instances/{id}/connections/confirm
                                       check status ──────────────► GET /connected_accounts/{id}
                                       if ACTIVE → persist row
                                     ◄── { status: ACTIVE, connection }
  list refreshes, wizard closes
```

A pending connection lives **only in the browser's memory** until it is
confirmed `ACTIVE` — `POST /connections` never writes a row, so an abandoned flow
leaves nothing behind. The OAuth callback is handled **entirely in the browser**:
`callback_url` is a client-side SPA route (`/connections/callback`) on our own
origin that `postMessage`s the opener and closes. On that message the wizard
calls `confirm`, which checks the status with Composio and persists the row only
if `ACTIVE`. The popup closing is a fallback trigger for the same confirm step.

## CLAWORC_CONNECTION_SECRET

A per-instance secret (`claworc-cs-<48 hex>`) authenticates the agent to the
`/connections/` broker.

- Generated for **every instance** and re-ensured on every container/pod
  (re)create, so the env var is always present (`injectConnectionSecret` in
  `internal/handlers/instances.go`, reserved name in `internal/handlers/envvars.go`).
- Stored Fernet-encrypted on the instance row (`connection_secret`), with an
  indexed SHA-256 hash (`connection_secret_hash`) so the broker resolves the
  owning instance in O(1) without decrypting rows.
- Injected as the reserved `CLAWORC_CONNECTION_SECRET` env var.

## Proxy contract (agent side)

The broker exposes a narrow allowlist — everything else is rejected. The agent
(e.g. via a user-authored OpenClaw skill) calls:

```bash
# Discover the tools available for this instance's connected toolkits
curl -s http://127.0.0.1:40001/connections/tools \
  -H "Authorization: Bearer $CLAWORC_CONNECTION_SECRET"

# Execute a tool
curl -s -X POST http://127.0.0.1:40001/connections/tools/execute/GMAIL_SEND_EMAIL \
  -H "Authorization: Bearer $CLAWORC_CONNECTION_SECRET" \
  -H 'Content-Type: application/json' \
  -d '{"arguments":{"recipient_email":"x@y.z","subject":"Hi","body":"…"}}'
```

The broker injects the real `x-api-key` and forces `user_id` to the instance's
derived value — any client-supplied `user_id`/`connected_account_id` is stripped.
`GET /tools` is scoped to the instance's `ACTIVE` toolkits; with no connections it
returns an empty list rather than the full catalog.

## Generated skill

When a connection becomes `ACTIVE`, the control plane auto-generates an OpenClaw
skill and writes it into the instance at
`/home/claworc/.openclaw/skills/claworc-<toolkit-slug>/SKILL.md`.

- **Name** — `claworc-<toolkit-slug>` (e.g. `claworc-gmail`).
- **Description** — `Integration with <Toolkit Name>. <toolkit description>` (the
  description is fetched from Composio).
- **Body** — a discovery `curl` recipe (`GET /connections/tools`) followed by one
  section per tool. Each tool section lists the tool description, every input and
  output parameter (name, type, required flag, description), and a complete
  example `curl` request (full execute URL, `Authorization` header, and a JSON
  request body with a placeholder per input parameter). The instance's connection
  secret value is baked directly into the `Authorization: Bearer …` header of
  every example.

The skill is (re)generated on connect and whenever the instance reconnects over
SSH (so it survives container recreation), and removed on disconnect — unless
another active connection still uses the same toolkit.

Generation lives in `internal/internalproxy/composio_skill.go`
(`GenerateConnectionSkill` / `BuildConnectionSkill`); deployment over SSH reuses
the existing skill-deploy path in `internal/handlers/`.

## Lifecycle

- **Instance restart** — the secret is re-injected from the DB; connections
  survive because Composio holds the tokens keyed by the stable UUID-derived
  `user_id`.
- **Connection delete** — best-effort delete of the Composio connected account,
  then the local row is removed, then the generated skill is removed from the
  instance (unless another active connection still uses the same toolkit).
- **Instance delete** — best-effort delete of every connected account, then the
  connection rows are removed; the secret dies with the instance row.

## API endpoints (control plane)

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/api/v1/connections/toolkits` | List connectable OAuth toolkits |
| `GET` | `/api/v1/instances/{id}/connections` | List an instance's connections |
| `POST` | `/api/v1/instances/{id}/connections` | Initiate a connection (no row) → `redirect_url` |
| `POST` | `/api/v1/instances/{id}/connections/confirm` | Confirm ACTIVE with Composio; persist on success |
| `DELETE` | `/api/v1/instances/{id}/connections/{connId}` | Remove a connection |

## Key reference

| File | Description |
|------|-------------|
| `control-plane/internal/internalproxy/composio.go` | `/connections/` broker (allowlist, key/user_id injection) |
| `control-plane/internal/internalproxy/composio_client.go` | Control-plane Composio REST client (wizard) |
| `control-plane/internal/internalproxy/composio_skill.go` | Generates the `claworc-<toolkit>` skill (toolkit/tool fetch + SKILL.md builder) |
| `control-plane/internal/internalproxy/connection_keys.go` | `CLAWORC_CONNECTION_SECRET` generation / resolution |
| `control-plane/internal/handlers/composio.go` | REST handlers for the wizard + connection CRUD |
| `control-plane/internal/database/models/models.go` | `ComposioConnection`, `ComposioAuthConfig` |
| `control-plane/frontend/src/common/components/ConnectionModal.tsx` | Add-connection wizard |
| `control-plane/frontend/src/common/components/ConnectionsSection.tsx` | Connections card on the agent Settings tab |
