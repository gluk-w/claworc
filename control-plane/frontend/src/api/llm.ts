import client from "./client";
import type { LLMProvider, ProviderModel } from "@/types/instance";

// ---------------------------------------------------------------------------
// External provider catalog (https://claworc.com/providers/)
// ---------------------------------------------------------------------------

export interface CatalogProviderSummary {
  key: string;
  label: string;
  icon_key: string | null;
  api_format: string;
  model_count: number;
  has_reasoning: boolean;
  has_vision: boolean;
}

export interface CatalogProviderDetail {
  key: string;
  label: string;
  icon_key: string | null;
  api_format: string;
  models: {
    model_id: string;
    model_name: string;
    slug: string;
    api_format: string;
    base_url: string | null;
    reasoning: boolean;
    vision: boolean;
    context_window: number | null;
    max_tokens: number | null;
    tag?: string | null;
    description?: string | null;
  }[];
}

export async function fetchCatalogProviders(): Promise<CatalogProviderSummary[]> {
  const { data } = await client.get<CatalogProviderSummary[]>("/llm/catalog");
  return data;
}

export async function fetchCatalogProviderDetail(key: string): Promise<CatalogProviderDetail> {
  const { data } = await client.get<CatalogProviderDetail>(`/llm/catalog/${encodeURIComponent(key)}`);
  return data;
}

// ---------------------------------------------------------------------------
// Internal provider management
// ---------------------------------------------------------------------------

export async function fetchProviders(): Promise<LLMProvider[]> {
  const { data } = await client.get<LLMProvider[]>("/llm/providers");
  return data;
}

export async function createProvider(payload: {
  key: string;
  provider: string;
  name: string;
  base_url: string;
  api_type?: string;
  models?: ProviderModel[];
}): Promise<LLMProvider> {
  const { data } = await client.post<LLMProvider>("/llm/providers", payload);
  return data;
}

export async function updateProvider(
  id: number,
  payload: { name?: string; base_url?: string; api_type?: string; models?: ProviderModel[] },
): Promise<LLMProvider> {
  const { data } = await client.put<LLMProvider>(`/llm/providers/${id}`, payload);
  return data;
}

export async function deleteProvider(id: number): Promise<void> {
  await client.delete(`/llm/providers/${id}`);
}

export async function syncAllProviders(): Promise<LLMProvider[]> {
  const { data } = await client.post<LLMProvider[]>("/llm/providers/sync");
  return data;
}

// ---------------------------------------------------------------------------
// Usage stats
// ---------------------------------------------------------------------------

export interface InstanceUsageStat {
  instance_id: number;
  instance_name: string;
  instance_display_name: string;
  total_requests: number;
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
  cost_usd: number;
}

export interface ProviderUsageStat {
  provider_id: number;
  provider_key: string;
  provider_name: string;
  total_requests: number;
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
  cost_usd: number;
}

export interface ModelUsageStat {
  model_id: string;
  provider_id: number;
  provider_key: string;
  total_requests: number;
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
  cost_usd: number;
}

export interface UsageTimePoint {
  date: string;
  total_requests: number;
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
  cost_usd: number;
}

export interface UsageStatsResponse {
  by_instance: InstanceUsageStat[];
  by_provider: ProviderUsageStat[];
  by_model: ModelUsageStat[];
  time_series: UsageTimePoint[];
  total: {
    total_requests: number;
    input_tokens: number;
    cached_input_tokens: number;
    output_tokens: number;
    cost_usd: number;
  };
  instances: { id: number; name: string; display_name: string }[];
  providers: { id: number; key: string; name: string }[];
  granularity: "minute" | "hour" | "day";
}

export async function fetchUsageStats(params: {
  start_date?: string;
  end_date?: string;
  instance_id?: number;
  provider_id?: number;
}): Promise<UsageStatsResponse> {
  const { data } = await client.get<UsageStatsResponse>("/llm/usage/stats", { params });
  return data;
}

export async function resetUsageLogs(): Promise<void> {
  await client.delete("/llm/usage");
}
