export type SSHConnectionState =
  | "disconnected"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "failed";

export interface SSHHealthMetrics {
  connected_at: string;
  last_health_check: string;
  uptime_seconds: number;
  successful_checks: number;
  failed_checks: number;
  healthy: boolean;
}

export interface SSHTunnelStatus {
  service: string;
  local_port: number;
  remote_port: number;
  created_at: string;
  last_check?: string;
  last_successful_check?: string;
  last_error?: string;
  bytes_transferred: number;
  healthy: boolean;
}

export interface SSHStateEvent {
  from: string;
  to: string;
  timestamp: string;
}

export interface SSHStatusResponse {
  connection_state: SSHConnectionState;
  health: SSHHealthMetrics | null;
  tunnels: SSHTunnelStatus[];
  recent_events: SSHStateEvent[];
}

export interface SSHEvent {
  instance_name: string;
  type: string;
  details: string;
  timestamp: string;
}

export interface SSHEventsResponse {
  events: SSHEvent[];
}

export interface SSHTestResult {
  success: boolean;
  latency_ms: number;
  tunnel_status: {
    service: string;
    healthy: boolean;
    error?: string;
  }[];
  command_test: boolean;
  error?: string;
}

export interface SSHReconnectResponse {
  success: boolean;
  message: string;
}

export interface SSHFingerprintResponse {
  fingerprint: string;
  algorithm: string;
  verified: boolean;
}

export interface GlobalSSHInstanceStatus {
  instance_id: number;
  instance_name: string;
  display_name: string;
  instance_status: string;
  connection_state: SSHConnectionState;
  health: SSHHealthMetrics | null;
  tunnel_count: number;
  healthy_tunnels: number;
}

export interface GlobalSSHStatusResponse {
  instances: GlobalSSHInstanceStatus[];
  total_count: number;
  connected: number;
  reconnecting: number;
  failed: number;
  disconnected: number;
}

export interface SSHUptimeBucket {
  label: string;
  count: number;
}

export interface SSHHealthRate {
  instance_name: string;
  display_name: string;
  success_rate: number;
  total_checks: number;
}

export interface SSHReconnectionCount {
  instance_name: string;
  display_name: string;
  count: number;
}

export interface SSHMetricsResponse {
  uptime_buckets: SSHUptimeBucket[];
  health_rates: SSHHealthRate[];
  reconnection_counts: SSHReconnectionCount[];
}

export interface IPRestrictResponse {
  instance_id: number;
  allowed_ips: string;
  normalized_list: string;
}
