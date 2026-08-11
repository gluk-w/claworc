import { useQuery } from "@tanstack/react-query";
import { getChannelHealth } from "@common/api/channels";

export function useChannelHealth(instanceId: number | undefined) {
  return useQuery({
    queryKey: ["instance-channel-health", instanceId],
    queryFn: () => getChannelHealth(instanceId!),
    enabled: !!instanceId,
    refetchInterval: 15000,
    refetchIntervalInBackground: false,
    retry: false,
  });
}
