# Per-Agent Slack Connections

Claworc can connect each agent (OpenClaw instance) to a Slack workspace with a
structured per-agent config: bot/app tokens plus a channel allowlist and DM
policy. It is a thin layer over OpenClaw's native Slack channel plugin — Claworc
stores the settings, delivers them to the instance, and OpenClaw does the rest.

## Prerequisites (Slack side)

Claworc cannot create the Slack app itself. For each workspace you need a Slack
app in **Socket Mode** with:

- A **bot token** (`xoxb-…`) from *OAuth & Permissions* with the usual bot scopes
  (`app_mentions:read`, `chat:write`, `channels:history`, `im:history`, …).
- An **app-level token** (`xapp-…`) with the `connections:write` scope from
  *Basic Information → App-Level Tokens*.

The bot must also be invited to each channel it should respond in.

## How it works

### Storage

- **Tokens** are stored in the instance's encrypted env-vars map
  (`Instance.EnvVars`, Fernet at rest) under `SLACK_BOT_TOKEN` and
  `SLACK_APP_TOKEN`. They are injected into the container as environment
  variables; OpenClaw's Slack plugin reads them from the environment for the
  default account whenever `channels.slack.botToken`/`appToken` are unset. The
  tokens therefore never appear in the agent's `openclaw.json` on the PVC.
- **Structure** (enabled flag, channel allowlist, DM policy) is stored as JSON
  in `Instance.SlackConfig` (column added via AutoMigrate; no hand-written
  migration).

### Delivery

The stored structure is rendered into an OpenClaw `channels.slack` block, e.g.:

```json
{
  "enabled": true,
  "groupPolicy": "allowlist",
  "channels": {
    "C0123456789": { "enabled": true, "requireMention": true }
  },
  "dmPolicy": "pairing"
}
```

and delivered two ways:

1. **At boot** — `buildCreateParams` (and the create path) sets the reserved
   `OPENCLAW_INITIAL_SLACK` env var; the agent image's `svc-openclaw/run`
   script applies it with `openclaw config unset channels.slack` followed by
   `openclaw config set channels.slack … --json` before the gateway starts.
   Every container (re)start therefore reconciles the agent's Slack config
   from the DB. When the env var is absent (Slack never configured through
   Claworc), the script leaves `channels.slack` alone, so manual edits via the
   Config tab keep working.
2. **On edit while running** — `PUT /api/v1/instances/{id}/slack` pushes the
   same unset+set sequence over SSH and restarts the gateway
   (`applySlackConfig` in `internal/handlers/slack.go`), so channel/DM changes
   apply without a container restart. A *token* change instead triggers the
   standard env-vars container restart, which re-applies everything via (1).

The unset-before-set is required because `openclaw config set` deep-merges map
values — without it, channels removed in Claworc would linger. Consequence:
once Slack is managed through Claworc, Claworc owns the whole `channels.slack`
path and will overwrite manual edits under it.

### API

- `GET /api/v1/instances/{id}/slack` — returns `configured`, `enabled`,
  `channels`, `dm_policy`, `has_bot_token`/`has_app_token` (reflecting the
  merged global+instance env vars) and masked token previews.
- `PUT /api/v1/instances/{id}/slack` — partial update: omitted fields keep
  their value; tokens use omit=keep, `""`=remove, value=set. Responds with the
  new state plus `restarting: true` when a token change kicked off a restart.
- Create: `POST /api/v1/instances` accepts a `slack` object
  (`{enabled, channels, dm_policy, bot_token, app_token}`) so a new agent
  connects to Slack on first boot.

Access follows the standard per-instance settings model
(`middleware.CanAccessInstance`: admins and team managers).

### Validation

Channel entries must be raw Slack channel IDs (`C…`/`G…`, uppercase
alphanumeric with at least one digit — `#name` prefixes are stripped, input is
uppercased, duplicates dropped). Names are rejected up front because OpenClaw
routes by ID under `groupPolicy: "allowlist"` and name keys silently fail to
match. `dm_policy` is one of `pairing` (default), `open` (rendered with
`allowFrom: ["*"]`, which OpenClaw requires for open DMs), or `disabled`.

## UI

- **Agent → Settings → Slack** card (next to Webhook): enable toggle, masked
  token inputs (leave blank to keep; remove via the Environment Variables
  card), channel list with per-channel "require @-mention", DM policy select.
- **Create Agent form**: an optional Slack card with the same fields.

## Scope / deferred

Socket Mode + the default Slack account only. HTTP/relay modes, multiple
workspaces per agent (`channels.slack.accounts.*`), per-channel user
allowlists, and DM pairing management are not surfaced — all remain reachable
by editing the agent's OpenClaw config directly (Config tab), as long as Slack
is not also managed through this feature (see ownership note above).

Cloned agents deliberately get no Slack connection (clone copies neither
env vars nor `SlackConfig`) — two agents sharing one bot token would both
answer every message.
