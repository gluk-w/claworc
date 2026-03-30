export interface Backup {
  id: number;
  instance_id: number;
  instance_name: string;
  type: "full" | "incremental";
  parent_id: number | null;
  status: "running" | "completed" | "failed";
  file_path: string;
  size_bytes: number;
  marker_time: string;
  error_message?: string;
  note: string;
  created_at: string;
  completed_at?: string;
}

export interface BackupCreatePayload {
  type: "full" | "incremental";
  note?: string;
}

export interface BackupRestorePayload {
  instance_id: number;
}
