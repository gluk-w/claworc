# Per-Agent Discord Connections

Claworc can connect each agent (OpenClaw instance) to Discord with a
structured per-agent config: a bot token plus a server/channel allowlist and
DM policy. It is the Discord counterpart of the Slack feature (see
`docs/slack-connections.md`) and shares its architecture; this doc covers the
Discord-specific parts.

## Prerequisites (Discord side)

Claworc cannot create the Discord app itself. You need:

- A Discord application with a **bot** and its **bot token** (Developer
  Portal → Bot → Reset Token). One token is sufficient — OpenClaw connects
  via the Gateway/WebSocket transport only, so there is no app-level token,
  public key, or interactions endpoint to configure.
- **Message Content Intent enabled** in Developer Portal → Bot → Privileged
  Gateway Intents. Without it the gateway connection fails with a
  disallowed-intents error.
- The bot invited to each server with scopes `bot` + `applications.commands`
  and baseline permissions (View Channels, Send Messages, Read Message
  History, Embed Links, Attach Files).

## Differences from Slack

- **Single credential**: the token is stored in the instance's encrypted
  env-vars map as `DISCORD_BOT_TOKEN` (OpenClaw's env fallback for the
  default account when `channels.discord.token` is unset).
- **Two-level allowlist**: entries are `{guild_id, channel_id?}` pairs keyed
  by raw numeric snowflake IDs (Discord → Developer Mode → right-click → Copy
  ID). An entry with no channel ID allows the *whole server*; a server with
  channel entries restricts to exactly those channels. Mixing both styles for
  the same server is rejected. Rendered shape:

  ```json
  {
    "enabled": true,
    "groupPolicy": "allowlist",
    "guilds": {
      "123456789012345678": {
        "channels": {
          "234567890123456789": { "allow": true, "requireMention": true }
        }
      },
      "999999999999999999": { "requireMention": true }
    },
    "dmPolicy": "pairing"
  }
  ```

- **`groupPolicy` is always written explicitly**: unlike Slack (which
  defaults to allowlist), OpenClaw's Discord runtime defaults to
  `groupPolicy: "open"` when unset — every guild the bot is in would be
  answered. Claworc always renders `"allowlist"` for secure-by-default
  behavior.

Everything else matches Slack: structure in `Instance.DiscordConfig`
(additive column, noop migration 00014), boot-time delivery via the reserved
`OPENCLAW_INITIAL_DISCORD` env var applied by `svc-openclaw/run`
(unset-then-set, authoritative when present, hands off when absent), live SSH
push + gateway restart for config-only edits, container restart for token
changes, `GET/PUT /api/v1/instances/{id}/discord`, a `discord` object on
instance create, the settings-tab card, and the create-form section. Clones
carry no Discord connection.

## Scope / deferred

Default Discord account only. Multi-account (`channels.discord.accounts.*`),
per-guild `users`/`roles` sender allowlists, role-based agent bindings
(`bindings[].match.roles`), group-DM settings, and the presence/guild-members
privileged intents are not surfaced — all remain reachable by editing the
agent's OpenClaw config directly, as long as Discord is not also managed
through this feature (Claworc owns the whole `channels.discord` path once
used).
