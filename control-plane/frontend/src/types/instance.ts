export interface InstanceModels {
  effective: string[];
  disabled_defaults: string[];
  extra: string[];
}

export interface ChannelAccount {
  name: string;
  enabled?: boolean;
  groups?: string[];
}

export interface ChannelInfo {
  type: string;
  accounts: ChannelAccount[];
}
export interface Instance {
  id: number;
  name: string;
  display_name: string;
  status: "creating" | "running" | "restarting" | "stopping" | "stopped" | "error";
  cpu_request: string;
  cpu_limit: string;
  memory_request: string;
  memory_limit: string;
  storage_homebrew: string;
  storage_clawd: string;
  storage_chrome: string;
  has_brave_override: boolean;
  api_key_overrides: string[];
  models: InstanceModels;
  default_model: string;
  container_image: string | null;
  has_image_override: boolean;
  vnc_resolution: string | null;
  has_resolution_override: boolean;
  control_url: string;
  gateway_token: string;
  channels: ChannelInfo[];
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
  storage_clawd?: string;
  storage_chrome?: string;
  brave_api_key?: string | null;
  api_keys?: Record<string, string>;
  models?: { disabled: string[]; extra: string[] };
  default_model?: string;
  container_image?: string | null;
  vnc_resolution?: string | null;
}

export interface InstanceUpdatePayload {
  api_keys?: Record<string, string | null>;
  brave_api_key?: string;
  models?: { disabled: string[]; extra: string[] };
  default_model?: string;
}

export interface InstanceConfig {
  config: string;
}

export interface InstanceConfigUpdate {
  config: string;
  restarted: boolean;
}
