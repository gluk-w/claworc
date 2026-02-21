import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  fetchSSHStatus,
  fetchSSHEvents,
  testSSHConnection,
  reconnectSSH,
  fetchSSHFingerprint,
  fetchGlobalSSHStatus,
} from "@/api/ssh";

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

export function useSSHTest(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => testSSHConnection(instanceId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["ssh-status", instanceId] });
    },
  });
}

export function useSSHReconnect(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => reconnectSSH(instanceId),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["ssh-status", instanceId] });
      qc.invalidateQueries({ queryKey: ["ssh-events", instanceId] });
    },
  });
}

export function useSSHFingerprint(instanceId: number, enabled = true) {
  return useQuery({
    queryKey: ["ssh-fingerprint", instanceId],
    queryFn: () => fetchSSHFingerprint(instanceId),
    enabled,
    staleTime: 60_000,
  });
}

export function useGlobalSSHStatus() {
  return useQuery({
    queryKey: ["global-ssh-status"],
    queryFn: fetchGlobalSSHStatus,
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
  });
}
