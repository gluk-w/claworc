import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  fetchProviders,
  createProvider,
  updateProvider,
  deleteProvider,
  fetchCatalogProviders,
  fetchCatalogProviderDetail,
  fetchUsageStats,
  resetUsageLogs,
} from "@/api/llm";
import type { UsageStatsResponse } from "@/api/llm";
import type { ProviderModel } from "@/types/instance";
import { successToast, errorToast } from "@/utils/toast";

export function useProviders() {
  return useQuery({
    queryKey: ["llm-providers"],
    queryFn: fetchProviders,
    staleTime: 30_000,
  });
}

export function useCreateProvider() {
  return useMutation({ mutationFn: createProvider });
}

export function useUpdateProvider() {
  return useMutation({
    mutationFn: ({ id, payload }: { id: number; payload: { name?: string; base_url?: string; api_type?: string; models?: ProviderModel[]; api_key?: string } }) =>
      updateProvider(id, payload),
  });
}

export function useCatalogProviders(source: string = "builtin", customUrl: string = "") {
  return useQuery({
    queryKey: ["catalog-providers", source, customUrl],
    queryFn: () => fetchCatalogProviders(source, customUrl),
    staleTime: 5 * 60 * 1000,
  });
}

export function useCatalogProviderDetail(key: string | null, source: string = "builtin", customUrl: string = "") {
  return useQuery({
    queryKey: ["catalog-provider", key, source, customUrl],
    queryFn: () => fetchCatalogProviderDetail(key!, source, customUrl),
    enabled: !!key && key !== "__custom__",
    staleTime: 5 * 60 * 1000,
  });
}

export function useUsageStats(params: {
  start_date?: string;
  end_date?: string;
  instance_id?: number;
  provider_id?: number;
}) {
  return useQuery<UsageStatsResponse>({
    queryKey: ["llm-usage-stats", params],
    queryFn: () => fetchUsageStats(params),
    staleTime: 10_000,
    refetchInterval: 10_000,
  });
}

export function useDeleteProvider() {
  return useMutation({ mutationFn: deleteProvider });
}

export function useResetUsageLogs() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: resetUsageLogs,
    onSuccess: () => {
      successToast("Usage logs cleared");
      queryClient.invalidateQueries({ queryKey: ["llm-usage-stats"] });
      queryClient.invalidateQueries({ queryKey: ["llm-usage-logs"] });
    },
    onError: (err) => errorToast("Failed to reset usage logs", err),
  });
}
