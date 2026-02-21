import { useQuery } from "@tanstack/react-query";
import { fetchSSHStatus, fetchSSHEvents } from "@/api/ssh";

export function useSSHStatus(instanceId: number, enabled = true) {
  return useQuery({
    queryKey: ["ssh-status", instanceId],
    queryFn: () => fetchSSHStatus(instanceId),
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
    enabled,
  });
}

export function useSSHEvents(instanceId: number, enabled = true) {
  return useQuery({
    queryKey: ["ssh-events", instanceId],
    queryFn: () => fetchSSHEvents(instanceId),
    refetchInterval: 15_000,
    refetchIntervalInBackground: false,
    enabled,
  });
}
