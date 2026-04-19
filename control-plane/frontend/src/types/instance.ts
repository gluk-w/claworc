export interface InstanceModels {
  effective: string[];
  disabled_defaults: string[];
  extra: string[];
}

export interface Instance {
  id: number;
  name: string;
  display_name: string;
  status: "creating" | "running" | "restarting" | "stopping" | "stopped" | "error";
  status_message?: string;
  cpu_request: string;
  cpu_limit: string;
  memory_request: string;
  memory_limit: string;
  storage_homebrew: string;
  storage_home: string;
  has_brave_override: boolean;
  models: InstanceModels;
  default_model: string;
  container_image: string | null;
  has_image_override: boolean;
  vnc_resolution: string | null;
  has_resolution_override: boolean;
  timezone: string | null;
  has_timezone_override: boolean;
  user_agent: string | null;
  has_user_agent_override: boolean;
  /** Per-instance env var overrides. Values are masked (e.g. "****abcd"). */
  env_vars: Record<string, string>;
  has_env_override: boolean;
  /** Set to true when env var changes were saved but a restart is needed to apply them. */
  requires_restart?: boolean;
  /** Set to true by the backend when it kicked off an auto-restart to apply env var changes. */
  restarting?: boolean;
  live_image_info?: string;
  allowed_source_ips: string;
  enabled_providers: number[];
  instance_providers: LLMProvider[];
  control_url: string;
  gateway_token: string;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

// Keep as distinct type for future detail-only fields
export type InstanceDetail = Instance;

export interface InstanceCreatePayload {
  display_name: string;
  cpu_request?: string;
  cpu_limit?: string;
  memory_request?: string;
  memory_limit?: string;
  storage_homebrew?: string;
  storage_home?: string;
  brave_api_key?: string | null;
  models?: { disabled: string[]; extra: string[] };
  default_model?: string;
  container_image?: string | null;
  vnc_resolution?: string | null;
  timezone?: string | null;
  user_agent?: string | null;
  enabled_providers?: number[];
  env_vars_set?: Record<string, string>;
}

export interface InstanceUpdatePayload {
  brave_api_key?: string;
  models?: { disabled: string[]; extra: string[] };
  default_model?: string;
  timezone?: string;
  user_agent?: string;
  allowed_source_ips?: string;
  enabled_providers?: number[];
  display_name?: string;
  cpu_request?: string;
  cpu_limit?: string;
  memory_request?: string;
  memory_limit?: string;
  vnc_resolution?: string;
  env_vars_set?: Record<string, string>;
  env_vars_unset?: string[];
}

export interface InstanceStats {
  cpu_usage_millicores: number;
  cpu_usage_percent: number;
  memory_usage_bytes: number;
  memory_limit_bytes: number;
}

export interface ProviderModelCost {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
}

export interface ProviderModel {
  id: string;
  name: string;
  reasoning?: boolean;
  input?: string[];
  contextWindow?: number;
  maxTokens?: number;
  cost?: ProviderModelCost;
}

export interface LLMProvider {
  id: number;
  key: string;
  instance_id?: number; // non-null = instance-specific provider
  provider: string; // catalog provider key, empty for custom
  name: string;
  base_url: string;
  api_type: string;
  masked_api_key?: string;
  models: ProviderModel[] | null;
  created_at: string;
  updated_at: string;
}

export interface InstanceConfig {
  config: string;
}

export interface InstanceConfigUpdate {
  config: string;
  restarted: boolean;
}
