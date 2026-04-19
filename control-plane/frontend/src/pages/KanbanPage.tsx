import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Loader2, Square, X, Info, Send, RotateCcw, Play, Check, Archive, Trash2 } from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  kanbanApi,
  type KanbanBoard,
  type KanbanTask,
  type KanbanComment,
} from "@/api/kanban";
import { fetchProviders } from "@/api/llm";
import { useInstances } from "@/hooks/useInstances";
import { successToast, errorToast } from "@/utils/toast";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const COLUMNS: { key: string; label: string }[] = [
  { key: "draft", label: "Draft" },
  { key: "todo", label: "Todo" },
  { key: "in_progress", label: "In Progress" },
  { key: "failed", label: "Failed" },
  { key: "done", label: "Done" },
];

const LS_MODEL_KEY = "kanban-evaluator-model";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatTime(iso: string) {
  const d = new Date(iso);
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}


/** Read #task-<id> or #new-task from the URL hash. */
function readHash(): { taskId: number | null; newTask: boolean } {
  const h = window.location.hash;
  if (h === "#new-task") return { taskId: null, newTask: true };
  const m = h.match(/^#task-(\d+)$/);
  if (m) return { taskId: Number(m[1]), newTask: false };
  return { taskId: null, newTask: false };
}

function setHash(v: string) {
  window.history.replaceState(null, "", v || window.location.pathname);
}

// ---------------------------------------------------------------------------
// Comment kind → visual role
// ---------------------------------------------------------------------------

const ROLE_COLORS: Record<string, { bg: string; border: string; name: string }> = {
  moderator: { bg: "bg-amber-50", border: "border-amber-200", name: "Moderator" },
  routing: { bg: "bg-amber-50", border: "border-amber-200", name: "Moderator" },
  evaluation: { bg: "bg-amber-50", border: "border-amber-200", name: "Moderator" },
  error: { bg: "bg-red-50", border: "border-red-200", name: "Error" },
  user: { bg: "bg-blue-50", border: "border-blue-200", name: "You" },
  assistant: { bg: "bg-gray-50", border: "border-gray-200", name: "Agent" },
  tool: { bg: "bg-gray-50", border: "border-gray-200", name: "Agent" },
};

function roleOf(c: KanbanComment) {
  return ROLE_COLORS[c.kind] ?? ROLE_COLORS.assistant;
}

// Author-to-avatar color (deterministic)
function avatarColor(author: string): string {
  const palette = [
    "bg-blue-600",
    "bg-green-600",
    "bg-purple-600",
    "bg-amber-600",
    "bg-rose-600",
    "bg-cyan-600",
    "bg-indigo-600",
    "bg-teal-600",
  ];
  let h = 0;
  for (let i = 0; i < author.length; i++) h = (h * 31 + author.charCodeAt(i)) | 0;
  return palette[Math.abs(h) % palette.length];
}

function authorInitials(author: string): string {
  if (author === "moderator") return "M";
  if (author.startsWith("agent:")) return author.slice(6, 8).toUpperCase();
  return author.slice(0, 2).toUpperCase();
}

function authorDisplayName(author: string): string {
  if (author === "moderator") return "Moderator";
  if (author.startsWith("agent:")) return author.slice(6);
  return author;
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function StatusPill({ status }: { status: string }) {
  const map: Record<string, string> = {
    draft: "bg-gray-100 text-gray-500",
    todo: "bg-gray-100 text-gray-600",
    dispatching: "bg-yellow-100 text-yellow-800",
    in_progress: "bg-blue-100 text-blue-800",
    done: "bg-green-100 text-green-800",
    failed: "bg-red-100 text-red-800",
  };
  return (
    <span
      className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
        map[status] ?? "bg-gray-100 text-gray-600"
      }`}
    >
      {status === "in_progress" || status === "dispatching" ? "working..." : status}
    </span>
  );
}

// ---------------------------------------------------------------------------
// Model selector
// ---------------------------------------------------------------------------

interface ProviderModelOption {
  providerKey: string;
  providerName: string;
  modelId: string;
  modelName: string;
}

function useModelGroups() {
  const providersQ = useQuery({ queryKey: ["llm-providers"], queryFn: fetchProviders });
  return useMemo(() => {
    const out: { providerKey: string; providerName: string; models: ProviderModelOption[] }[] =
      [];
    (providersQ.data ?? []).forEach((p: any) => {
      if (p.instance_id) return;
      const models = (p.models ?? []) as any[];
      if (models.length === 0) return;
      out.push({
        providerKey: p.key,
        providerName: p.name,
        models: models.map((m: any) => ({
          providerKey: p.key,
          providerName: p.name,
          modelId: m.id,
          modelName: m.name || m.id,
        })),
      });
    });
    return out;
  }, [providersQ.data]);
}

function ModelSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const groups = useModelGroups();
  return (
    <div className="flex items-center gap-2">
      <label className="text-xs text-gray-500 whitespace-nowrap flex items-center gap-1">
        Moderator LLM
        <span
          title="Large Language Model that will be used for moderation and outcome analysis"
          className="inline-flex items-center cursor-help"
        >
          <Info size={12} className="text-gray-400" />
        </span>
      </label>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="flex-1 px-2 py-1 border border-gray-300 rounded-md text-xs focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white"
      >
        <option value="" disabled hidden>
          Select model...
        </option>
        {groups.map((g) => (
          <optgroup key={g.providerKey} label={g.providerName}>
            {g.models.map((m) => (
              <option
                key={`${g.providerKey}::${m.modelId}`}
                value={`${g.providerKey}::${m.modelId}`}
              >
                {m.modelName}
              </option>
            ))}
          </optgroup>
        ))}
      </select>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Chat message bubble
// ---------------------------------------------------------------------------

function ChatBubble({ comment }: { comment: KanbanComment }) {
  const role = roleOf(comment);
  const isUser = comment.kind === "user";
  const isAgent = comment.author.startsWith("agent:");
  return (
    <div className={`flex gap-2.5 ${isUser ? "flex-row-reverse" : ""}`}>
      {/* Avatar */}
      {isAgent ? (
        <div className="w-7 h-7 rounded-full flex-shrink-0 flex items-center justify-center bg-gray-100 border border-gray-200">
          <img src="/openclaw.svg" alt="Agent" width={16} height={16} />
        </div>
      ) : (
        <div
          className={`w-7 h-7 rounded-full flex-shrink-0 flex items-center justify-center text-white text-[10px] font-bold ${avatarColor(comment.author)}`}
        >
          {authorInitials(comment.author)}
        </div>
      )}
      {/* Bubble */}
      <div className={`max-w-[80%] min-w-0`}>
        <div
          className={`flex items-baseline gap-2 mb-0.5 ${isUser ? "flex-row-reverse" : ""}`}
        >
          <span className="text-xs font-medium text-gray-700">
            {authorDisplayName(comment.author)}
          </span>
          <span className="text-[10px] text-gray-400">{formatTime(comment.created_at)}</span>
        </div>
        <div
          className={`rounded-lg px-3 py-2 text-sm break-words border ${role.bg} ${role.border}`}
        >
          {isUser ? (
            <div className="whitespace-pre-wrap">{comment.body}</div>
          ) : (
            <div className="prose-kanban [&>*:first-child]:mt-0 [&>*:last-child]:mb-0">
              <ReactMarkdown
                remarkPlugins={[remarkGfm]}
                components={{
                  p: ({ children }) => <p className="mb-2">{children}</p>,
                  pre: ({ children }) => (
                    <pre className="bg-gray-100 rounded p-2 my-2 overflow-x-auto text-xs">{children}</pre>
                  ),
                  code: ({ children, className }) => {
                    const isBlock = className?.startsWith("language-");
                    return isBlock ? (
                      <code className={className}>{children}</code>
                    ) : (
                      <code className="bg-gray-200/60 rounded px-1 py-0.5 text-xs">{children}</code>
                    );
                  },
                  a: ({ href, children }) => (
                    <a href={href} target="_blank" rel="noopener noreferrer" className="text-blue-600 underline hover:text-blue-800">
                      {children}
                    </a>
                  ),
                  ul: ({ children }) => <ul className="list-disc pl-4 mb-2">{children}</ul>,
                  ol: ({ children }) => <ol className="list-decimal pl-4 mb-2">{children}</ol>,
                  li: ({ children }) => <li className="mb-0.5">{children}</li>,
                  strong: ({ children }) => <strong className="font-semibold">{children}</strong>,
                  blockquote: ({ children }) => (
                    <blockquote className="border-l-2 border-gray-300 pl-2 my-2 text-gray-500">{children}</blockquote>
                  ),
                  table: ({ children }) => (
                    <div className="overflow-x-auto my-2">
                      <table className="border-collapse text-xs">{children}</table>
                    </div>
                  ),
                  th: ({ children }) => <th className="border border-gray-300 px-2 py-1 bg-gray-100">{children}</th>,
                  td: ({ children }) => <td className="border border-gray-300 px-2 py-1">{children}</td>,
                }}
              >
                {comment.body}
              </ReactMarkdown>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Working indicator — shows instance name + current tool
// ---------------------------------------------------------------------------

function extractToolName(body: string): string | null {
  try {
    const d = JSON.parse(body);
    return d.name || d.tool || null;
  } catch {
    return null;
  }
}

function WorkingIndicator({ comments }: { comments: KanbanComment[] }) {
  // Find instance name from the last agent comment
  const agentComment = [...comments].reverse().find((c) => c.author.startsWith("agent:"));
  const instanceName = agentComment ? authorDisplayName(agentComment.author) : "Agent";

  // Find the last tool being called
  const lastTool = [...comments]
    .reverse()
    .find((c) => c.kind === "tool");
  const toolName = lastTool ? extractToolName(lastTool.body) : null;

  return (
    <div className="flex gap-2.5">
      <div className="w-7 h-7 rounded-full flex-shrink-0 flex items-center justify-center bg-gray-100 border border-gray-200">
        <img src="/openclaw.svg" alt="Agent" width={16} height={16} className="animate-pulse" />
      </div>
      <div className="rounded-lg px-3 py-2 text-xs text-gray-400 border border-gray-200 bg-gray-50">
        <span className="text-gray-600 font-medium">{instanceName}</span> is working
        {toolName ? (
          <>
            {" "}
            &middot;{" "}
            <span className="font-mono text-gray-500">{toolName}</span>
          </>
        ) : (
          "..."
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Drawer state type
// ---------------------------------------------------------------------------

type DrawerState =
  | { mode: "closed" }
  | { mode: "create" }
  | { mode: "view"; taskId: number };

// ---------------------------------------------------------------------------
// KanbanPage
// ---------------------------------------------------------------------------

export default function KanbanPage() {
  const qc = useQueryClient();
  const [selectedBoardId, setSelectedBoardId] = useState<number | null>(null);
  const [showNewBoard, setShowNewBoard] = useState(false);
  const [showArchived, setShowArchived] = useState(false);
  const [drawer, setDrawer] = useState<DrawerState>(() => {
    const { taskId, newTask } = readHash();
    if (newTask) return { mode: "create" };
    if (taskId) return { mode: "view", taskId };
    return { mode: "closed" };
  });

  const boardsQ = useQuery({ queryKey: ["kanban-boards"], queryFn: kanbanApi.listBoards });
  const boardQ = useQuery({
    queryKey: ["kanban-board", selectedBoardId],
    queryFn: () => kanbanApi.getBoard(selectedBoardId!),
    enabled: selectedBoardId != null,
    refetchInterval: 3000,
  });

  useEffect(() => {
    if (selectedBoardId == null && boardsQ.data && boardsQ.data.length > 0) {
      setSelectedBoardId(boardsQ.data[0].id);
    }
  }, [boardsQ.data, selectedBoardId]);

  // Sync drawer state → URL hash
  useEffect(() => {
    if (drawer.mode === "create") setHash("#new-task");
    else if (drawer.mode === "view") setHash(`#task-${drawer.taskId}`);
    else setHash("");
  }, [drawer]);

  // Listen for browser back/forward changing the hash
  useEffect(() => {
    const onHash = () => {
      const { taskId, newTask } = readHash();
      if (newTask) setDrawer({ mode: "create" });
      else if (taskId) setDrawer({ mode: "view", taskId });
      else setDrawer({ mode: "closed" });
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  const tasksByStatus = useMemo(() => {
    const buckets: Record<string, KanbanTask[]> = {};
    COLUMNS.forEach((c) => (buckets[c.key] = []));
    (boardQ.data?.tasks ?? []).forEach((t) => {
      if (t.status === "archived") return;
      const key = t.status === "dispatching" ? "in_progress" : t.status;
      (buckets[key] ?? buckets.todo).push(t);
    });
    return buckets;
  }, [boardQ.data]);

  const archivedTasks = useMemo(
    () => (boardQ.data?.tasks ?? []).filter((t) => t.status === "archived"),
    [boardQ.data],
  );

  const onChanged = () => qc.invalidateQueries({ queryKey: ["kanban-board", selectedBoardId] });

  const openTask = useCallback((id: number) => setDrawer({ mode: "view", taskId: id }), []);
  const closeDrawer = useCallback(() => setDrawer({ mode: "closed" }), []);

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-3">
          <h1 className="text-xl font-semibold text-gray-900">Kanban</h1>
          <select
            value={selectedBoardId ?? ""}
            onChange={(e) => setSelectedBoardId(Number(e.target.value) || null)}
            className="px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white"
          >
            <option value="" disabled hidden>
              Select board...
            </option>
            {boardsQ.data?.map((b) => (
              <option key={b.id} value={b.id}>
                {b.name}
              </option>
            ))}
          </select>
          <button
            type="button"
            onClick={() => setShowNewBoard(true)}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
          >
            + New Board
          </button>
        </div>
        {selectedBoardId != null && (
          <div className="flex items-center gap-2">
            {archivedTasks.length > 0 && (
              <button
                type="button"
                onClick={() => setShowArchived((v) => !v)}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-gray-600 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
              >
                <Archive size={13} />
                {showArchived ? "Hide archived" : `View archived (${archivedTasks.length})`}
              </button>
            )}
            <button
              type="button"
              onClick={() => setDrawer({ mode: "create" })}
              className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
            >
              + New Task
            </button>
          </div>
        )}
      </div>

      {selectedBoardId == null ? (
        <div className="text-center py-16 text-gray-400">
          <p className="text-sm">No board selected.</p>
          <p className="text-xs mt-1">Pick or create a board to get started.</p>
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-5 gap-4">
          {COLUMNS.map((col) => (
            <div
              key={col.key}
              className="bg-white rounded-lg border border-gray-200 p-3 min-h-[200px]"
            >
              <h3 className="text-xs uppercase tracking-wide text-gray-500 font-semibold mb-3">
                {col.label}{" "}
                <span className="text-xs font-mono text-gray-400 bg-gray-100 px-1.5 py-0.5 rounded">
                  {tasksByStatus[col.key]?.length ?? 0}
                </span>
              </h3>
              <div className="space-y-2">
                {(tasksByStatus[col.key] ?? []).map((t) => (
                  <button
                    key={t.id}
                    onClick={() => openTask(t.id)}
                    className="w-full text-left bg-white hover:bg-blue-50 border border-gray-200 hover:border-blue-300 rounded-md p-2 transition-colors"
                  >
                    <div className="text-sm font-medium text-gray-900 line-clamp-5">
                      <span className="text-gray-400 font-mono mr-1">#{t.id}</span>
                      {t.title}
                    </div>
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {showArchived && archivedTasks.length > 0 && (
        <div className="mt-6 bg-white rounded-lg border border-gray-200 p-3">
          <h3 className="text-xs uppercase tracking-wide text-gray-500 font-semibold mb-3">
            Archived{" "}
            <span className="text-xs font-mono text-gray-400 bg-gray-100 px-1.5 py-0.5 rounded">
              {archivedTasks.length}
            </span>
          </h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-2">
            {archivedTasks.map((t) => (
              <button
                key={t.id}
                onClick={() => openTask(t.id)}
                className="w-full text-left bg-gray-50 hover:bg-blue-50 border border-gray-200 hover:border-blue-300 rounded-md p-2 transition-colors"
              >
                <div className="text-sm font-medium text-gray-500 truncate">
                  <span className="text-gray-400 font-mono mr-1">#{t.id}</span>
                  {t.title}
                </div>
              </button>
            ))}
          </div>
        </div>
      )}

      {showNewBoard && (
        <NewBoardModal
          onClose={() => setShowNewBoard(false)}
          onCreated={(b) => {
            qc.invalidateQueries({ queryKey: ["kanban-boards"] });
            setSelectedBoardId(b.id);
            setShowNewBoard(false);
          }}
        />
      )}
      {drawer.mode !== "closed" && selectedBoardId != null && (
        <TaskDrawer
          mode={drawer.mode}
          taskId={drawer.mode === "view" ? drawer.taskId : undefined}
          boardId={selectedBoardId}
          onClose={closeDrawer}
          onChanged={onChanged}
          onCreated={(id) => setDrawer({ mode: "view", taskId: id })}
        />
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// NewBoardModal
// ---------------------------------------------------------------------------

function NewBoardModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (b: KanbanBoard) => void;
}) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [eligible, setEligible] = useState<number[]>([]);
  const { data: instances } = useInstances();

  const create = useMutation({
    mutationFn: () =>
      kanbanApi.createBoard({ name, description, eligible_instances: eligible }),
    onSuccess: (b) => {
      successToast("Board created");
      onCreated(b);
    },
    onError: (e) => errorToast("Create failed", e),
  });

  return (
    <ModalShell title="New Kanban Board" onClose={onClose}>
      <div className="space-y-4">
        <div>
          <label className="block text-xs text-gray-500 mb-1">Name *</label>
          <input
            autoFocus
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>
        <div>
          <label className="block text-xs text-gray-500 mb-1">Description</label>
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={2}
            className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>
        <div>
          <label className="block text-xs text-gray-500 mb-1">Eligible Instances *</label>
          <div className="border border-gray-300 rounded-md p-2 max-h-40 overflow-y-auto space-y-1">
            {instances?.map((i: any) => (
              <label key={i.id} className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={eligible.includes(i.id)}
                  onChange={(e) =>
                    setEligible((prev) =>
                      e.target.checked ? [...prev, i.id] : prev.filter((x) => x !== i.id),
                    )
                  }
                  className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                />
                {i.display_name || i.name}
              </label>
            ))}
          </div>
        </div>
      </div>
      <ModalFooter
        onCancel={onClose}
        onSubmit={() => create.mutate()}
        submitDisabled={!name || eligible.length === 0 || create.isPending}
        submitLabel={create.isPending ? "Creating..." : "Save"}
      />
    </ModalShell>
  );
}

// ---------------------------------------------------------------------------
// TaskDrawer — chat-style task view / create
// ---------------------------------------------------------------------------

function TaskDrawer({
  mode,
  taskId,
  boardId,
  onClose,
  onChanged,
  onCreated,
}: {
  mode: "create" | "view";
  taskId?: number;
  boardId: number;
  onClose: () => void;
  onChanged: () => void;
  onCreated: (id: number) => void;
}) {
  const qc = useQueryClient();
  const messagesEndRef = useRef<HTMLDivElement>(null);

  // ---- Shared state ----
  const [picked, setPicked] = useState(() => localStorage.getItem(LS_MODEL_KEY) ?? "");
  const [comment, setComment] = useState("");

  const isCreate = mode === "create";

  // ---- View-mode query ----
  const taskQ = useQuery({
    queryKey: ["kanban-task", taskId],
    queryFn: () => kanbanApi.getTask(taskId!),
    enabled: !isCreate && taskId != null,
    refetchInterval: 2000,
  });

  const t = taskQ.data?.task;
  const isDraft = t?.status === "draft";
  const isRunning = t?.status === "in_progress" || t?.status === "dispatching";
  const isFinished = t?.status === "done" || t?.status === "failed";

  // For draft view, load model from task
  const [draftModelLoaded, setDraftModelLoaded] = useState(false);
  useEffect(() => {
    if (t && isDraft && !draftModelLoaded) {
      if (t.evaluator_provider_key && t.evaluator_model) {
        setPicked(`${t.evaluator_provider_key}::${t.evaluator_model}`);
      }
      setDraftModelLoaded(true);
    }
  }, [t, isDraft, draftModelLoaded]);

  const handleModelChange = (v: string) => {
    setPicked(v);
    localStorage.setItem(LS_MODEL_KEY, v);
  };

  // Auto-scroll to bottom when comments change
  const prevCommentCount = useRef(0);
  useEffect(() => {
    const count = taskQ.data?.comments?.length ?? 0;
    if (count !== prevCommentCount.current) {
      prevCommentCount.current = count;
      messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [taskQ.data?.comments?.length]);

  // ---- Mutations ----
  const createMut = useMutation({
    mutationFn: (status: "draft" | "todo") => {
      const [providerKey, modelId] = picked.split("::");
      return kanbanApi.createTask(boardId, {
        title: "",
        description: comment,
        evaluator_provider_key: providerKey ?? "",
        evaluator_model: modelId ?? "",
        status,
      });
    },
    onSuccess: (task, status) => {
      successToast(status === "draft" ? "Draft saved" : "Task created");
      setComment("");
      onChanged();
      onCreated(task.id);
    },
    onError: (e) => errorToast("Create failed", e),
  });

  const startMut = useMutation({
    mutationFn: () => kanbanApi.startTask(taskId!),
    onSuccess: () => {
      successToast("Task started");
      qc.invalidateQueries({ queryKey: ["kanban-task", taskId] });
      onChanged();
    },
    onError: (e) => errorToast("Start failed", e),
  });

  const stopMut = useMutation({
    mutationFn: () => kanbanApi.stopTask(taskId!),
    onSuccess: () => {
      successToast("Stop requested");
      qc.invalidateQueries({ queryKey: ["kanban-task", taskId] });
      onChanged();
    },
    onError: (e) => errorToast("Stop failed", e),
  });

  const commentMut = useMutation({
    mutationFn: (body: string) => kanbanApi.addUserComment(taskId!, body),
    onSuccess: () => {
      setComment("");
      qc.invalidateQueries({ queryKey: ["kanban-task", taskId] });
    },
    onError: (e) => errorToast("Comment failed", e),
  });

  const reopenMut = useMutation({
    mutationFn: () => kanbanApi.reopenTask(taskId!),
    onSuccess: () => {
      successToast("Task reopened");
      qc.invalidateQueries({ queryKey: ["kanban-task", taskId] });
      onChanged();
    },
    onError: (e) => errorToast("Reopen failed", e),
  });

  const archiveMut = useMutation({
    mutationFn: () => kanbanApi.patchTask(taskId!, { status: "archived" }),
    onSuccess: () => {
      successToast("Task archived");
      onChanged();
      onClose();
    },
    onError: (e) => errorToast("Archive failed", e),
  });

  const deleteMut = useMutation({
    mutationFn: () => kanbanApi.deleteTask(taskId!),
    onSuccess: () => {
      successToast("Task deleted");
      onChanged();
      onClose();
    },
    onError: (e) => errorToast("Delete failed", e),
  });

  // Keyboard
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [onClose]);

  const handleSend = () => {
    if (!comment.trim()) return;
    if (isCreate) {
      createMut.mutate("todo");
    } else if (isDraft) {
      // Update description with new text and start
      kanbanApi
        .patchTask(taskId!, { description: comment })
        .then(() => startMut.mutate());
    } else if (isFinished) {
      // Comment and reopen
      commentMut.mutateAsync(comment).then(() => reopenMut.mutate());
    } else {
      commentMut.mutate(comment);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <div className="fixed inset-0 bg-black/40 z-50 flex justify-end" onClick={onClose}>
      <div
        className="w-[640px] max-w-full h-full bg-white shadow-xl flex flex-col"
        onClick={(e) => e.stopPropagation()}
      >
        {/* ---- Header ---- */}
        <div className="px-4 py-3 border-b border-gray-200">
          <div className="flex items-center justify-between mb-2">
            <div className="flex items-center gap-2 min-w-0">
              {t && <StatusPill status={t.status} />}
              <h2 className="text-sm font-semibold text-gray-900 truncate">
                {isCreate ? (
                  "New Task"
                ) : t ? (
                  <>
                    <span className="text-gray-400 font-mono mr-1">#{t.id}</span>
                    {t.title}
                  </>
                ) : (
                  "Loading..."
                )}
              </h2>
            </div>
            <div className="flex items-center gap-1.5 flex-shrink-0">
              {!isCreate && isDraft && (
                <button
                  type="button"
                  onClick={() => startMut.mutate()}
                  disabled={startMut.isPending}
                  title="Start working"
                  className="w-7 h-7 inline-flex items-center justify-center rounded-full text-green-600 border border-green-200 hover:bg-green-50 disabled:opacity-50 transition-colors"
                >
                  {startMut.isPending ? (
                    <Loader2 size={13} className="animate-spin" />
                  ) : (
                    <Play size={12} className="fill-current" />
                  )}
                </button>
              )}
              {!isCreate && isRunning && (
                <button
                  type="button"
                  onClick={() => stopMut.mutate()}
                  disabled={stopMut.isPending}
                  title="Stop task"
                  className="w-7 h-7 inline-flex items-center justify-center rounded-full text-red-600 border border-red-200 hover:bg-red-50 disabled:opacity-50 transition-colors"
                >
                  {stopMut.isPending ? (
                    <Loader2 size={13} className="animate-spin" />
                  ) : (
                    <Square size={11} className="fill-current" />
                  )}
                </button>
              )}
              {!isCreate && t?.status === "done" && (
                <button
                  type="button"
                  onClick={() => archiveMut.mutate()}
                  disabled={archiveMut.isPending}
                  title="Archive task"
                  className="w-7 h-7 inline-flex items-center justify-center rounded-full text-green-600 border border-green-200 hover:bg-green-50 disabled:opacity-50 transition-colors"
                >
                  {archiveMut.isPending ? (
                    <Loader2 size={13} className="animate-spin" />
                  ) : (
                    <Check size={14} />
                  )}
                </button>
              )}
              {!isCreate && t?.status === "failed" && (
                <button
                  type="button"
                  onClick={() => reopenMut.mutate()}
                  disabled={reopenMut.isPending}
                  title="Reopen task"
                  className="w-7 h-7 inline-flex items-center justify-center rounded-full text-gray-600 border border-gray-200 hover:bg-gray-50 disabled:opacity-50 transition-colors"
                >
                  {reopenMut.isPending ? (
                    <Loader2 size={13} className="animate-spin" />
                  ) : (
                    <RotateCcw size={12} />
                  )}
                </button>
              )}
              {!isCreate && (
                <button
                  type="button"
                  onClick={() => {
                    if (window.confirm("Delete this task?")) deleteMut.mutate();
                  }}
                  disabled={deleteMut.isPending}
                  title="Delete task"
                  className="w-7 h-7 inline-flex items-center justify-center rounded-full text-red-400 hover:text-red-600 border border-gray-200 hover:border-red-200 hover:bg-red-50 disabled:opacity-50 transition-colors"
                >
                  {deleteMut.isPending ? (
                    <Loader2 size={13} className="animate-spin" />
                  ) : (
                    <Trash2 size={12} />
                  )}
                </button>
              )}
              <button
                onClick={onClose}
                title="Close"
                className="w-7 h-7 inline-flex items-center justify-center rounded-full text-gray-400 hover:text-gray-600 hover:bg-gray-100 transition-colors"
              >
                <X size={15} />
              </button>
            </div>
          </div>
          {/* Model selector bar */}
          {(isCreate || isDraft) && (
            <ModelSelect value={picked} onChange={handleModelChange} />
          )}
        </div>

        {/* ---- Chat body ---- */}
        <div className="overflow-y-auto flex-1 px-4 py-4 space-y-3 bg-white">
          {isCreate ? (
            <div className="flex items-center justify-center h-full">
              <p className="text-sm text-gray-400">
                Describe what you want the agent to do...
              </p>
            </div>
          ) : t ? (
            <>
              {/* Task description as first "message" */}
              <div className="flex gap-2.5">
                <div className="w-7 h-7 rounded-full flex-shrink-0 flex items-center justify-center text-white text-[10px] font-bold bg-gray-400">
                  T
                </div>
                <div className="max-w-[80%] min-w-0">
                  <div className="flex items-baseline gap-2 mb-0.5">
                    <span className="text-xs font-medium text-gray-700">Task</span>
                    <span className="text-[10px] text-gray-400">
                      {formatTime(t.created_at)}
                    </span>
                  </div>
                  <div className="rounded-lg px-3 py-2 text-sm whitespace-pre-wrap break-words border bg-white border-gray-300">
                    {t.description}
                  </div>
                </div>
              </div>

              {/* Comments as chat bubbles */}
              {taskQ.data?.comments
                .filter((c) => c.body.trim() !== "")
                .map((c) => (
                  <ChatBubble key={c.id} comment={c} />
                ))}

              {/* Artifacts */}
              {taskQ.data && taskQ.data.artifacts.length > 0 && (
                <div className="ml-9 border border-gray-200 rounded-lg p-2.5">
                  <div className="text-xs font-medium text-gray-500 mb-1.5">Artifacts</div>
                  <ul className="space-y-0.5">
                    {taskQ.data.artifacts.map((a) => (
                      <li key={a.id} className="text-xs">
                        <a
                          href={`/api/v1/kanban/tasks/${taskId}/artifacts/${a.id}`}
                          className="text-blue-600 hover:text-blue-800"
                        >
                          {a.path}
                        </a>{" "}
                        <span className="text-gray-400">({a.size_bytes}b)</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )}

              {/* In-progress typing indicator */}
              {isRunning && (
                <WorkingIndicator comments={taskQ.data?.comments ?? []} />
              )}

              <div ref={messagesEndRef} />
            </>
          ) : (
            <div className="flex items-center justify-center h-full">
              <Loader2 size={20} className="animate-spin text-gray-300" />
            </div>
          )}
        </div>

        {/* ---- Input bar ---- */}
        <div className="px-4 py-3 border-t border-gray-200 bg-white">
          <div className="flex items-end gap-2">
            <textarea
              autoFocus={isCreate}
              value={comment}
              onChange={(e) => setComment(e.target.value)}
              onKeyDown={handleKeyDown}
              rows={2}
              className="flex-1 px-3 py-2 border border-gray-300 rounded-lg text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
              placeholder={
                isCreate
                  ? "Describe the task for the agent..."
                  : isDraft
                    ? "Update the task description..."
                    : "Add a comment..."
              }
            />
            <div className="flex flex-col gap-1">
              {isCreate && (
                <button
                  type="button"
                  onClick={() => {
                    if (comment.trim()) createMut.mutate("draft");
                  }}
                  disabled={!comment.trim() || createMut.isPending}
                  title="Save as draft"
                  className="w-8 h-8 inline-flex items-center justify-center rounded-lg text-gray-500 border border-gray-300 hover:bg-gray-50 disabled:opacity-40 transition-colors text-xs font-medium"
                >
                  D
                </button>
              )}
              <button
                type="button"
                onClick={handleSend}
                disabled={
                  !comment.trim() ||
                  createMut.isPending ||
                  commentMut.isPending ||
                  startMut.isPending
                }
                title={
                  isCreate
                    ? "Start working"
                    : isDraft
                      ? "Update & start"
                      : isFinished
                        ? "Comment & reopen"
                        : "Send comment"
                }
                className="w-8 h-8 inline-flex items-center justify-center rounded-lg text-white bg-blue-600 hover:bg-blue-700 disabled:opacity-50 transition-colors"
              >
                <Send size={14} />
              </button>
            </div>
          </div>
          {isFinished && comment.trim() && (
            <p className="text-[10px] text-gray-400 mt-1">
              Sending a comment will reopen the task.
            </p>
          )}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Shared modal components
// ---------------------------------------------------------------------------

function ModalShell({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 bg-black/40 z-50 flex items-center justify-center"
      onClick={onClose}
    >
      <div
        className="bg-white rounded-lg shadow-xl p-6 w-full max-w-md mx-4"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="text-base font-semibold text-gray-900 mb-4">{title}</h2>
        {children}
      </div>
    </div>
  );
}

function ModalFooter({
  onCancel,
  onSubmit,
  submitDisabled,
  submitLabel = "Save",
}: {
  onCancel: () => void;
  onSubmit: () => void;
  submitDisabled: boolean;
  submitLabel?: string;
}) {
  return (
    <div className="flex items-center justify-between mt-6">
      <button
        type="button"
        onClick={onCancel}
        className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
      >
        Cancel
      </button>
      <button
        type="button"
        onClick={onSubmit}
        disabled={submitDisabled}
        className="px-4 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
      >
        {submitLabel}
      </button>
    </div>
  );
}
