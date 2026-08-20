export interface DiscordChannelRule {
  /** Raw numeric Discord server (guild) ID — not the server name. */
  guild_id: string;
  /** Raw numeric channel ID; empty/undefined allows the whole server. */
  channel_id?: string;
  /** Undefined means OpenClaw's default (true): respond only when @-mentioned. */
  require_mention?: boolean;
}

/** GET /instances/{id}/discord response. */
export interface InstanceDiscord {
  configured: boolean;
  enabled: boolean;
  channels: DiscordChannelRule[];
  dm_policy: string;
  has_bot_token: boolean;
  /** Masked token for display (e.g. "****abcd"), only when a token is set. */
  bot_token_masked?: string;
  /** Set by PUT when a token change triggered a container restart. */
  restarting?: boolean;
}

/** PUT /instances/{id}/discord payload. Omitted fields keep their current value. */
export interface InstanceDiscordUpdatePayload {
  enabled?: boolean;
  channels?: DiscordChannelRule[];
  dm_policy?: string;
  /** Token: omit = keep, "" = remove, non-empty = set. */
  bot_token?: string;
}

/** Create-time Discord config embedded in InstanceCreatePayload. */
export interface DiscordCreateConfig {
  enabled: boolean;
  channels?: DiscordChannelRule[];
  dm_policy?: string;
  bot_token?: string;
}
