import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchInstanceDiscord, updateInstanceDiscord } from "@common/api/discord";
import type { InstanceDiscordUpdatePayload } from "@common/types/discord";

export function useInstanceDiscord(instanceId: number | undefined) {
  return useQuery({
    queryKey: ["instance-discord", instanceId],
    queryFn: () => fetchInstanceDiscord(instanceId!),
    enabled: !!instanceId,
  });
}

export function useUpdateInstanceDiscord(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (payload: InstanceDiscordUpdatePayload) =>
      updateInstanceDiscord(instanceId, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["instance-discord", instanceId] });
      // A token change triggers a container restart, which the instance
      // detail view surfaces via its status polling.
      qc.invalidateQueries({ queryKey: ["instances", instanceId] });
    },
  });
}
