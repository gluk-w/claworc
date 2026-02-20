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
