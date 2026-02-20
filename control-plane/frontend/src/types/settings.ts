/**
 * Settings returned by GET /api/v1/settings.
 *
 * All API key values are masked by the backend (e.g. "****abcd") and should
 * never contain plaintext secrets.
 */
export interface Settings {
  /** Brave Search API key (masked). */
  brave_api_key: string;
  /** LLM provider API keys keyed by provider env-var name (masked). */
  api_keys: Record<string, string>;
  /** Custom base URLs keyed by the same provider env-var name. */
  base_urls: Record<string, string>;
  /** Ordered list of default LLM model identifiers. */
  default_models: string[];
  /** Docker/OCI image used for new bot instances. */
  default_container_image: string;
  /** VNC resolution string, e.g. "1920x1080". */
  default_vnc_resolution: string;
  /** Kubernetes CPU request, e.g. "500m". */
  default_cpu_request: string;
  /** Kubernetes CPU limit, e.g. "2000m". */
  default_cpu_limit: string;
  /** Kubernetes memory request, e.g. "1Gi". */
  default_memory_request: string;
  /** Kubernetes memory limit, e.g. "4Gi". */
  default_memory_limit: string;
  /** PVC size for the Homebrew volume, e.g. "10Gi". */
  default_storage_homebrew: string;
  /** PVC size for the Clawd volume, e.g. "5Gi". */
  default_storage_clawd: string;
  /** PVC size for the Chrome profile volume, e.g. "5Gi". */
  default_storage_chrome: string;
}

/**
 * Payload accepted by PUT /api/v1/settings.
 *
 * All fields are optional — only the provided fields are updated.
 * API keys sent here must be plaintext; the backend encrypts them at rest.
 */
export interface SettingsUpdatePayload {
  /** Replace the default models list. */
  default_models?: string[];
  /** Upsert LLM provider API keys (plaintext). */
  api_keys?: Record<string, string>;
  /** Custom base URLs per provider (empty string removes the override). */
  base_urls?: Record<string, string>;
  /** Provider key names to delete (also removes associated base URLs). */
  delete_api_keys?: string[];
  /** Brave Search API key (plaintext). */
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

// ---------------------------------------------------------------------------
// Provider key testing types (POST /api/v1/settings/test-provider-key)
// ---------------------------------------------------------------------------

/** Request body for testing an LLM provider API key. */
export interface TestProviderKeyRequest {
  /** Provider identifier, e.g. "openai", "anthropic". */
  provider: string;
  /** Plaintext API key to test. */
  api_key: string;
  /** Optional custom base URL to use instead of the provider default. */
  base_url?: string;
}

/** Response from the provider key test endpoint. */
export interface TestProviderKeyResponse {
  /** Whether the key was accepted by the provider. */
  success: boolean;
  /** Human-readable summary of the result. */
  message: string;
  /** Additional details on failure (e.g. error description). */
  details?: string;
}

// ---------------------------------------------------------------------------
// Provider analytics types (GET /api/v1/analytics/providers)
// ---------------------------------------------------------------------------

/** Aggregated usage metrics for a single LLM provider over a time window. */
export interface ProviderStats {
  /** Provider identifier, e.g. "openai". */
  provider: string;
  /** Total API requests recorded in the period. */
  total_requests: number;
  /** Number of requests that resulted in errors. */
  error_count: number;
  /** Ratio of errors to total requests (0–1). */
  error_rate: number;
  /** Average response latency in milliseconds. */
  avg_latency: number;
  /** Most recent error message, if any. */
  last_error?: string;
  /** ISO 8601 timestamp of the most recent error. */
  last_error_at?: string;
}

/** Response from the provider analytics endpoint. */
export interface ProviderAnalyticsResponse {
  /** Usage stats keyed by provider identifier. */
  providers: Record<string, ProviderStats>;
  /** Number of days the analytics window covers. */
  period_days: number;
  /** ISO 8601 start of the analytics window. */
  since: string;
}

/** Visual health indicator derived from provider analytics. */
export type ProviderHealthStatus = "healthy" | "warning" | "error" | "unknown";
