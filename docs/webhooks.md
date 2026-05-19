# Webhooks

External systems and other AI agents can send a message (and optional file
attachments) to an OpenClaw instance over HTTP and receive the agent's
reply synchronously on the same request. Each call is routed through the
existing OpenClaw chat protocol — webhooks are just a new transport on
top of it, not a separate runtime.

Webhook configuration is per-instance and lives under the instance's
settings page (right after **Enabled models**). There is no separate
"triggers" page and no global webhook concept.

## Routes

There are two webhook entrypoints, distinguished by *which keys they
accept*, not by what they do:

| Endpoint | Path | Authenticates against | Reachable from |
| --- | --- | --- | --- |
| Public | `POST /webhooks/{instance-uuid}` on the control plane | `WebhookApiKey` rows where `is_private = false` | The public network the control-plane is exposed on |
| Private | `POST http://127.0.0.1:<LLMGatewayPort>/webhooks/{instance-uuid}` | `WebhookApiKey` rows where `is_private = true` | Reachable from other AI agents only — each instance gets an agent-listener tunnel that pins the gateway to `127.0.0.1:<LLMGatewayPort>` on the instance side (see `internal/sshproxy/tunnel.go`). |

The control plane returns the private URL in its absolute form (e.g.
`http://127.0.0.1:40001/webhooks/<uuid>`) so the UI can show what
another agent should `curl`. The public URL is intentionally returned as
a relative path (e.g. `/webhooks/<uuid>`); the browser resolves it
against `window.location.origin`, which is correct under the Vite dev
proxy, behind a reverse proxy, and on the bare control-plane port.

`{instance-uuid}` is `Instance.UUID` (stable, non-enumerable). The
sequential `Instance.ID` is never used in webhook URLs.

A request returns 404 when:

- No instance has the supplied UUID, or
- The instance has zero keys whose `is_private` matches the endpoint.

The 404 (rather than 401) shape is intentional so callers can't probe
whether a UUID exists by varying their token.

## Request format

`POST /webhooks/{instance-uuid}` accepts either body:

- `application/json`
  ```json
  { "session_name": "<conversation-name>", "message": "..." }
  ```
- `multipart/form-data` with `session_name` and `message` form fields plus
  one or more `file` parts.

Authentication is **`Authorization: Bearer <raw-token>` only**. No
custom header is honored — keeping a single, well-known auth path is
simpler and harder to misconfigure.

`session_name` is any non-empty string of Latin letters, digits, dashes,
underscores, and dots (regex `^[A-Za-z0-9._-]+$`). The same `session_name`
across calls reuses the OpenClaw session, so a webhook conversation can
span multiple HTTP requests.

## Response format

The handler holds the HTTP request open until the OpenClaw agent emits
its `lifecycle/end` frame, then writes the assistant's final reply as
`text/plain` (no JSON envelope). The body is the agent's response and
nothing else — callers can pipe it directly into another tool.

There is no internal timeout — the request is bounded by the caller's
own HTTP client timeout (or by `r.Context()` cancellation when the
client disconnects). Callers should size their timeout to the longest
reply they expect from the agent.

## Attachment delivery

Files sent in a multipart request are written into the **target
instance** at:

```
/tmp/webhooks/{session_name}/<original-filename>
```

…using the same SSH/SFTP path the file-upload UI uses (`handlers.WriteInstanceFile`). The message body sent to OpenClaw is then
prefixed with a short preamble listing each path so the agent can read
them:

```
Attached files:
- /tmp/webhooks/<session>/foo.png

<original message>
```

The OpenClaw chat protocol is unchanged.

## Bridge into OpenClaw chat

A single shared helper, `handlers.RunWebhookBridge`, drives both
entrypoints. It:

1. Dials the per-instance OpenClaw gateway over the existing SSH tunnel
   (`getTunnelPort(id, "gateway")` + `sshproxy.DialGateway`).
2. Sends one `chat.send` frame with `sessionKey = session_name` — i.e.
   the caller-supplied session id becomes the OpenClaw session key, so
   conversational continuity is preserved across calls with the same id.
3. Reads gateway events. `payload.stream == "assistant"` events carry a
   **cumulative** snapshot in `payload.data.text`; the bridge keeps the
   latest snapshot. The loop exits when `payload.stream == "lifecycle"`
   with `data.phase == "end"`.
4. Returns the accumulated assistant text to the HTTP layer, which
   serializes it as `reply`.

This mirrors the moderator runner (`internal/moderator/runner.go`) —
look there for any clarifications on the OpenClaw event shape.

## Data model

Three pieces, all in `internal/database/models/`:

- `Instance.UUID` — added by AutoMigrate, backfilled by migration
  `00007_backfill_instance_uuid.go`. New rows get a v4 UUID from the
  `BeforeCreate` GORM hook.
- `WebhookApiKey` — `(InstanceID, Key, Label, IsPrivate, LastUsedAt,
  CreatedAt)`. `Key` is the raw 128-hex-char token (64 bytes from
  `crypto/rand`) encrypted at rest with the existing Fernet helper
  (`internal/utils/crypto.go`), same pattern as `Instance.GatewayToken`
  and `LLMProvider.APIKey`. The admin UI decrypts on read so an admin
  can copy the value back; auth at the request edge decrypts each
  candidate row and compares with `subtle.ConstantTimeCompare`.
- `WebhookLog` — `(InstanceID, SourceIP, SessionID, RequestBytes,
  ResponseBytes, StatusCode, DurationMs, ErrorMessage, KeyLast4,
  IsPrivate, CreatedAt)`. `KeyLast4` is denormalized so log rows
  survive key deletion.

No `Webhook` table — a webhook is implicit on every instance; only the
keys and logs are stored explicitly.

Tokens carry **no recognizable prefix** (e.g. no `claworc-wh-` marker).
Known-prefix tokens are trivially greppable in code leaks, paste sites,
and log dumps; opaque hex avoids feeding scanners.

## Permissions

The CRUD endpoints (`GET/POST/PATCH/DELETE /api/v1/instances/{id}/webhook…`)
are gated by `middleware.CanAccessInstance` — same model used for
**Resources** and **Enabled models**:

- Admins have full access.
- Team managers of the instance's team can edit.
- Plain team members get 403.

The webhook trigger routes themselves are not behind session auth at
all; they are authenticated by the bearer token.

## Migration

`migrations/migration_00007_backfill_instance_uuid.go` walks every
existing `Instance` row whose `UUID` is null/empty and writes a fresh
`uuid.New().String()`. AutoMigrate adds the column on every boot, so
fresh installs and upgrades both end up with a fully-populated unique
index.

## Files

- `internal/database/models/models.go` — `Instance.UUID` + `BeforeCreate`.
- `internal/database/models/webhook.go` — `WebhookApiKey`, `WebhookLog`.
- `internal/database/migrations/migration_00007_backfill_instance_uuid.go`.
- `internal/handlers/webhooks.go` — CRUD.
- `internal/handlers/webhook_trigger.go` — public + private entry handlers.
- `internal/handlers/webhook_bridge.go` — shared OpenClaw chat bridge.
- `internal/handlers/files.go` — `WriteInstanceFile` helper used for attachments.
- `internal/llmgateway/gateway.go` — `RegisterRoute` hook used to plug
  the private trigger into the gateway mux.
- `frontend/src/components/WebhookSection.tsx` — UI section embedded in
  the agent detail page after Enabled Models.
- `frontend/src/api/webhooks.ts`, `frontend/src/hooks/useWebhook.ts`,
  `frontend/src/types/webhook.ts`.
