# Shared Folders

## Overview

Shared Folders allow users to create named shared volumes and map them to multiple OpenClaw instances. Data written to a shared folder from one instance is immediately visible to all other mapped instances. This enables collaboration, shared datasets, and common workspace patterns.

Any authenticated user can create shared folders and map them to instances they have access to. Admins can see and manage all shared folders.

## Data Model

### SharedFolder

| Field         | Type     | Description                                        |
|---------------|----------|----------------------------------------------------|
| `ID`          | uint     | Primary key                                        |
| `Name`        | string   | User-visible folder name                           |
| `MountPath`   | string   | Container mount path (same on all mapped instances) |
| `OwnerID`     | uint     | User who created the folder                        |
| `InstanceIDs` | string   | JSON array of instance IDs mapped to this folder   |
| `CreatedAt`   | datetime | Creation timestamp                                 |
| `UpdatedAt`   | datetime | Last update timestamp                              |

The `InstanceIDs` field is a JSON text column storing `[]uint`, following the same pattern as `Instance.EnabledProviders`.

## API Endpoints

All endpoints require authentication. In the protected route group (not admin-only).

| Method   | Path                    | Description                          |
|----------|-------------------------|--------------------------------------|
| `GET`    | `/shared-folders`       | List shared folders (own or all for admin) |
| `POST`   | `/shared-folders`       | Create a new shared folder           |
| `GET`    | `/shared-folders/{id}`  | Get folder details                   |
| `PUT`    | `/shared-folders/{id}`  | Update name, mount path, instance mappings |
| `DELETE` | `/shared-folders/{id}`  | Delete a shared folder               |

### Create Request

```json
{ "name": "Shared Data", "mount_path": "/shared/data" }
```

### Update Request

All fields are optional:

```json
{
  "name": "New Name",
  "mount_path": "/shared/new-path",
  "instance_ids": [1, 3, 5]
}
```

### Response Format

```json
{
  "id": 1,
  "name": "Shared Data",
  "mount_path": "/shared/data",
  "owner_id": 1,
  "instance_ids": [1, 3],
  "created_at": "2026-04-03T10:00:00Z"
}
```

## Volume Lifecycle

### Creation

Volumes are created by the orchestrator when an instance starts or restarts with shared folder mappings:

- **Docker**: Named volume `claworc-shared-<folder_id>` with label `type=shared-folder`
- **Kubernetes**: PersistentVolumeClaim `shared-folder-<folder_id>` with `ReadWriteMany` access mode

### Mounting

When an instance is created, restarted, or has its image updated, the orchestrator reads shared folder mappings from the database and adds the corresponding volume mounts to the container/pod spec.

The mount path is the same for all instances mapped to a given shared folder.

### Automatic Restart on Mapping Changes

When instance mappings or the mount path are changed (via `UpdateSharedFolder` or `DeleteSharedFolder`), all affected running instances are **automatically restarted** in the background. This includes:

- Instances that were added to the folder (gain the mount)
- Instances that were removed from the folder (lose the mount)
- All mapped instances when the mount path changes

The restart recreates the container (Docker: stop + remove + create; K8s: deployment update) with the current set of mounts. Stopped instances are skipped and will pick up changes on their next start.

The helper `restartInstanceAsync` (in `instances.go`) handles SSH tunnel teardown, status updates, and the async restart. `buildCreateParams` constructs the full `CreateParams` from a database `Instance`.

### Deletion

When a shared folder is deleted from the database:
- The database record is removed
- All mapped running instances are automatically restarted (mount is removed)
- The backing volume is **not** automatically deleted (safety measure)

Orphaned volumes can be cleaned up manually:
- Docker: `docker volume ls --filter label=type=shared-folder`
- K8s: `kubectl get pvc -l type=shared-folder`

## Access Control

| Action                  | Owner | Admin |
|------------------------|-------|-------|
| Create shared folder   | Yes   | Yes   |
| List own shared folders | Yes   | Yes (sees all) |
| View folder details    | Yes   | Yes   |
| Update folder          | Yes   | Yes   |
| Delete folder          | Yes   | Yes   |
| Map to instance        | If has instance access | Yes |

The `CanAccessInstance` middleware check ensures users can only map shared folders to instances they are assigned to.

## Mount Path Restrictions

The mount path must:
- Start with `/`
- Not be or start with `/home/claworc` (instance home directory)
- Not be or start with `/home/linuxbrew` (Homebrew installation)
- Not be or start with `/dev/shm` (shared memory)

Recommended paths: `/shared/<name>`, `/data/<name>`

## Kubernetes Considerations

Shared folder PVCs use `ReadWriteMany` (RWX) access mode, which requires a storage class that supports it (e.g., NFS, CephFS, AWS EFS). If the cluster only supports `ReadWriteOnce`, two instances cannot mount the same PVC simultaneously.

## Frontend

The "Shared Folders" page is accessible to all authenticated users via the sidebar (FolderOpen icon). It provides:

- Table listing all shared folders with name, mount path, instance count, and creation date
- Create/Edit modal with name, mount path, and instance multi-select (`MultiSelect` component)
- Delete confirmation dialog
- Amber notice (edit mode only) informing that affected instances will be automatically restarted
