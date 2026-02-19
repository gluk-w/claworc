export interface UsageSummary {
  group: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  estimated_cost_usd: string;
}

export interface InstanceUsageSummary {
  provider: string;
  model: string;
  requests: number;
  input_tokens: number;
  output_tokens: number;
  estimated_cost_usd: string;
}

export interface BudgetLimit {
  limit_micro: number;
  period_type: "daily" | "monthly";
  alert_threshold: number;
  hard_limit: boolean;
}

export interface RateLimit {
  provider: string;
  requests_per_minute: number;
  tokens_per_minute: number;
}

export interface InstanceLimits {
  budget: BudgetLimit | null;
  rate_limits: RateLimit[];
}

export interface ProxyStatus {
  proxy_enabled: boolean;
}
