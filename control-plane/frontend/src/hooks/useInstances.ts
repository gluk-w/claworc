import { useEffect, useRef, createElement } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import toast from "react-hot-toast";
import { successToast, errorToast, infoToast } from "@/utils/toast";
import AppToast from "@/components/AppToast";
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
    onSuccess: (_data, { displayName }) => {
      infoToast("Cloning instance", displayName);
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
    onSuccess: (_data, { displayName }) => {
      infoToast("Restarting instance", displayName);
      qc.invalidateQueries({ queryKey: ["instances"] });
    },
    onError: (error: any, { displayName }) => {
      errorToast(`Failed to restart ${displayName}`, error);
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
        successToast("Instance restarted", inst.display_name);
      }
      if (prev.get(inst.id) === "stopping" && inst.status === "stopped") {
        successToast("Instance stopped", inst.display_name);
      }
      prev.set(inst.id, inst.status);
    }
  }, [instances]);
}

/** Show a persistent toast tracking creation progress for instances in "creating" status. */
export function useCreationToast(instances: Instance[] | undefined) {
  const activeRef = useRef<Map<number, string>>(new Map());

  useEffect(() => {
    if (!instances) return;
    const active = activeRef.current;
    const currentIds = new Set<number>();

    for (const inst of instances) {
      if (inst.status === "creating") {
        currentIds.add(inst.id);
        const toastId = `creation-${inst.id}`;
        active.set(inst.id, toastId);
        toast.custom(
          createElement(AppToast, {
            title: inst.display_name,
            description: inst.status_message || "Starting...",
            status: "loading",
            toastId,
          }),
          { id: toastId, duration: Infinity },
        );
      } else if (active.has(inst.id)) {
        // Transitioned away from "creating" — show final state briefly
        const toastId = active.get(inst.id)!;
        const isSuccess = inst.status === "running";
        toast.custom(
          createElement(AppToast, {
            title: inst.display_name,
            description: isSuccess ? "Instance ready" : (inst.status_message || "Creation failed"),
            status: isSuccess ? "success" : "error",
            toastId,
          }),
          { id: toastId, duration: isSuccess ? 4000 : 8000 },
        );
        active.delete(inst.id);
      }
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
