export type ChannelOverallStatus =
  | "healthy"
  | "degraded"
  | "unhealthy"
  | "unreachable"
  | "no_channels"
  | "unknown"
  | "disabled";

export type ChannelAccountStatus =
  | "healthy"
  | "disconnected"
  | "not_running"
  | "stale"
  | "disabled"
  | "unknown";

export interface ChannelAccountHealth {
  channel: string;
  account_id: string;
  status: ChannelAccountStatus;
  enabled: boolean;
  running: boolean;
  connected: boolean;
  mode: string;
  last_event_at: string | null;
  last_inbound_at: string | null;
  last_outbound_at: string | null;
  last_error: string;
  reconnect_attempts: number;
  checked_at: string | null;
}

export interface ChannelHealth {
  instance_id: number;
  overall: ChannelOverallStatus;
  gateway_reachable: boolean;
  checked_at: string | null;
  channels: ChannelAccountHealth[];
}

/** Compact summary embedded in Instance list/detail responses. */
export interface ChannelHealthSummary {
  overall: ChannelOverallStatus;
  unhealthy_count: number;
  checked_at: string | null;
}
