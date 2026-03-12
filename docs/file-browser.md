# File Browser — Internal Specification

## Architecture

```
Frontend (SVAR Filemanager)
        │  intercept / on events
        ▼
  HTTP API (Go handlers)           control-plane/internal/handlers/files.go
        │  SSH commands
        ▼
  SSH Layer (sshproxy)             control-plane/internal/sshproxy/files.go
        │  shell commands (ls, cat, mv, rm, find, …)
        ▼
  Agent container (Docker / K8s)
```

The file browser is stateless on the server. Every operation opens a new SSH session via
`executeCommand()` (or `executeCommandWithStdin()` for uploads), runs a shell command,
and returns the output. There is no server-side file cache.

---

## SVAR Filemanager Integration

### Data Model

SVAR builds an internal tree from the `data` prop (an array of `SvarFileItem`):

```ts
interface SvarFileItem {
  id: string;      // virtual path, e.g. "/clawd-data/subdir/file.txt"
  value?: string;  // display name
  size?: number;
  date?: Date;
  type: "folder" | "file";
}
```

Parent is derived from `id` by stripping the last path component. **All ancestor folders must
be present in the data array** or SVAR cannot attach children to the tree. `FileBrowser.tsx`
injects synthetic ancestor items when navigating into a subdirectory.

### Virtual vs Real Paths

SVAR uses virtual paths (e.g. `/subdir/file.txt`). The real agent filesystem path is
`ROOT_PATH + virtualPath` where `ROOT_PATH = "/config"`. All API calls use real paths.

### Event System

| Event | Trigger | Handler in FileBrowser |
|-------|---------|------------------------|
| `set-path` | folder double-click | `api.on` — updates React `currentPath` state |
| `open-file` | file click | `api.on` — sets `selectedFile` for editor |
| `create-file` | new-file dialog confirm | `api.intercept` → POST `/files/create` |
| `upload-file` | drag-drop / upload button | `api.intercept` → POST `/files/upload` |
| `delete-file` | delete key / context menu | `api.intercept` → DELETE `/files?path=…` |
| `rename-file` | rename dialog confirm | `api.intercept` → POST `/files/rename` |

`api.on()` handlers run **after** SVAR's internal handlers (safe for state updates).
`api.intercept()` handlers run **before** SVAR's internal handlers. Return `false` from an
interceptor to prevent SVAR's default behaviour (which would try to modify its in-memory tree
without going through the real filesystem).

### Infinite Loop Guard

`set-path` can trigger a loop: user navigates → `setCurrentPath` → re-render → SVAR rebuilds
tree → internal `set-path` → handler → `setCurrentPath` again. Guard with `currentPathRef`:
only call `updatePath()` when `ev.id !== currentPathRef.current`.

### Directory Cache

`dirCacheRef` maps `virtualPath → SvarFileItem[]`. When navigating, the previous directory's
contents stay cached so the sidebar tree remains expanded. On delete/rename, stale entries are
evicted from the cache by calling `dirCacheRef.current.delete(virtualPath)`.

### Tree Expansion After Rebuild

`m.init()` (called when `data` prop changes) resets all tree nodes to closed state. A
`useEffect` on `fileData` re-expands all previously visited directories and all ancestors of
the current path by calling `api.exec("open-tree-folder", { id, mode: true })`.

---

## API Endpoints

Base path: `/api/v1/instances/{id}/files`

| Method | Path | Params | Description |
|--------|------|--------|-------------|
| `GET` | `/browse` | `?path=` | List directory contents |
| `GET` | `/read` | `?path=` | Read file as text |
| `GET` | `/download` | `?path=` | Download file as octet-stream |
| `POST` | `/create` | body: `{path, content}` | Create/overwrite file |
| `POST` | `/mkdir` | body: `{path}` | Create directory (mkdir -p) |
| `POST` | `/upload` | multipart: `file` field, `?path=` dir | Upload file |
| `DELETE` | (no suffix) | `?path=` | Delete file or directory recursively |
| `POST` | `/rename` | body: `{from, to}` | Rename or move |
| `GET` | `/search` | `?path=`, `?query=` | Search by filename (up to 200 results) |

### Response shapes

**Browse**: `{ "path": string, "entries": FileEntry[] }`

**FileEntry**:
```json
{ "name": "...", "type": "file|directory|symlink", "size": "123", "permissions": "-rw-r--r--" }
```

**Read**: `{ "path": string, "content": string }`

**Create / Mkdir / Upload / Delete / Rename**: `{ "success": true, "path": "..." }`

**Search**: `{ "path": string, "query": string, "results": FileEntry[] }`
(Each result's `name` is the **full path** of the match, not just the basename.)

---

## SSH Operations

| API operation | Shell command |
|---------------|---------------|
| Browse | `ls -la --color=never <path>` |
| Read | `cat <path>` |
| Write / Create | `> <path>` then `echo '<b64-chunk>' \| base64 -d >> <path>` (chunked) |
| Mkdir | `mkdir -p <path>` |
| Delete | `rm -rf <path>` |
| Rename | `mv <old> <new>` |
| Search | `find <dir> -iname '*<query>*' -not -path '*/\.*' 2>/dev/null \| head -200` |

All paths are quoted with `shellQuote()` (single-quote with embedded single-quote escaping)
from `sshproxy/logs.go`.

Search uses `stat -c '%F'` on each result to determine if it is a file or directory.

---

## Known Quirks and Design Decisions

- **`SearchFiles` returns full paths as `Name`**: Unlike `ListDirectory` which returns bare
  filenames, `SearchFiles` sets `Name` to the absolute path returned by `find`. API consumers
  should display accordingly.

- **Upload path resolution**: If `?path=` already ends with the filename, it is used as-is.
  Otherwise the filename from `Content-Disposition` is appended. See `UploadFile` handler.

- **No size for directories in browse**: `size` is `null` in `FileEntry` for directories
  (matching the `ls -la` output where directory size is block count, not meaningful).

- **WriteFile truncates first**: `WriteFile` always truncates the target file before writing
  chunks. This means a failed mid-write leaves a partial file.

- **Delete is recursive**: `rm -rf` is used unconditionally. Deleting a file or a directory
  tree both succeed. There is no trash / undo.

- **Search skips hidden files**: The `find` command excludes paths matching `*/.*` to avoid
  surfacing `.git`, `.cache`, etc.

- **Audit logging**: Every file operation is logged via `auditFileOp()` to `ssh_audit_logs`
  (event type `file_operation`). This includes op name, path, and size where applicable.

- **Authentication**: All file endpoints are under the `RequireAuth` middleware group.
  `CanAccessInstance` additionally restricts non-admin users to their own instances.
