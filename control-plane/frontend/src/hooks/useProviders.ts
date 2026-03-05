import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  fetchProviders,
  createProvider,
  updateProvider,
  deleteProvider,
  fetchCatalogProviders,
  fetchCatalogProviderDetail,
} from "@/api/llm";
import { successToast, errorToast } from "@/utils/toast";

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
    mutationFn: ({ id, payload }: { id: number; payload: { name?: string; base_url?: string } }) =>
      updateProvider(id, payload),
    onSuccess: () => {
      successToast("Provider updated");
      queryClient.invalidateQueries({ queryKey: ["llm-providers"] });
    },
    onError: (err) => errorToast("Failed to update provider", err),
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
