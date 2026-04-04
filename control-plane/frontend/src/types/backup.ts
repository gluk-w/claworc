export interface Backup {
  id: number;
  instance_id: number;
  instance_name: string;
  status: "running" | "completed" | "failed";
  file_path: string;
  paths: string;
  size_bytes: number;
  error_message?: string;
  note: string;
  created_at: string;
  completed_at?: string;
}

export interface BackupCreatePayload {
  paths?: string[];
  note?: string;
}

export interface BackupRestorePayload {
  instance_id: number;
}

export interface BackupSchedule {
  id: number;
  instance_ids: string;
  cron_expression: string;
  paths: string;
  last_run_at?: string;
  next_run_at?: string;
  created_at: string;
  updated_at: string;
}

export interface BackupScheduleCreatePayload {
  instance_ids: string;
  cron_expression: string;
  paths: string[];
}

export interface BackupScheduleUpdatePayload {
  instance_ids?: string;
  cron_expression?: string;
  paths?: string[];
}
