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
