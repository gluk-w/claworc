export interface Settings {
  anthropic_api_key: string;
  openai_api_key: string;
  brave_api_key: string;
  default_cpu_request: string;
  default_cpu_limit: string;
  default_memory_request: string;
  default_memory_limit: string;
  default_storage_clawdbot: string;
  default_storage_homebrew: string;
  default_storage_clawd: string;
  default_storage_chrome: string;
}

export type SettingsUpdate = Partial<Settings>;
