import client from "./client";
import type {
  Backup,
  BackupCreatePayload,
  BackupRestorePayload,
  BackupSchedule,
  BackupScheduleCreatePayload,
  BackupScheduleUpdatePayload,
} from "@/types/backup";

export async function createBackup(
  instanceId: number,
  payload: BackupCreatePayload,
): Promise<{ id: number; message: string }> {
  const { data } = await client.post(
    `/instances/${instanceId}/backups`,
    payload,
  );
  return data;
}

export async function fetchInstanceBackups(
  instanceId: number,
): Promise<Backup[]> {
  const { data } = await client.get<Backup[]>(
    `/instances/${instanceId}/backups`,
  );
  return data;
}

export interface BackupsPage {
  backups: Backup[];
  total: number;
  limit: number;
  offset: number;
}

export async function fetchAllBackups(
  params: { limit: number; offset: number; instance?: string },
): Promise<BackupsPage> {
  const { data } = await client.get<BackupsPage>("/backups", { params });
  return data;
}

export async function fetchBackup(backupId: number): Promise<Backup> {
  const { data } = await client.get<Backup>(`/backups/${backupId}`);
  return data;
}

export async function deleteBackup(backupId: number): Promise<void> {
  await client.delete(`/backups/${backupId}`);
}

export async function cancelBackup(backupId: number): Promise<void> {
  await client.post(`/backups/${backupId}/cancel`);
}

export async function restoreBackup(
  backupId: number,
  payload: BackupRestorePayload,
): Promise<void> {
  await client.post(`/backups/${backupId}/restore`, payload);
}

export function getBackupDownloadUrl(backupId: number): string {
  return `${client.defaults.baseURL}/backups/${backupId}/download`;
}

// Schedule API

export async function fetchBackupSchedules(): Promise<BackupSchedule[]> {
  const { data } = await client.get<BackupSchedule[]>("/backup-schedules");
  return data;
}

export async function createBackupSchedule(
  payload: BackupScheduleCreatePayload,
): Promise<BackupSchedule> {
  const { data } = await client.post<BackupSchedule>(
    "/backup-schedules",
    payload,
  );
  return data;
}

export async function updateBackupSchedule(
  id: number,
  payload: BackupScheduleUpdatePayload,
): Promise<BackupSchedule> {
  const { data } = await client.put<BackupSchedule>(
    `/backup-schedules/${id}`,
    payload,
  );
  return data;
}

export async function deleteBackupSchedule(id: number): Promise<void> {
  await client.delete(`/backup-schedules/${id}`);
}
