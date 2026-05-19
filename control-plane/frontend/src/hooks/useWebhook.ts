import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  createInstanceWebhookKey,
  deleteInstanceWebhookKey,
  fetchInstanceWebhook,
  fetchInstanceWebhookLogs,
  regenerateInstanceWebhookKey,
  updateInstanceWebhookKey,
} from "@/api/webhooks";

export function useInstanceWebhook(instanceId: number | undefined) {
  return useQuery({
    queryKey: ["instance-webhook", instanceId],
    queryFn: () => fetchInstanceWebhook(instanceId!),
    enabled: !!instanceId,
  });
}

export function useInstanceWebhookLogs(instanceId: number | undefined) {
  return useQuery({
    queryKey: ["instance-webhook-logs", instanceId],
    queryFn: () => fetchInstanceWebhookLogs(instanceId!, { limit: 100 }),
    enabled: !!instanceId,
    refetchInterval: 10000,
  });
}

export function useCreateInstanceWebhookKey(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (payload: { label?: string; is_private?: boolean }) =>
      createInstanceWebhookKey(instanceId, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["instance-webhook", instanceId] });
    },
  });
}

export function useUpdateInstanceWebhookKey(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ keyId, payload }: { keyId: number; payload: { label?: string; is_private?: boolean } }) =>
      updateInstanceWebhookKey(instanceId, keyId, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["instance-webhook", instanceId] });
    },
  });
}

export function useRegenerateInstanceWebhookKey(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ keyId, payload }: { keyId: number; payload?: { label?: string; is_private?: boolean } }) =>
      regenerateInstanceWebhookKey(instanceId, keyId, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["instance-webhook", instanceId] });
    },
  });
}

export function useDeleteInstanceWebhookKey(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (keyId: number) => deleteInstanceWebhookKey(instanceId, keyId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["instance-webhook", instanceId] });
    },
  });
}
