import { useEffect, useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import toast from "react-hot-toast";
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
      toast.success("Instance updated");
    },
    onError: (error: any) => {
      toast.error(error.response?.data?.detail || "Failed to update instance");
    },
  });
}

export function useCloneInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id }: { id: number; displayName: string }) =>
      cloneInstance(id),
    onSuccess: (_data, { displayName }) => {
      toast(`Cloning ${displayName}`);
      qc.invalidateQueries({ queryKey: ["instances"] });
    },
    onError: (error: any, { displayName }) => {
      toast.error(
        error.response?.data?.detail || `Failed to clone ${displayName}`,
      );
    },
  });
}

export function useDeleteInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => deleteInstance(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["instances"] }),
    onError: (error: any) => {
      toast.error(error.response?.data?.detail || "Failed to delete instance");
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
      toast(`Stopping ${displayName}`);
      qc.invalidateQueries({ queryKey: ["instances"] });
    },
    onError: (error: any, { displayName }) => {
      toast.error(
        error.response?.data?.detail || `Failed to stop ${displayName}`,
      );
    },
  });
}

export function useRestartInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id }: { id: number; displayName: string }) =>
      restartInstance(id),
    onSuccess: (_data, { displayName }) => {
      toast(`Restarting ${displayName}`);
      qc.invalidateQueries({ queryKey: ["instances"] });
    },
    onError: (error: any, { displayName }) => {
      toast.error(
        error.response?.data?.detail || `Failed to restart ${displayName}`,
      );
    },
  });
}

/** Show a "Restarted <name>" toast when any instance transitions from "restarting" → "running". */
export function useRestartedToast(instances: Instance[] | undefined) {
  const prevRef = useRef<Map<number, string>>(new Map());

  useEffect(() => {
    if (!instances) return;
    const prev = prevRef.current;
    for (const inst of instances) {
      if (prev.get(inst.id) === "restarting" && inst.status === "running") {
        toast.success(`Restarted ${inst.display_name}`);
      }
      if (prev.get(inst.id) === "stopping" && inst.status === "stopped") {
        toast.success(`Stopped ${inst.display_name}`);
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
  });
}

export function useReorderInstances() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (orderedIds: number[]) => reorderInstances(orderedIds),
    onError: () => {
      qc.invalidateQueries({ queryKey: ["instances"] });
      toast.error("Failed to reorder instances");
    },
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
