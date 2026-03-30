import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { successToast, errorToast } from "@/utils/toast";
import {
  fetchInstanceBackups,
  createBackup,
  deleteBackup,
  restoreBackup,
} from "@/api/backups";
import type { Backup } from "@/types/backup";

export function useInstanceBackups(instanceId: number) {
  const query = useQuery({
    queryKey: ["backups", instanceId],
    queryFn: () => fetchInstanceBackups(instanceId),
    refetchInterval: (query) => {
      const data = query.state.data as Backup[] | undefined;
      const hasRunning = data?.some((b) => b.status === "running");
      return hasRunning ? 3000 : false;
    },
  });
  return query;
}

export function useCreateBackup(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (payload: { type: "full" | "incremental"; note?: string }) =>
      createBackup(instanceId, payload),
    onSuccess: () => {
      successToast("Backup started");
      qc.invalidateQueries({ queryKey: ["backups", instanceId] });
    },
    onError: (err) => {
      errorToast("Failed to start backup", err);
    },
  });
}

export function useDeleteBackup(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (backupId: number) => deleteBackup(backupId),
    onSuccess: () => {
      successToast("Backup deleted");
      qc.invalidateQueries({ queryKey: ["backups", instanceId] });
    },
    onError: (err) => {
      errorToast("Failed to delete backup", err);
    },
  });
}

export function useRestoreBackup(instanceId: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (backupId: number) =>
      restoreBackup(backupId, { instance_id: instanceId }),
    onSuccess: () => {
      successToast("Restore started");
      qc.invalidateQueries({ queryKey: ["backups", instanceId] });
    },
    onError: (err) => {
      errorToast("Failed to start restore", err);
    },
  });
}
