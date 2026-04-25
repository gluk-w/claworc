# Task Manager

The control plane runs many long-lived goroutines on behalf of user actions:
creating an instance, restarting it, updating its image, cloning it, taking
a backup, deploying a skill. Until the TaskManager existed, status for that
work was kept on the database row of whatever it was operating on
(`instance.status = 'creating'`, `backup.status = 'running'`, …). When the
goroutine died — process restart, panic, OOM, disk full — those rows stayed
in their transient state forever and there was no way to cancel them
(see issue #89).

The `taskmanager` package owns every long-running goroutine and is the
**in-memory source of truth** for "what work is currently happening." It
exposes status over REST and an SSE stream, supports cancellation with a
cleanup callback, and survives stale DB state by treating the DB row as a
historical record while the live in-memory `Task` is authoritative.

## Concepts

- **`TaskType`** — the kind of work. Constants live in `taskmanager.go`:
  `instance.create`, `instance.restart`, `instance.image_update`,
  `instance.clone`, `backup.create`, `skill.deploy`. Add a new constant when
  you wire up a new operation.
- **`State`** — lifecycle position: `running → succeeded | failed | canceled`.
  Once terminal, the state never changes.
- **`OnCancel`** — a callback invoked exactly once when a task is canceled,
  *after* `ctx` is canceled and *before* the task transitions to
  `canceled`. Use it to remove partial files, mark DB rows, abort SSH
  sessions. **A nil `OnCancel` means the task is not user-cancellable**:
  `Manager.Cancel` returns `ErrNotCancellable` and the REST handler maps
  that to **405 Method Not Allowed**.
- **`InstanceID`** on a Task is metadata used for filtering
  (`?instance_id=`) and the toast subject line. It is **not** the access
  predicate.
- **`UserID`** on a Task is the visibility anchor — the ID of the user
  who initiated the work. Non-admins only see tasks where
  `Task.UserID == caller.ID`. `UserID == 0` denotes a system-initiated
  task (e.g. scheduled backup) and is admin-only. Always pass
  `callerID(r)` from the HTTP handler so toasts surface to the user who
  clicked the button — not to everyone with access to the same instance.

## Usage from a handler

```go
import (
    "context"
    "fmt"

    "github.com/gluk-w/claworc/control-plane/internal/handlers"
    "github.com/gluk-w/claworc/control-plane/internal/taskmanager"
)

handlers.TaskMgr.Start(taskmanager.StartOpts{
    Type:         taskmanager.TaskBackupCreate,
    InstanceID:   inst.ID,
    UserID:       callerID(r), // visibility — toast goes only to this user
    ResourceID:   strconv.FormatUint(uint64(b.ID), 10),
    ResourceName: fmt.Sprintf("%s backup", inst.Name),
    OnCancel:     backupOnCancel(b.ID, absPath), // nil = not cancellable
    Run: func(ctx context.Context, h *taskmanager.Handle) error {
        h.UpdateMessage("archiving filesystem")
        if err := runFullBackup(ctx, ...); err != nil {
            return err
        }
        return nil
    },
})
```

Inside `Run`:
- Honour `ctx`. The context is cancelled when the user clicks Cancel — your
  long-running calls (orchestrator, SSH, network IO) should propagate it.
- Call `h.UpdateMessage("step name")` whenever you reach a meaningful
  milestone. The message becomes the toast description on the frontend and
  fires an `updated` SSE event.

## Lifecycle guarantees

- `Run` is called once per `Start`. A `Run` panic is recovered into a
  `failed` state with `Message = "panic: …"`.
- `OnCancel` is invoked exactly once. If `Run` returns after a cancel, its
  return value is ignored — the task remains `canceled`.
- `OnCancel` runs in a fresh 30-second context, not the cancelled one, so
  cleanup itself is not instantly aborted.
- Terminal tasks remain in the in-memory map for one hour after `EndedAt`,
  then are GC'd by a background sweeper. The retention window lets the
  frontend show a brief "succeeded" toast and lets late-arriving SSE
  reconnects re-seed.
- Subscribers (`Subscribe`) get a buffered channel; events are **dropped**
  if the subscriber is slower than the producer. Reconnecting clients
  re-seed via `List({OnlyActive: true})` so no live state is lost on the
  network — but historical events between disconnect and reconnect are
  not replayed.

## REST + SSE API

All endpoints are mounted under `/api/v1/tasks` and require auth.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/tasks` | List tasks; query: `type`, `instance_id`, `resource_id`, `state`, `only_active=true` |
| `GET` | `/api/v1/tasks/{id}` | Get one task |
| `POST` | `/api/v1/tasks/{id}/cancel` | Cancel; 405 if not cancellable, 409 if already terminal |
| `GET` | `/api/v1/tasks/events` | SSE stream of `{type, task}` JSON |

### Visibility

Tasks belong to the user who started them. A toast for "Restarting bot-foo"
should surface to the user who clicked Restart — not to every other user
who happens to have access to that instance.

- Admin (`User.Role == "admin"`): sees and acts on every task, including
  system-initiated tasks (`UserID == 0`).
- Non-admin: sees only tasks where `Task.UserID == User.ID`. Tasks with
  `UserID == 0` are admin-only.
- The same predicate gates `GET /tasks`, `GET /tasks/{id}`,
  `POST /tasks/{id}/cancel`, and the per-event filter on `/tasks/events`.
  Each SSE subscriber captures `(isAdmin, userID)` at connect time;
  changing roles requires a reconnect to take effect.

### SSE event shape

```json
{ "type": "started", "task": { "id": "...", "type": "backup.create", "state": "running", ... } }
{ "type": "updated", "task": { "id": "...", "message": "step 2",   ... } }
{ "type": "ended",   "task": { "id": "...", "state": "succeeded",  ... } }
```

`type` is one of `started`, `updated`, `ended`. Clients should key by
`task.id` and replace prior state on each event.

## Boot reconciliation

The control plane runs `reconcileStuckTasks()` once at startup
(`control-plane/reconcile.go`). It:

- Marks `instances` rows in `creating` / `restarting` / `stopping` /
  `deleting` as `error` with reason `"process restarted before task
  completed"`.
- Marks `backups` rows in `running` as `failed` with the same reason.

This is the durable fix for stuck rows after a process crash: the
in-memory tasks of the previous process are gone, so we treat their DB
rows as history and let the user retry.

## Adding a new task type

1. Add a `TaskType` constant in `taskmanager.go`.
2. At the call site, replace `go func() { ... }()` with
   `handlers.TaskMgr.Start(taskmanager.StartOpts{ ... })` (or
   `backup.TaskMgr.Start` if you're inside the `backup` package).
3. Decide cancellability: provide an `OnCancel` (cleanup) or leave it nil
   (not user-cancellable, returns 405).
4. Set `UserID` from the request (`callerID(r)`) so the toast surfaces
   only to the user who triggered the action. Use 0 only for
   system-initiated work (e.g. cron / scheduler), which is admin-only.
   Set `InstanceID` for filtering and toast labels.
5. On the frontend, add a label in `TaskToasts.tsx` (`TYPE_LABEL` map) so
   the toast says something humans recognise.
6. If you added a new failure mode that can leave a DB row stuck, extend
   `reconcile.go` to sweep it.

## Files

- `control-plane/internal/taskmanager/taskmanager.go` — package
- `control-plane/internal/taskmanager/taskmanager_test.go` — unit tests
- `control-plane/internal/handlers/tasks.go` — REST + SSE + RBAC
- `control-plane/reconcile.go` — boot-time stuck-row sweep
- `control-plane/frontend/src/api/tasks.ts` — frontend client
- `control-plane/frontend/src/hooks/useTaskStream.ts` — singleton SSE store
- `control-plane/frontend/src/components/TaskToasts.tsx` — global toast surface
