export interface RestartingInstance {
  id: number;
  name: string;
  display_name: string;
}

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
  /** Global env vars applied to every instance. Values are masked (e.g. "****abcd"). */
  default_env_vars: Record<string, string>;
  /**
   * Only populated on the PUT response when env vars changed: the set of
   * running instances the backend kicked a restart on to apply the change.
   */
  restarting_instances?: RestartingInstance[];
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
  /** Env vars to create or overwrite (plaintext values). */
  env_vars_set?: Record<string, string>;
  /** Env var names to remove. */
  env_vars_unset?: string[];
}

// Keep backward compat alias
export type SettingsUpdate = SettingsUpdatePayload;
