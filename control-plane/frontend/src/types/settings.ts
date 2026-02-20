export interface Settings {
  brave_api_key: string;
  api_keys: Record<string, string>;
  base_urls: Record<string, string>;
  default_models: string[];
  default_container_image: string;
  default_vnc_resolution: string;
  default_cpu_request: string;
  default_cpu_limit: string;
  default_memory_request: string;
  default_memory_limit: string;
  default_storage_homebrew: string;
  default_storage_clawd: string;
  default_storage_chrome: string;
}

export interface SettingsUpdatePayload {
  default_models?: string[];
  api_keys?: Record<string, string>;
  base_urls?: Record<string, string>;
  delete_api_keys?: string[];
  brave_api_key?: string;
  default_container_image?: string;
  default_vnc_resolution?: string;
  default_cpu_request?: string;
  default_cpu_limit?: string;
  default_memory_request?: string;
  default_memory_limit?: string;
  default_storage_homebrew?: string;
  default_storage_clawd?: string;
  default_storage_chrome?: string;
}

// Keep backward compat alias
export type SettingsUpdate = SettingsUpdatePayload;

// Provider analytics types
export interface ProviderStats {
  provider: string;
  total_requests: number;
  error_count: number;
  error_rate: number;
  avg_latency: number;
  last_error?: string;
  last_error_at?: string;
}

export interface ProviderAnalyticsResponse {
  providers: Record<string, ProviderStats>;
  period_days: number;
  since: string;
}

export type ProviderHealthStatus = "healthy" | "warning" | "error" | "unknown";
