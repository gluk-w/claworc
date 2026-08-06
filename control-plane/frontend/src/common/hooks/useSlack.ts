import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetchInstanceSlack, updateInstanceSlack } from "@common/api/slack";
import type { InstanceSlackUpdatePayload } from "@common/types/slack";

export function useInstanceSlack(instanceId: number | undefined) {
  return useQuery({
    queryKey: ["instance-slack", instanceId],
    queryFn: () => fetchInstanceSlack(instanceId!),
    enabled: !!instanceId,
  });
}

export function useUpdateInstanceSlack(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (payload: InstanceSlackUpdatePayload) => updateInstanceSlack(instanceId, payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["instance-slack", instanceId] });
      // A token change triggers a container restart, which the instance
      // detail view surfaces via its status polling.
      qc.invalidateQueries({ queryKey: ["instances", instanceId] });
    },
  });
}
