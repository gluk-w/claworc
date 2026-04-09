# Kanban Board with Auto-Routed OpenClaw Tasks

## Context

Claworc manages multiple OpenClaw instances but offers no built-in way to organize work *across* them. The Kanban feature lets users create tasks on a global board, and a **moderator** component automatically picks the best-fit instance based on each agent's "soul" (an LLM summary of its workspace markdown) and skill set, dispatches the task, streams results back as comments, pulls artifacts, and runs an LLM evaluator.

---

## Architecture Overview

```
┌─────────────┐    ┌──────────────────────────────────┐    ┌──────────────┐
│  Frontend   │    │       Control-Plane              │    │  OpenClaw    │
│  KanbanPage │◄──►│  ┌────────────────────────────┐  │    │  Instance    │
│  (polling)  │    │  │  HTTP handlers (CRUD)      │  │    │              │
└─────────────┘    │  └────────────────────────────┘  │    │  Gateway WS  │
                   │  ┌────────────────────────────┐  │SSH │  ws://.../   │
                   │  │  Moderator service         │──┼───►│  gateway     │
                   │  │  - dispatcher              │  │tun │              │
                   │  │  - workspace summarizer    │  │nel │              │
                   │  │  - WS run client per task  │◄─┼────┤  events      │
                   │  │  - artifact collector      │  │exec│  workspace/  │
                   │  └────────────────────────────┘  │    │              │
                   └──────────────────────────────────┘    └──────────────┘
```

Result delivery uses the **existing chat WebSocket protocol** (same `DialGateway` helper that `handlers/chat.go` uses), driven from a Go-side moderator goroutine with a unique `sessionKey` per task. No custom OpenClaw skill, no outbound webhook.

---

## Data Model

GORM models in `control-plane/internal/database/models.go`, registered in `database.go` AutoMigrate.

```go
type KanbanBoard struct {
    ID                uint
    Name              string
    Description       string
    EligibleInstances string    // JSON array of instance IDs
    CreatedAt, UpdatedAt time.Time
}

type KanbanTask struct {
    ID                   uint
    BoardID              uint      // FK → KanbanBoard
    Title                string    // auto-generated from first line of description
    Description          string    // user-provided prompt sent to OpenClaw
    Status               string    // draft → todo → dispatching → in_progress → done|failed|archived
    AssignedInstanceID   *uint     // chosen by moderator dispatcher
    OpenClawSessionID    string    // per-task gateway sessionKey
    OpenClawRunID        string
    EvaluatorProviderKey string    // global provider chosen on task form
    EvaluatorModel       string
    CreatedAt, UpdatedAt time.Time
}

type KanbanComment struct {
    ID                uint
    TaskID            uint
    Kind              string    // routing|assistant|tool|moderator|evaluation|error|user
    Author            string    // "moderator", "agent:<instance-name>", or username
    Body              string    // Markdown; assistant comment body is replaced (not appended) per cumulative snapshot
    OpenClawSessionID string    // session id for traceability
    CreatedAt, UpdatedAt time.Time
}

type KanbanArtifact struct {
    ID          uint
    TaskID      uint
    Path        string    // workspace-relative path
    SizeBytes   int64
    SHA256      string
    StoragePath string    // local storage under CLAWORC_DATA_PATH/kanban/artifacts/<taskID>/
    CreatedAt   time.Time
}

type InstanceSoul struct {
    InstanceID uint      // primary key
    Summary    string    // LLM-generated workspace summary
    Skills     string    // JSON array of skill slugs
    UpdatedAt  time.Time
}
```

---

## Task Statuses

| Status | Meaning |
|---|---|
| `draft` | Created but not yet dispatched. User can still edit description/model. |
| `todo` | Ready for dispatch. Moderator will pick it up. |
| `dispatching` | Moderator is choosing an instance (LLM ranking in progress). |
| `in_progress` | Agent is working on the task. |
| `done` | Agent finished. Artifacts collected, evaluation complete. |
| `failed` | Dispatch or run encountered an error (not user-stop). |
| `archived` | User confirmed completion. Hidden from board columns, viewable via "View archived". |

---

## Backend: Moderator Package

Package: `control-plane/internal/moderator/`. **Fully isolated** — imports only Go stdlib. No imports from `internal/sshproxy`, `internal/handlers`, `internal/orchestrator`, `internal/database`, or `internal/llmgateway`. Verified with `go list -deps ./internal/moderator`.

All external dependencies are expressed as narrow interfaces (ports). Adapters wiring these ports to claworc internals live in `internal/modwiring/adapters.go` and are passed in via `moderator.New()` in `main.go`.

### Ports (`moderator/ports.go`)

```go
type GatewayDialer interface {
    Dial(ctx context.Context, instanceID uint, sessionKey string) (GatewayConn, error)
}

type GatewayConn interface {
    Send(ctx context.Context, frame []byte) error
    Recv(ctx context.Context) ([]byte, error)
    Close() error
}

type WorkspaceFS interface {
    List(ctx context.Context, instanceID uint, dir string) ([]FileEntry, error)
    Read(ctx context.Context, instanceID uint, path string) ([]byte, error)
    Write(ctx context.Context, instanceID uint, path string, data []byte) error
    MkdirAll(ctx context.Context, instanceID uint, dir string) error
    RemoveAll(ctx context.Context, instanceID uint, path string) error
}

type LLMClient interface {
    Complete(ctx context.Context, providerKey, model, prompt string) (string, error)
}

type Store interface {
    GetTask(ctx context.Context, id uint) (Task, error)
    UpdateTask(ctx context.Context, id uint, fields map[string]any) error
    GetBoard(ctx context.Context, id uint) (Board, error)
    InsertComment(ctx context.Context, c Comment) (uint, error)
    SetCommentBody(ctx context.Context, id uint, body string) error
    ListComments(ctx context.Context, taskID uint) ([]Comment, error)
    InsertArtifact(ctx context.Context, a Artifact) error
    ListTaskArtifacts(ctx context.Context, taskID uint) ([]Artifact, error)
    GetSouls(ctx context.Context, instanceIDs []uint) ([]Soul, error)
    UpsertSoul(ctx context.Context, s Soul) error
}

type Settings interface {
    ModeratorProvider() (key, model string)
    SummaryInterval() time.Duration
    ArtifactMaxBytes() int64
    ArtifactStorageDir() string
    WorkspaceDir() string
    TaskOutcomeDir() string    // default "/home/claworc/tasks"
}

type InstanceLister interface {
    ListInstanceIDs(ctx context.Context) ([]uint, error)
    InstanceName(ctx context.Context, id uint) (string, error)
}
```

### Files

- **`ports.go`** — interface definitions + plain DTO structs (`Task`, `Comment`, `Board`, `Soul`, `Artifact`, `FileEntry`).
- **`moderator.go`** — `Service` struct with per-task cancel context map. Methods: `EnqueueTask`, `Stop`, `Reopen`, `markStopped`, `markFailed`.
- **`dispatcher.go`** — `Dispatch(ctx, taskID)`: loads board → eligible instances → cached souls → LLM ranking → routing comment with instance display name → sets status to `dispatching`.
- **`runner.go`** — `Run(ctx, taskID)`: injects prior artifacts, builds comment history, opens gateway WS, sends structured prompt, streams events, collects outcomes, cleans up instance files, runs evaluator.
- **`summarizer.go`** — background goroutine refreshing `InstanceSoul` per instance at `kanban_summary_interval`.
- **`mentions.go`** — regex-based path extractor for mention-driven artifact collection (legacy fallback).

### Key behaviors

**Cumulative text handling.** OpenClaw gateway `assistant` stream events send the *full cumulative text* in `data.text`, not incremental deltas. The runner replaces the assistant comment body via `SetCommentBody` each time (not append). This prevents the duplication bug where appended chunks repeat earlier text.

**Per-task cancellation.** `Service` maintains a `sync.Mutex`-guarded `map[uint]context.CancelFunc`. `EnqueueTask` creates a cancellable context per task. `Stop(taskID)` cancels it. Both the Dispatch and Run phases check for cancellation — stopped tasks are moved to `todo` (not `failed`) with a "Task stopped." moderator comment.

**Reopen.** `Reopen(taskID)` sets status back to `todo` and calls `EnqueueTask`. The runner reads existing `kind=user` comments via `ListComments` and appends them after the task description as `--- User feedback ---` so the agent sees user notes on the next run.

**Instance name resolution.** Both the dispatcher routing comment and the runner's comment author use `InstanceLister.InstanceName()` to show the display name (e.g. "Routed to My Agent", "agent:My Agent") instead of numeric IDs.

### Adapters (`internal/modwiring/adapters.go`)

| Adapter | Satisfies | Implementation |
|---|---|---|
| `GatewayDialer` | `moderator.GatewayDialer` | Looks up tunnel port + decrypts gateway token → calls `sshproxy.DialGateway` → wraps `*websocket.Conn` in `GatewayConn` |
| `WorkspaceFS` | `moderator.WorkspaceFS` | Calls `SSHManager.EnsureConnectedWithIPCheck` → `sshproxy.ListDirectory`/`ReadFile`/`WriteFile`/`CreateDirectory`/`DeletePath` |
| `LLMClient` | `moderator.LLMClient` | Direct HTTP call to provider BaseURL. Switches on `prov.APIType`: `anthropic-messages` uses `/v1/messages` with `x-api-key`, default uses OpenAI-compat `/v1/chat/completions` with Bearer auth |
| `Store` | `moderator.Store` | GORM adapter translating between `database.*` models and moderator DTOs |
| `Settings` | `moderator.Settings` | Reads `kanban_*` keys from settings table with sensible defaults |
| `InstanceLister` | `moderator.InstanceLister` | GORM queries on `Instance` table; `InstanceName` returns `display_name` falling back to `name` |

---

## Artifact Pipeline

### Structured outcome storage

The moderator instructs the agent to save all output files to `~/tasks/<taskID>/` on the instance (included as `--- Instructions ---` in the prompt). After the agent finishes:

1. **Directory-based collection** (`collectOutcomes`): recursively lists `~/tasks/<taskID>/` on the instance via `walkDir`. For each file, reads it via SSH, stores locally at `$CLAWORC_DATA_PATH/kanban/artifacts/<taskID>/<relativePath>`, and inserts a `KanbanArtifact` row. Per-file size cap applies (`kanban_artifacts_max_bytes`, default 5 MB).

2. **Fallback to mention-based** (`collectArtifactsMentionBased`): if `~/tasks/<taskID>/` is empty or doesn't exist (agent didn't follow the instruction), falls back to scanning the agent's transcript text for file path mentions using regex patterns (backtick-quoted paths, verb-based mentions like "saved to X", bare workspace-rooted paths). Mentioned paths under the workspace dir are downloaded.

3. **Moderator comment**: a summary of pulled and skipped artifacts is inserted as a `moderator` comment.

4. **Instance cleanup**: after collecting artifacts, `~/tasks/<taskID>/` is deleted from the instance via `WorkspaceFS.RemoveAll`.

### Artifact injection on subsequent runs

When a task is re-run (reopened or retried), before sending the prompt to the agent:

1. `injectPriorArtifacts`: loads `ListTaskArtifacts(taskID)` — artifacts from prior run(s) stored on the control-plane.
2. Reads each artifact file from control-plane local storage (`StoragePath`).
3. Uploads to the assigned instance at `~/tasks/<taskID>/<artifact.Path>` via `WorkspaceFS.Write`.
4. Inserts a `moderator` comment listing injected files.
5. Returns a description string included in the agent prompt under `--- Artifacts from prior run ---`.

### Comment history injection

On subsequent runs, `buildCommentHistory` formats all prior comments (excluding tool comments which are raw JSON and empty bodies) as a readable transcript:

```
[routing] moderator: Routed to InstanceX. ...
[assistant] agent:InstanceX: <agent output, truncated to 2000 chars>
[user] stan: Please also handle edge case X
[evaluation] moderator: VERDICT: success ...
```

Total history is capped at 8000 characters. Included in the prompt under `--- Prior conversation history ---`.

### Agent prompt structure

The full message sent to the agent via `chat.send` is composed of these sections (empty sections omitted):

```
--- Artifacts from prior run ---
Files from the previous run of this task are at ~/tasks/<id>/:
- ~/tasks/<id>/output.py
- ~/tasks/<id>/results.json

--- Prior conversation history ---
[routing] moderator: Routed to MyAgent. ...
[assistant] agent:MyAgent: I created output.py...
[evaluation] moderator: VERDICT: success ...

--- Task ---
<user's task description>

--- Instructions ---
Save all output files to: ~/tasks/<id>/
Use artifacts from the prior run if they are relevant to your work.

--- User feedback ---
<user comments, if any>
```

### Artifact deletion

- **Task completion**: `~/tasks/<id>/` is deleted from the instance after artifacts are collected.
- **Task deletion** (HTTP `DELETE /kanban/tasks/{id}`): artifact files are removed from the control-plane filesystem at `$CLAWORC_DATA_PATH/kanban/artifacts/<id>/` (or the configured `kanban_artifacts_dir`), along with all DB records (comments, artifacts, task).

---

## Backend: HTTP Handlers

File: `control-plane/internal/handlers/kanban.go`. Routes mounted under `/api/v1/kanban/` in `main.go` inside the authenticated route group.

| Method | Path | Purpose |
|---|---|---|
| GET | `/kanban/boards` | List boards |
| POST | `/kanban/boards` | Create board (name, description, eligible_instances[]) |
| GET | `/kanban/boards/{id}` | Board detail + all tasks |
| PUT | `/kanban/boards/{id}` | Update board |
| DELETE | `/kanban/boards/{id}` | Delete board + its tasks |
| POST | `/kanban/boards/{id}/tasks` | Create task → enqueues `Dispatch` if status=todo |
| GET | `/kanban/tasks/{id}` | Task detail with comments + artifacts (polling endpoint) |
| PATCH | `/kanban/tasks/{id}` | Manual field update (status, title, description, evaluator_provider_key, evaluator_model) |
| DELETE | `/kanban/tasks/{id}` | Delete task + comments + artifacts + local artifact files |
| POST | `/kanban/tasks/{id}/start` | Start a draft task (verifies draft status → sets todo → enqueues) |
| POST | `/kanban/tasks/{id}/stop` | Cancel a running task |
| POST | `/kanban/tasks/{id}/comments` | Add a user comment (kind=`user`, author=session username) |
| POST | `/kanban/tasks/{id}/reopen` | Reopen a done/failed task |
| GET | `/kanban/tasks/{id}/artifacts/{artifact_id}` | Download artifact bytes |

### Task creation details

- `title` is optional — if empty, `autoTitle()` takes the first line of the description, capped at 60 chars with `...`.
- `status` can be `"draft"` (saved but not dispatched) or `"todo"` (default, dispatched immediately).
- `description` is required (validated).
- `evaluator_provider_key` + `evaluator_model` override the global moderator LLM for this task's ranking and evaluation.

---

## Settings

Read on-demand from the settings table by the `modwiring.Settings` adapter. Defaults in parentheses.

| Key | Purpose | Default |
|---|---|---|
| `kanban_moderator_provider_key` | Global LLM provider for summarization/ranking/evaluation | (none) |
| `kanban_moderator_model` | Model ID within that provider | (none) |
| `kanban_summary_interval` | How often the summarizer refreshes InstanceSoul | `10m` |
| `kanban_artifacts_max_bytes` | Per-file size cap for artifact download | `5242880` (5 MB) |
| `kanban_artifacts_dir` | Storage root for downloaded artifacts | `${CLAWORC_DATA_PATH}/kanban/artifacts` |
| `kanban_workspace_dir` | Agent workspace path to scan for markdown/artifacts | `/home/claworc/.openclaw/workspace` |
| `kanban_task_outcome_dir` | Base dir on instance for task output files | `/home/claworc/tasks` |

The per-task `EvaluatorProviderKey`/`EvaluatorModel` (selected in the task creation form) overrides the global default for that task's ranking and evaluation LLM calls. The task-form dropdown shows global providers only (not per-instance).

---

## Frontend

Page: `control-plane/frontend/src/pages/KanbanPage.tsx`. Route: `/kanban`. API client: `frontend/src/api/kanban.ts`.

### Layout

- **Board picker**: `<select>` dropdown of boards + "+ New Board" button.
- **Five columns**: Draft, Todo, In Progress, Failed, Done. Tasks with status `dispatching` appear in the In Progress column. Archived tasks are hidden.
- **Task cards**: show `#<id> <title>` with up to 5 lines (`line-clamp-5`). Click opens the task drawer.
- **"+ New Task" button**: opens the task drawer in create mode.
- **"View archived (N)" button**: toggles visibility of a collapsible archived tasks section below the board columns. Only shown when archived tasks exist.

### URL hash sync

The open drawer state is persisted in the URL hash:
- `#task-<id>` — opens the task detail view.
- `#new-task` — opens the create drawer.
- Empty hash — drawer closed.

Back/forward navigation works via `hashchange` event listener.

### New Board modal

Name, description, eligible-instance multi-select (checkboxes from instance list). Esc closes. Style-guide compliant modal footer (Cancel left, Save right).

### Task drawer — chat-style UI

The drawer is a unified component handling both create and view modes.

**Create mode:**
- Header: "New Task".
- Model selector bar at top: "Moderator LLM" label with info tooltip ("Large Language Model that will be used for moderation and outcome analysis"). `<select>` with `<optgroup>` per global provider. Selection persisted in `localStorage` key `kanban-evaluator-model` (format `providerKey::modelId`).
- Body: empty placeholder text "Describe what you want the agent to do..."
- Input bar: textarea + "D" button (save as draft) + Send button (create + dispatch).
- Enter submits (creates + dispatches). Shift+Enter for newlines.

**View mode — Draft task:**
- Header: status pill + `#<id> <title>` + Play button (start working) + trash + close.
- Model selector bar (same as create mode, initialized from task's stored model).
- Input bar: textarea for updating description + Send (update & start).

**View mode — Running task (in_progress / dispatching):**
- Header: status pill ("working...") + Stop button (red square) + trash + close.
- Chat body: task description as first message, then comments as chat bubbles, then `WorkingIndicator`.
- Input bar: textarea for adding comments.

**View mode — Finished task (done / failed):**
- Header for done: status pill + Archive button (green checkmark) + trash + close.
- Header for failed: status pill + Reopen button (rotate icon) + trash + close.
- Chat body: full comment history as chat bubbles + artifacts section.
- Input bar: sending a comment on a done/failed task automatically reopens it. Hint text: "Sending a comment will reopen the task."

**Trash button**: visible on all non-create views. Shows `window.confirm("Delete this task?")` before deleting. Deletes the task (DB records + control-plane artifact files), closes the drawer.

### Chat bubbles

Comments are rendered as chat bubbles with role-based styling:

| Comment kind | Color scheme | Author display |
|---|---|---|
| `routing`, `moderator`, `evaluation` | Amber background, amber border | "Moderator" |
| `error` | Red background, red border | "Error" |
| `user` | Blue background, blue border | "You" (username) |
| `assistant`, `tool` | Gray background, gray border | Instance name |

- **User messages**: plain text with `whitespace-pre-wrap`.
- **Non-user messages**: rendered via `ReactMarkdown` with `remark-gfm`. Light-themed styling: `bg-gray-100` code blocks, `bg-gray-200/60` inline code, `text-blue-600` links, `border-gray-300` tables.
- **Agent avatars**: use `/openclaw.svg` logo instead of letter initials.
- **Other avatars**: colored circles with 2-letter initials (deterministic palette based on author name hash).
- **Empty comments** (body.trim() === "") are filtered out before rendering (prevents empty gray bubble before first streaming event).

### Working indicator

Shown when task is `in_progress` or `dispatching`. Displays:
- OpenClaw logo avatar (pulsing animation).
- `"<InstanceName> is working · <toolName>"` — instance name extracted from the last agent comment's `author` field (`agent:Name` format), tool name extracted by JSON-parsing the last tool comment's body for `name` or `tool` property.

### Polling

- Board data: refetched every 3 seconds.
- Task detail (when drawer is open): refetched every 2 seconds.
- Auto-scroll: chat scrolls to bottom when comment count changes.

---

## Task Lifecycle

### Happy path

1. **Create**: user writes a description in the drawer → clicks Send → `POST /boards/{id}/tasks` with `status: "todo"` → `autoTitle()` generates title from first line → `EnqueueTask` fires background goroutine.
2. **Dispatch**: load board's eligible instances → load cached `InstanceSoul` rows → if >1 candidate, call moderator LLM to rank → insert `routing` comment with instance display name + reasoning → set `assigned_instance_id` + status `dispatching`.
3. **Run**:
   - Inject prior artifacts (no-op on first run) via `injectPriorArtifacts`.
   - Build comment history (empty on first run) via `buildCommentHistory`.
   - Compose structured prompt with artifacts listing + history + task description + instructions + user feedback.
   - Generate per-task `sessionKey` → set status `in_progress`.
   - Open gateway WS via `sshproxy.DialGateway` → send `chat.send` frame.
   - Stream events:
     - `assistant` events: replace rolling comment body with cumulative `data.text` snapshot.
     - `tool` events: insert separate `tool` comment with raw JSON body.
     - `lifecycle` with `phase=end`: break loop.
4. **Artifact collection**: `collectOutcomes` → try `~/tasks/<id>/` directory on instance → fallback to mention-based scanning → store locally → insert artifact rows → insert moderator comment with pull report.
5. **Instance cleanup**: delete `~/tasks/<id>/` from the instance.
6. **Evaluation**: call moderator LLM with task + agent output + artifact list → insert `evaluation` comment with verdict (success|partial|failed) → set status `done`.

### Draft flow

1. User clicks Send with "D" button → `POST /boards/{id}/tasks` with `status: "draft"` → no dispatch.
2. User opens draft → edits model → clicks Play button or types new description + Send → `POST /tasks/{id}/start` → status changes to `todo` → `EnqueueTask`.

### Stop

1. User clicks Stop → `POST /tasks/{id}/stop` → `Service.Stop` cancels the task's context.
2. Runner's recv loop detects `ctx.Done()` → returns `ErrStopped`.
3. `markStopped`: inserts "Task stopped." comment → sets status to **`todo`** (not failed).
4. If stopped during Dispatch (context.Canceled), same `markStopped` path.

### Reopen

1. User sends a comment on a done/failed task → frontend calls `addUserComment` then `reopenTask`.
2. `Reopen(taskID)`: sets status to `todo` → calls `EnqueueTask`.
3. Runner builds message with prior artifacts + full comment history + task description + user feedback notes.

### Archive

1. User clicks checkmark (Archive) on a done task → `PATCH /tasks/{id}` with `status: "archived"`.
2. Task disappears from board columns but remains in DB.
3. "View archived (N)" button toggles the archived section.
4. Opening an archived task from the archived section shows the full chat history and artifacts.

### Delete

1. User clicks trash icon on any task → `window.confirm` → `DELETE /tasks/{id}`.
2. Handler removes artifact files from control-plane filesystem → deletes comments, artifacts, and task from DB.
3. Drawer closes, board refreshes.

### Failure

- Dispatch errors (no eligible instances, LLM failure, etc.): `markFailed` inserts error comment + sets status `failed`.
- Run errors (gateway connection drop, recv failure): same `markFailed`.
- Evaluator failure: inserts error comment but task still moves to `done` (evaluation is non-blocking for task completion).

---

## Verification

1. **Migrations**: start control-plane fresh → confirm `kanban_boards`, `kanban_tasks`, `kanban_comments`, `kanban_artifacts`, `instance_souls` tables are created.
2. **Settings**: set `kanban_moderator_provider_key` + `kanban_moderator_model` via settings API.
3. **Summarizer**: wait for 1 cycle (or restart) → verify `InstanceSoul` rows exist for running instances.
4. **Create board**: select two instances as eligible.
5. **Create task**: type description, model auto-selected from localStorage. Title auto-generated.
6. **Observe routing**: `routing` comment appears with instance display name + reasoning. Card moves to In Progress with "working..." pill.
7. **Observe streaming**: assistant comment body grows in real time (2s polling). Working indicator shows instance name + current tool.
8. **Observe completion**: `lifecycle phase=end` → artifact pull from `~/tasks/<id>/` → instance cleanup → evaluation → card moves to Done.
9. **Draft flow**: create with "D" button → card in Draft column → open → click Play → dispatched.
10. **Stop**: click stop on an in-progress task → "Task stopped" comment → card moves to **Todo** (not Failed).
11. **Reopen**: add a comment on done/failed task → task re-dispatched with prior artifacts injected + full comment history + user feedback.
12. **Archive**: click checkmark on done task → card disappears → visible via "View archived" toggle.
13. **Delete**: click trash → confirm → task + artifacts removed from DB and filesystem.
14. **URL hash**: open task → URL shows `#task-<id>` → reload page → same task drawer opens. Navigate back → drawer closes.
15. **Isolation**: `go list -deps ./internal/moderator | grep claworc` → only `internal/moderator` itself.
