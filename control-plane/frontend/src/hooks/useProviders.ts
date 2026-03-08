import { createElement } from "react";
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
import toast from "react-hot-toast";
import AppToast from "@/components/AppToast";

export function useProviders() {
  return useQuery({
    queryKey: ["llm-providers"],
    queryFn: fetchProviders,
    staleTime: 30_000,
  });
}

export function useCreateProvider() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: createProvider,
    onSuccess: () => {
      successToast("Provider created");
      queryClient.invalidateQueries({ queryKey: ["llm-providers"] });
    },
    onError: (err) => errorToast("Failed to create provider", err),
  });
}

export function useUpdateProvider() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, payload }: { id: number; payload: { name?: string; base_url?: string; api_type?: string; models?: ProviderModel[] } }) =>
      updateProvider(id, payload),
    onMutate: ({ id }) => {
      const toastId = `provider-update-${id}`;
      toast.custom(
        createElement(AppToast, {
          title: "Updating provider",
          description: "Pushing config to instances...",
          status: "loading",
          toastId,
        }),
        { id: toastId, duration: Infinity },
      );
      return { toastId };
    },
    onSuccess: (_, __, context) => {
      const toastId = context!.toastId;
      toast.custom(
        createElement(AppToast, {
          title: "Provider updated",
          status: "success",
          toastId,
        }),
        { id: toastId, duration: 4000 },
      );
      queryClient.invalidateQueries({ queryKey: ["llm-providers"] });
    },
    onError: (err, { id }, context) => {
      const toastId = context?.toastId ?? `provider-update-${id}`;
      const detail =
        (err as any)?.response?.data?.detail ??
        (err instanceof Error ? err.message : undefined);
      toast.custom(
        createElement(AppToast, {
          title: "Failed to update provider",
          description: detail,
          status: "error",
          toastId,
        }),
        { id: toastId, duration: 8000 },
      );
    },
  });
}

export function useCatalogProviders() {
  return useQuery({
    queryKey: ["catalog-providers"],
    queryFn: fetchCatalogProviders,
    staleTime: 5 * 60 * 1000,
  });
}

export function useCatalogProviderDetail(key: string | null) {
  return useQuery({
    queryKey: ["catalog-provider", key],
    queryFn: () => fetchCatalogProviderDetail(key!),
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
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: deleteProvider,
    onSuccess: () => {
      successToast("Provider deleted");
      queryClient.invalidateQueries({ queryKey: ["llm-providers"] });
    },
    onError: (err) => errorToast("Failed to delete provider", err),
  });
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
