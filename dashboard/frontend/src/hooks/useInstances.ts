import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  fetchInstances,
  fetchInstance,
  createInstance,
  deleteInstance,
  startInstance,
  stopInstance,
  restartInstance,
  fetchInstanceConfig,
  updateInstanceConfig,
} from "@/api/instances";
import type { InstanceCreatePayload } from "@/types/instance";

export function useInstances() {
  return useQuery({
    queryKey: ["instances"],
    queryFn: fetchInstances,
    refetchInterval: 5000,
  });
}

export function useInstance(id: number) {
  return useQuery({
    queryKey: ["instances", id],
    queryFn: () => fetchInstance(id),
    refetchInterval: 5000,
  });
}

export function useCreateInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (payload: InstanceCreatePayload) => createInstance(payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["instances"] }),
  });
}

export function useDeleteInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => deleteInstance(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["instances"] }),
  });
}

export function useStartInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => startInstance(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["instances"] }),
  });
}

export function useStopInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => stopInstance(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["instances"] }),
  });
}

export function useRestartInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => restartInstance(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["instances"] }),
  });
}

export function useInstanceConfig(id: number) {
  return useQuery({
    queryKey: ["instances", id, "config"],
    queryFn: () => fetchInstanceConfig(id),
  });
}

export function useUpdateInstanceConfig() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, config }: { id: number; config: string }) =>
      updateInstanceConfig(id, config),
    onSuccess: (_data, variables) => {
      qc.invalidateQueries({ queryKey: ["instances", variables.id, "config"] });
      qc.invalidateQueries({ queryKey: ["instances"] });
    },
  });
}
