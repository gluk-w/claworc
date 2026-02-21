import { useQuery } from "@tanstack/react-query";
import { fetchSSHStatus, fetchSSHEvents } from "@/api/ssh";

export function useSSHStatus(instanceId: number, enabled: boolean = true) {
  return useQuery({
    queryKey: ["instances", instanceId, "ssh-status"],
    queryFn: () => fetchSSHStatus(instanceId),
    enabled,
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
  });
}

export function useSSHEvents(instanceId: number, enabled: boolean = true) {
  return useQuery({
    queryKey: ["instances", instanceId, "ssh-events"],
    queryFn: () => fetchSSHEvents(instanceId),
    enabled,
    refetchInterval: 15_000,
    refetchIntervalInBackground: false,
  });
}
