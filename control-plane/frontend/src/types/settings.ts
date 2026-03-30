export interface Settings {
  brave_api_key: string;
  default_models: string[];
  default_container_image: string;
  default_vnc_resolution: string;
  default_cpu_request: string;
  default_cpu_limit: string;
  default_memory_request: string;
  default_memory_limit: string;
  default_storage_homebrew: string;
  default_storage_home: string;
  default_timezone: string;
  default_user_agent: string;
}

export interface SettingsUpdatePayload {
  default_models?: string[];
  brave_api_key?: string;
  default_container_image?: string;
  default_vnc_resolution?: string;
  default_cpu_request?: string;
  default_cpu_limit?: string;
  default_memory_request?: string;
  default_memory_limit?: string;
  default_storage_homebrew?: string;
  default_storage_home?: string;
  default_timezone?: string;
  default_user_agent?: string;
}

// Keep backward compat alias
export type SettingsUpdate = SettingsUpdatePayload;
