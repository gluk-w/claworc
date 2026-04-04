import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { successToast, errorToast } from "@/utils/toast";
import {
  fetchAllBackups,
  fetchInstanceBackups,
  createBackup,
  deleteBackup,
  restoreBackup,
  fetchBackupSchedules,
  createBackupSchedule,
  updateBackupSchedule,
  deleteBackupSchedule,
} from "@/api/backups";
import type {
  Backup,
  BackupCreatePayload,
  BackupScheduleCreatePayload,
  BackupScheduleUpdatePayload,
} from "@/types/backup";

export function useAllBackups() {
  return useQuery({
    queryKey: ["backups"],
    queryFn: fetchAllBackups,
    refetchInterval: (query) => {
      const data = query.state.data as Backup[] | undefined;
      const hasRunning = data?.some((b) => b.status === "running");
      return hasRunning ? 3000 : false;
    },
  });
}

export function useInstanceBackups(instanceId: number) {
  return useQuery({
    queryKey: ["backups", instanceId],
    queryFn: () => fetchInstanceBackups(instanceId),
    refetchInterval: (query) => {
      const data = query.state.data as Backup[] | undefined;
      const hasRunning = data?.some((b) => b.status === "running");
      return hasRunning ? 3000 : false;
    },
  });
}

export function useCreateBackup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      instanceId,
      ...payload
    }: BackupCreatePayload & { instanceId: number }) =>
      createBackup(instanceId, payload),
    onSuccess: () => {
      successToast("Backup started");
      qc.invalidateQueries({ queryKey: ["backups"] });
    },
    onError: (err) => {
      errorToast("Failed to start backup", err);
    },
  });
}

export function useDeleteBackup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (backupId: number) => deleteBackup(backupId),
    onSuccess: () => {
      successToast("Backup deleted");
      qc.invalidateQueries({ queryKey: ["backups"] });
    },
    onError: (err) => {
      errorToast("Failed to delete backup", err);
    },
  });
}

export function useRestoreBackup() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      backupId,
      instanceId,
    }: {
      backupId: number;
      instanceId: number;
    }) => restoreBackup(backupId, { instance_id: instanceId }),
    onSuccess: () => {
      successToast("Restore started");
      qc.invalidateQueries({ queryKey: ["backups"] });
    },
    onError: (err) => {
      errorToast("Failed to start restore", err);
    },
  });
}

// Schedule hooks

export function useBackupSchedules() {
  return useQuery({
    queryKey: ["backup-schedules"],
    queryFn: fetchBackupSchedules,
  });
}

export function useCreateSchedule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (payload: BackupScheduleCreatePayload) =>
      createBackupSchedule(payload),
    onSuccess: () => {
      successToast("Schedule created");
      qc.invalidateQueries({ queryKey: ["backup-schedules"] });
    },
    onError: (err) => {
      errorToast("Failed to create schedule", err);
    },
  });
}

export function useUpdateSchedule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...payload
    }: BackupScheduleUpdatePayload & { id: number }) =>
      updateBackupSchedule(id, payload),
    onSuccess: () => {
      successToast("Schedule updated");
      qc.invalidateQueries({ queryKey: ["backup-schedules"] });
    },
    onError: (err) => {
      errorToast("Failed to update schedule", err);
    },
  });
}

export function useDeleteSchedule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => deleteBackupSchedule(id),
    onSuccess: () => {
      successToast("Schedule deleted");
      qc.invalidateQueries({ queryKey: ["backup-schedules"] });
    },
    onError: (err) => {
      errorToast("Failed to delete schedule", err);
    },
  });
}
