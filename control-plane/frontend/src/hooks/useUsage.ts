import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import toast from "react-hot-toast";
import {
  fetchUsage,
  fetchInstanceUsage,
  fetchInstanceLimits,
  setInstanceBudget,
  setInstanceRateLimit,
  fetchProxyStatus,
} from "@/api/usage";
import type { BudgetLimit, RateLimit } from "@/types/usage";

export function useProxyStatus() {
  return useQuery({
    queryKey: ["proxy-status"],
    queryFn: fetchProxyStatus,
    staleTime: 60_000,
  });
}

export function useUsage(params?: {
  since?: string;
  until?: string;
  group_by?: string;
}) {
  return useQuery({
    queryKey: ["usage", params],
    queryFn: () => fetchUsage(params),
    refetchInterval: 30_000,
  });
}

export function useInstanceUsage(
  id: number,
  params?: { since?: string; until?: string },
) {
  return useQuery({
    queryKey: ["instance-usage", id, params],
    queryFn: () => fetchInstanceUsage(id, params),
    refetchInterval: 30_000,
  });
}

export function useInstanceLimits(id: number) {
  return useQuery({
    queryKey: ["instance-limits", id],
    queryFn: () => fetchInstanceLimits(id),
  });
}

export function useSetBudget() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, budget }: { id: number; budget: BudgetLimit }) =>
      setInstanceBudget(id, budget),
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: ["instance-limits", id] });
      toast.success("Budget updated");
    },
    onError: () => toast.error("Failed to update budget"),
  });
}

export function useSetRateLimit() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      rateLimits,
    }: {
      id: number;
      rateLimits: RateLimit[];
    }) => setInstanceRateLimit(id, rateLimits),
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: ["instance-limits", id] });
      toast.success("Rate limit updated");
    },
    onError: () => toast.error("Failed to update rate limit"),
  });
}
