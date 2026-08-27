import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { successToast, errorToast } from "@common/utils/toast";
import {
  fetchConnections,
  fetchToolkits,
  initiateConnection,
  deleteConnection,
} from "@common/api/connections";
import type { Toolkit } from "@common/types/connection";

export function useConnections(instanceId: number) {
  return useQuery({
    queryKey: ["connections", instanceId],
    queryFn: () => fetchConnections(instanceId),
    enabled: !!instanceId,
  });
}

export function useComposioToolkits(enabled: boolean) {
  return useQuery({
    queryKey: ["composio-toolkits"],
    queryFn: fetchToolkits,
    enabled,
    staleTime: 5 * 60 * 1000,
  });
}

export function useInitiateConnection(instanceId: number) {
  return useMutation({
    mutationFn: ({
      toolkit,
      callbackUrl,
    }: {
      toolkit: Toolkit;
      callbackUrl: string;
    }) => initiateConnection(instanceId, toolkit, callbackUrl),
    onError: (error: unknown) =>
      errorToast("Failed to start connection", error),
  });
}

export function useDeleteConnection(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (connectionId: number) =>
      deleteConnection(instanceId, connectionId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["connections", instanceId] });
      successToast("Connection removed");
    },
    onError: (error: unknown) =>
      errorToast("Failed to remove connection", error),
  });
}
