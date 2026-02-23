export interface SSHStatusMetrics {
  connected_at: string;
  last_health_check: string;
  uptime: string;
  successful_checks: number;
  failed_checks: number;
}

export interface SSHStatusTunnel {
  label: string;
  local_port: number;
  remote_port: number;
  status: string;
  created_at: string;
  last_health_check: string;
  successful_checks: number;
  failed_checks: number;
  uptime: string;
}

export interface SSHStatusEvent {
  from: string;
  to: string;
  timestamp: string;
  reason: string;
}

export interface SSHStatusResponse {
  state: string;
  metrics: SSHStatusMetrics | null;
  tunnels: SSHStatusTunnel[];
  recent_events: SSHStatusEvent[];
}

export interface SSHTestResponse {
  status: "ok" | "error";
  output: string;
  latency_ms: number;
  error: string | null;
}

export interface SSHEventEntry {
  type: string;
  timestamp: string;
  details: string;
}

export interface SSHEventsResponse {
  events: SSHEventEntry[];
}

export interface SSHReconnectResponse {
  status: "ok" | "error";
  latency_ms: number;
  error: string | null;
}

export interface SSHFingerprintResponse {
  fingerprint: string;
  public_key: string;
}
