import { useEffect, useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { successToast, errorToast, infoToast } from "@/utils/toast";
import {
  fetchInstances,
  fetchInstance,
  createInstance,
  updateInstance,
  deleteInstance,
  startInstance,
  stopInstance,
  restartInstance,
  cloneInstance,
  fetchInstanceConfig,
  updateInstanceConfig,
  reorderInstances,
  fetchInstanceStats,
  updateInstanceImage,
} from "@/api/instances";
import type { Instance, InstanceCreatePayload, InstanceUpdatePayload } from "@/types/instance";

export function useInstances() {
  return useQuery({
    queryKey: ["instances"],
    queryFn: fetchInstances,
    refetchInterval: 5000,
    refetchIntervalInBackground: false,
  });
}

export function useInstance(id: number) {
  return useQuery({
    queryKey: ["instances", id],
    queryFn: () => fetchInstance(id),
    refetchInterval: 5000,
    refetchIntervalInBackground: false,
  });
}

export function useCreateInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (payload: InstanceCreatePayload) => createInstance(payload),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["instances"] }),
  });
}

export function useUpdateInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, payload }: { id: number; payload: InstanceUpdatePayload }) =>
      updateInstance(id, payload),
    onSuccess: (_data, { id }) => {
      qc.invalidateQueries({ queryKey: ["instances", id] });
      qc.invalidateQueries({ queryKey: ["instances"] });
      successToast("Instance updated");
    },
    onError: (error: any) => {
      errorToast("Failed to update instance", error);
    },
  });
}

export function useCloneInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id }: { id: number; displayName: string }) =>
      cloneInstance(id),
    // No success toast here — the TaskManager-driven `instance.clone` task
    // surfaces its own loading→success/error toast via TaskToasts. Two toasts
    // for one user action is noise.
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["instances"] });
    },
    onError: (error: any, { displayName }) => {
      errorToast(`Failed to clone ${displayName}`, error);
    },
  });
}

export function useDeleteInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => deleteInstance(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["instances"] }),
    onError: (error: any) => {
      errorToast("Failed to delete instance", error);
    },
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
    mutationFn: ({ id }: { id: number; displayName: string }) =>
      stopInstance(id),
    onSuccess: (_data, { displayName }) => {
      infoToast("Stopping instance", displayName);
      qc.invalidateQueries({ queryKey: ["instances"] });
    },
    onError: (error: any, { displayName }) => {
      errorToast(`Failed to stop ${displayName}`, error);
    },
  });
}

export function useRestartInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id }: { id: number; displayName: string }) =>
      restartInstance(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["instances"] });
    },
    onError: (error: any, { displayName }) => {
      errorToast(`Failed to restart ${displayName}`, error);
    },
  });
}

export function useUpdateInstanceImage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => updateInstanceImage(id),
    onSuccess: (_data, id) => {
      qc.invalidateQueries({ queryKey: ["instances", id] });
      qc.invalidateQueries({ queryKey: ["instances"] });
    },
    onError: (error: any) => {
      errorToast("Failed to update image", error);
    },
  });
}

/** Show a "Stopped <name>" toast when any instance transitions from "stopping" → "stopped".
 * Restart transitions are surfaced by TaskToasts (task type `instance.restart`),
 * so this hook does not duplicate them. */
export function useRestartedToast(instances: Instance[] | undefined) {
  const prevRef = useRef<Map<number, string>>(new Map());

  useEffect(() => {
    if (!instances) return;
    const prev = prevRef.current;
    for (const inst of instances) {
      if (prev.get(inst.id) === "stopping" && inst.status === "stopped") {
        successToast("Instance stopped", inst.display_name);
      }
      prev.set(inst.id, inst.status);
    }
  }, [instances]);
}

export function useInstanceConfig(id: number, enabled: boolean = true) {
  return useQuery({
    queryKey: ["instances", id, "config"],
    queryFn: () => fetchInstanceConfig(id),
    enabled,
    retry: 3,
    retryDelay: (attempt) => Math.min(1000 * 2 ** attempt, 4000),
  });
}

export function useReorderInstances() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (orderedIds: number[]) => reorderInstances(orderedIds),
    onError: () => {
      qc.invalidateQueries({ queryKey: ["instances"] });
      errorToast("Failed to reorder instances");
    },
  });
}

export function useInstanceStats(id: number, enabled: boolean = true) {
  return useQuery({
    queryKey: ["instance-stats", id],
    queryFn: () => fetchInstanceStats(id),
    refetchInterval: 10_000,
    refetchIntervalInBackground: false,
    enabled,
    retry: false,
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
