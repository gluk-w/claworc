export interface Instance {
  id: number;
  name: string;
  display_name: string;
  nodeport_chrome: number;
  nodeport_terminal: number;
  status: "creating" | "running" | "stopped" | "error";
  cpu_request: string;
  cpu_limit: string;
  memory_request: string;
  memory_limit: string;
  storage_clawdbot: string;
  storage_homebrew: string;
  storage_clawd: string;
  storage_chrome: string;
  has_anthropic_override: boolean;
  has_openai_override: boolean;
  has_brave_override: boolean;
  created_at: string;
  updated_at: string;
}

export interface InstanceDetail extends Instance {
  vnc_chrome_url: string;
  vnc_terminal_url: string;
}

export interface InstanceCreatePayload {
  display_name: string;
  cpu_request?: string;
  cpu_limit?: string;
  memory_request?: string;
  memory_limit?: string;
  storage_clawdbot?: string;
  storage_homebrew?: string;
  storage_clawd?: string;
  storage_chrome?: string;
  anthropic_api_key?: string | null;
  openai_api_key?: string | null;
  brave_api_key?: string | null;
  clawdbot_config?: string | null;
}

export interface InstanceConfig {
  config: string;
}

export interface InstanceConfigUpdate {
  config: string;
  restarted: boolean;
}
