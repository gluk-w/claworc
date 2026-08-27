# Connections (Composio)

> Part of the [Internal Proxy](./internal-proxy.md). Connections are served on the
> `/connections/` route of the shared `internal/internalproxy` server, alongside
> the [LLM proxy](./virtual-keys.md).

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

## Required API key permissions

Composio supports **scoped project API keys** whose permission areas are chosen
at creation time and **cannot be changed afterwards** — a wrong key has to be
recreated. Claworc needs all six of these:

| Permission area | Why | Call sites |
|---|---|---|
| Toolkits — read | Wizard catalog + toolkit metadata for the generated skill | `GET /toolkits`, `GET /toolkits/{slug}` |
| Tools — read | Tool discovery for the skill and the `/connections/tools` broker route | `GET /tools` |
| Tool execution — write | The agent actually running a tool | `POST /tools/execute/{slug}` |
| Auth configs — write | Creating the cached per-toolkit OAuth blueprint | `POST /auth_configs` |
| Connected accounts — read | Confirming a connection became `ACTIVE` | `GET /connected_accounts/{id}` |
| Connected accounts — write | Starting the OAuth link, and cleanup on disconnect | `POST /connected_accounts/link`, `DELETE /connected_accounts/{id}` |

Composio exposes no endpoint that reports a key's own scopes, so Settings →
API Keys → **Composio API Key → Test** probes them
(`POST /api/v1/settings/composio/test`, `internal/internalproxy/composio_probe.go`):

- read areas are probed with harmless `GET`s;
- write areas are probed with a deliberately **invalid** body. A missing scope is
  rejected 401/403 at the auth layer, while an authorized request gets a 400/404
  validation error — so a passing probe never creates anything at Composio.

`writeProbePassed` holds that classification in one place. The button only
appears while the key input is open, so it tests the key as typed — before it is
saved. (The endpoint also accepts an omitted or masked `api_key` and falls back
to the stored key.)

The [nightly contract check](#contract-drift-check-nightly) probes the same paths
with the same bodies and the same 401/403 semantics, so if Composio ever starts
rejecting an unscoped write *before* the auth layer, the nightly run surfaces it
rather than the Test button silently reporting a false pass.

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
returns an empty list rather than the full catalog. The broker pages through
Composio server-side and returns the aggregate as
`{"items":[…],"next_cursor":null}`, dropping any item whose `toolkit.slug` is not
one of the instance's `ACTIVE` toolkits — the same defence-in-depth filter the
skill generator applies.

## Generated skill

When a connection becomes `ACTIVE`, the control plane auto-generates an OpenClaw
skill and writes it into the instance at
`/home/claworc/.openclaw/skills/claworc-<toolkit-slug>/SKILL.md`.

- **Name** — `claworc-<toolkit-slug>` (e.g. `claworc-gmail`).
- **Description** — `Integration with <Toolkit Name>. <toolkit description>` (the
  description is fetched from Composio).
- **Body** — a discovery `curl` recipe (`GET /connections/tools`) followed by the
  toolkit's tools, split into two sections:
  - **`## Tools`** — Composio's curated *important* subset
    (`GET /tools?toolkit_slug=<slug>&important=true`), one section per tool: the
    tool description, every input and output parameter (name, type, required
    flag, description), and a complete example `curl` request (full execute URL,
    `Authorization` header, and a JSON request body with a placeholder per input
    parameter). The instance's connection secret value is baked directly into the
    `Authorization: Bearer …` header of every example. If Composio has no
    important subset for the toolkit, the first `maxDetailedTools` (10) of the
    full list are rendered here instead.
  - **`## Other tools`** — every remaining tool as a `` `SLUG` — description ``
    bullet. Their schemas are one discovery call away, and rendering them in full
    would make a large toolkit's SKILL.md unusable.

Both fetches page through `next_cursor` and filter the returned items by
`toolkit.slug`. That filter is deliberate: Composio's parameter is
`toolkit_slug` (**singular**) and unknown query params are silently ignored
rather than rejected, so a wrong name returns the whole catalog. Re-checking each
item means a future rename degrades to an empty skill instead of one full of
another toolkit's tools.

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

## Contract drift check (nightly)

Composio **ignores unknown query parameters instead of rejecting them**, so a
renamed path segment or filter never surfaces as an error — it surfaces as a
`200` carrying the wrong data. That is how `toolkit_slugs` (plural) shipped:
every generated skill listed a foreign toolkit's tools and nothing failed.

`.github/workflows/composio-contract.yml` runs nightly (06:00 UTC, plus
`workflow_dispatch` and any push touching `internalproxy/composio*.go`) against
the live API using the `COMPOSIO_API_KEY` repository secret. It executes the
build-tagged suite in `internal/internalproxy/composio_live_test.go`:

```bash
cd control-plane
COMPOSIO_API_KEY=... go test -tags composio_live -count=1 -v \
  ./internal/internalproxy/ -run TestComposioContract
```

The tests assert on each parameter's **effect**, not just on a 2xx:

| Test | Guards against |
|---|---|
| `ToolkitCatalog` / `ToolkitDetail` | `/toolkits` + `/toolkits/{slug}` paths, `managed_by`, name/description moving between top level and `meta` |
| `ToolkitSlugFilter` | `toolkit_slug` being renamed or ignored — every item must belong to the requested toolkit, and the unfiltered catalog must still be visibly broader |
| `ImportantFilter` | `important=true` no longer narrowing the result |
| `Pagination` | `limit` / `cursor` / `next_cursor` renames that would silently truncate a skill to one page |
| `ToolItemShape` | `input_parameters` / `output_parameters` / `description` renames |
| `WritePaths` / `ReadPaths` | `/auth_configs`, `/connected_accounts/link`, `/tools/execute/{slug}`, `/connected_accounts` moving (404) or the key losing a scope (401/403) — the same probes the Settings **Test** button uses (see [Required API key permissions](#required-api-key-permissions)) |

The write paths are probed with bodies that cannot produce a valid create, and
the execute probe uses a `user_id` with no connected account, so a run never
mutates anything at Composio. A failing scheduled run opens (or comments on) a
`composio-contract`-labelled issue.

## API endpoints (control plane)

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/v1/settings/composio/test` | Verify the global API key and its permissions (admin) |
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
| `control-plane/internal/internalproxy/composio_probe.go` | API-key permission probes behind the Settings **Test** button |
| `control-plane/internal/internalproxy/composio_skill.go` | Generates the `claworc-<toolkit>` skill (toolkit/tool fetch + SKILL.md builder) |
| `control-plane/internal/internalproxy/composio_live_test.go` | Live API contract assertions (`-tags composio_live`), run by the nightly workflow |
| `control-plane/internal/internalproxy/connection_keys.go` | `CLAWORC_CONNECTION_SECRET` generation / resolution |
| `control-plane/internal/handlers/composio.go` | REST handlers for the wizard + connection CRUD, and the API-key `Test` endpoint |
| `control-plane/internal/database/models/models.go` | `ComposioConnection`, `ComposioAuthConfig` |
| `control-plane/frontend/src/app/pages/SettingsPage.tsx` | Global **Composio API Key** field and its **Test** button |
| `control-plane/frontend/src/common/components/ConnectionModal.tsx` | Add-connection wizard |
| `control-plane/frontend/src/common/components/ConnectionsSection.tsx` | Connections card on the agent Settings tab |
