import client from "./client";
import type { Backup, BackupCreatePayload, BackupRestorePayload } from "@/types/backup";

export async function createBackup(instanceId: number, payload: BackupCreatePayload): Promise<{ id: number; message: string }> {
  const { data } = await client.post(`/instances/${instanceId}/backups`, payload);
  return data;
}

export async function fetchInstanceBackups(instanceId: number): Promise<Backup[]> {
  const { data } = await client.get<Backup[]>(`/instances/${instanceId}/backups`);
  return data;
}

export async function fetchAllBackups(): Promise<Backup[]> {
  const { data } = await client.get<Backup[]>("/backups");
  return data;
}

export async function fetchBackup(backupId: number): Promise<Backup> {
  const { data } = await client.get<Backup>(`/backups/${backupId}`);
  return data;
}

export async function deleteBackup(backupId: number): Promise<void> {
  await client.delete(`/backups/${backupId}`);
}

export async function restoreBackup(backupId: number, payload: BackupRestorePayload): Promise<void> {
  await client.post(`/backups/${backupId}/restore`, payload);
}

export function getBackupDownloadUrl(backupId: number): string {
  return `${client.defaults.baseURL}/backups/${backupId}/download`;
}
