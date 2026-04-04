# Backups

Claworc provides full backup and restore functionality for OpenClaw instances. Backups capture specified directories from the container filesystem as compressed tar archives stored on the control plane's local filesystem. The feature is admin-only.

## Overview

- **Full backups only** — every backup is a complete snapshot of the selected directories
- **Custom path selection** — choose which directories to back up, with convenient aliases
- **Scheduled backups** — cron-based scheduling for automatic backups of one, many, or all instances
- **Download & restore** — download backup archives or restore them to any running instance

## Path Aliases

When creating a backup or configuring a schedule, you can use path aliases instead of absolute paths:

| Alias | Resolves to | Description |
|-------|------------|-------------|
| `HOME` | `/home/claworc` | The agent's home directory (default) |
| `Homebrew` | `/home/linuxbrew/.linuxbrew` | Linuxbrew installation |
| `/` | `/` | Full container rootfs (excluding system dirs) |

Any other value is treated as a literal absolute path (e.g., `/etc/nginx`).

The default when no paths are specified is `["HOME"]`.

## Storage

Backup archives are stored at:

```
{CLAWORC_DATA_PATH}/backups/{instanceName}/{instanceName}-{backupID}-{timestamp}.tar.gz
```

For example: `/app/data/backups/bot-my-agent/bot-my-agent-5-20260403-140530.tar.gz`

The following system directories are always excluded from backups:
`/proc`, `/sys`, `/dev`, `/tmp`, `/run`, `/dev/shm`, `/var/cache/apt`, `/var/lib/apt/lists`, `/var/log/journal`

## Creating Backups

### Manual (UI)

1. Navigate to **Backups** in the sidebar
2. Click **Create Backup**
3. Select the target instance
4. Configure folders to back up (default: HOME)
5. Optionally add a note
6. Click **Create Backup**

The backup runs asynchronously. The status shows "running" with a spinner until complete.

### Manual (API)

```
POST /api/v1/instances/{id}/backups
Content-Type: application/json

{
  "paths": ["HOME", "/etc/nginx"],
  "note": "Before deployment"
}
```

Response: `202 Accepted` with `{"id": <backupID>, "message": "Backup started"}`

### Scheduled

1. Navigate to **Backups** in the sidebar
2. In the Schedules section, click **Schedule Backups**
3. Select instances (or "All Instances" for automatic coverage of new instances)
4. Enter a cron expression or pick a preset (Daily, Weekly, Monthly)
5. Configure folders to back up
6. Click **Create Schedule**

The schedule executor runs every minute, checking for due schedules and triggering backups.

## Cron Format

Standard 5-field cron format: `minute hour day-of-month month day-of-week`

| Preset | Expression | Description |
|--------|-----------|-------------|
| Daily at 2:00 AM | `0 2 * * *` | Every day at 2am UTC |
| Weekly Sunday at 2:00 AM | `0 2 * * 0` | Every Sunday at 2am UTC |
| Monthly 1st at 2:00 AM | `0 2 1 * *` | First day of month at 2am UTC |
| Every 6 hours | `0 */6 * * *` | At minute 0 past every 6th hour |

## Restoring Backups

### API

```
POST /api/v1/backups/{backupId}/restore
Content-Type: application/json

{
  "instance_id": 3
}
```

The restore process:
1. Validates the backup is completed and the file exists
2. Streams the archive to the target container in base64-encoded 48KB chunks
3. Writes to a temp file (`/tmp/_claworc_restore.tar.gz`)
4. Extracts with `tar xzf` into the container's root filesystem
5. Cleans up the temp file

Restore runs asynchronously. The target instance must be running.

## Downloading Backups

```
GET /api/v1/backups/{backupId}/download
```

Returns the `.tar.gz` file as a streaming download. Only available for completed backups.

## API Reference

### Backups

| Method | Endpoint | Description |
|--------|---------|-------------|
| POST | `/api/v1/instances/{id}/backups` | Create backup |
| GET | `/api/v1/instances/{id}/backups` | List instance backups |
| GET | `/api/v1/backups` | List all backups |
| GET | `/api/v1/backups/{backupId}` | Get backup detail |
| DELETE | `/api/v1/backups/{backupId}` | Delete backup (file + record) |
| POST | `/api/v1/backups/{backupId}/restore` | Restore to instance |
| GET | `/api/v1/backups/{backupId}/download` | Download archive |

### Backup Schedules

| Method | Endpoint | Description |
|--------|---------|-------------|
| POST | `/api/v1/backup-schedules` | Create schedule |
| GET | `/api/v1/backup-schedules` | List all schedules |
| PUT | `/api/v1/backup-schedules/{id}` | Update schedule |
| DELETE | `/api/v1/backup-schedules/{id}` | Delete schedule |

All endpoints require admin authentication.

## Database Models

### Backup

| Field | Type | Description |
|-------|------|-------------|
| ID | uint | Primary key |
| InstanceID | uint | Foreign key to instance |
| InstanceName | string | Instance name at time of backup |
| Status | string | `running`, `completed`, or `failed` |
| FilePath | string | Relative path to archive file |
| Paths | string | JSON array of paths that were backed up |
| SizeBytes | int64 | Compressed archive size |
| ErrorMessage | string | Error details if failed |
| Note | string | Optional user note |
| CreatedAt | time | When backup was started |
| CompletedAt | time | When backup finished (nullable) |

### BackupSchedule

| Field | Type | Description |
|-------|------|-------------|
| ID | uint | Primary key |
| InstanceIDs | string | JSON array of instance IDs, or `"ALL"` |
| CronExpression | string | 5-field cron expression |
| Paths | string | JSON array of path aliases/paths |
| Enabled | bool | Whether schedule is active |
| LastRunAt | time | Last execution time (nullable) |
| NextRunAt | time | Next scheduled execution (nullable) |
| CreatedAt | time | When schedule was created |
| UpdatedAt | time | Last modification time |

`NextRunAt` is automatically recalculated whenever the cron expression is changed (on create or update). The schedule executor uses this field to determine which schedules are due.
