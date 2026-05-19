import { useEffect, useMemo, useRef, useState } from "react";
import { Copy, Eye, EyeOff, Globe, ListCollapse, Lock, Plus, RefreshCw, Trash2, X, ChevronDown } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  useCreateInstanceWebhookKey,
  useDeleteInstanceWebhookKey,
  useInstanceWebhook,
  useRegenerateInstanceWebhookKey,
  useUpdateInstanceWebhookKey,
} from "@/hooks/useWebhook";
import { fetchInstanceWebhookLogs } from "@/api/webhooks";
import type { WebhookApiKey, WebhookLog } from "@/types/webhook";
import { successToast, errorToast } from "@/utils/toast";
import { buildWebhookSnippet, type SnippetVariant } from "@/utils/webhookSnippets";
import ConfirmDialog from "@/components/ConfirmDialog";

interface Props {
  instanceId: number;
}

interface EditRow {
  id: number;
  keyId: number;
  token: string;
  label: string;
  isPrivate: boolean;
  lastUsedAt: string | null | undefined;
  reveal: boolean;
}

let rowSeq = 0;
const nextRowId = () => ++rowSeq;

function rowsFromKeys(keys: WebhookApiKey[]): EditRow[] {
  return keys.map((k) => ({
    id: nextRowId(),
    keyId: k.id,
    token: k.key,
    label: k.label ?? "",
    isPrivate: k.is_private,
    lastUsedAt: k.last_used_at,
    reveal: false,
  }));
}

function maskToken(t: string): string {
  if (!t) return "";
  if (t.length <= 8) return "*".repeat(t.length);
  return `${t.slice(0, 4)}${"*".repeat(Math.max(8, t.length - 8))}${t.slice(-4)}`;
}

const copyText = (text: string) => {
  navigator.clipboard?.writeText(text).then(
    () => successToast("Copied to clipboard"),
    () => errorToast("Copy failed"),
  );
};

export default function WebhookSection({ instanceId }: Props) {
  const { data, isLoading, error } = useInstanceWebhook(instanceId);
  const createMut = useCreateInstanceWebhookKey(instanceId);
  const updateMut = useUpdateInstanceWebhookKey(instanceId);
  const regenMut = useRegenerateInstanceWebhookKey(instanceId);
  const deleteMut = useDeleteInstanceWebhookKey(instanceId);

  const [rows, setRows] = useState<EditRow[]>([]);
  const [eventsOpen, setEventsOpen] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<EditRow | null>(null);
  const [pendingRegenerate, setPendingRegenerate] = useState<EditRow | null>(null);

  useEffect(() => {
    if (data?.keys) {
      setRows(rowsFromKeys(data.keys));
    }
  }, [data?.keys]);

  const browserOrigin = useMemo(() => {
    if (typeof window === "undefined") return "";
    return window.location.origin;
  }, []);
  const publicURL = data?.instance_uuid ? `${browserOrigin}/webhooks/${data.instance_uuid}` : "";
  const privateURL = data?.private_url ?? "";

  function patchRow(rowId: number, patch: Partial<EditRow>) {
    setRows((rs) => rs.map((r) => (r.id === rowId ? { ...r, ...patch } : r)));
  }

  async function togglePrivate(row: EditRow, isPrivate: boolean) {
    try {
      await updateMut.mutateAsync({ keyId: row.keyId, payload: { is_private: isPrivate } });
    } catch (e: any) {
      errorToast("Failed to update visibility", e);
    }
  }

  async function confirmRegenerate(row: EditRow) {
    setPendingRegenerate(null);
    try {
      await regenMut.mutateAsync({ keyId: row.keyId, payload: {} });
      successToast("Key regenerated");
    } catch (e: any) {
      errorToast("Failed to regenerate", e);
    }
  }

  async function confirmDelete(row: EditRow) {
    setPendingDelete(null);
    try {
      await deleteMut.mutateAsync(row.keyId);
      successToast("Webhook API key deleted");
    } catch (e: any) {
      errorToast("Failed to delete key", e);
    }
  }

  async function handleCreate(label: string, isPrivate: boolean) {
    try {
      await createMut.mutateAsync({ label, is_private: isPrivate });
      successToast("Key generated");
      setAddOpen(false);
    } catch (e: any) {
      errorToast("Failed to create key", e);
    }
  }

  if (isLoading) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900">Webhook</h3>
        <p className="text-xs text-gray-500 mt-2">Loading…</p>
      </div>
    );
  }
  if (error) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900">Webhook</h3>
        <p className="text-xs text-red-600 mt-2">Failed to load webhook config.</p>
      </div>
    );
  }

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-sm font-medium text-gray-900">Webhook</h3>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setEventsOpen(true)}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
            title="Webhook events"
          >
            <ListCollapse size={12} />
            Events
          </button>
        </div>
      </div>

      <p className="text-xs text-gray-500 mb-4">
        External callers (and other agents) can send a message to this agent over HTTP and receive its reply.
      </p>

      <div className="space-y-2 mb-5">
        <UrlRow label="Public URL" url={publicURL} />
        <UrlRow
          label="Private URL"
          url={privateURL}
          hint="Accessible only to other AI agents within Claworc"
        />
      </div>

      <div className="flex items-center justify-between mb-2">
        <h4 className="text-xs font-medium text-gray-900">API keys</h4>
        <button
          type="button"
          onClick={() => setAddOpen(true)}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
        >
          <Plus size={12} />
          Add key
        </button>
      </div>
      {rows.length === 0 ? (
        <p className="text-xs text-amber-700">
          No keys are configured, so the webhook is disabled.
        </p>
      ) : (
        <div className="divide-y divide-gray-100 border-y border-gray-100">
          {rows.map((row) => (
            <div key={row.id} className="py-1 flex flex-wrap items-center gap-2">
              <button
                type="button"
                onClick={() => togglePrivate(row, !row.isPrivate)}
                title={
                  row.isPrivate
                    ? "Accessible only to other AI agents within Claworc"
                    : "Public — accepted on the public webhook URL"
                }
                className={`p-1.5 ${row.isPrivate ? "text-blue-600 hover:text-blue-800" : "text-gray-400 hover:text-gray-700"}`}
              >
                {row.isPrivate ? <Lock size={14} /> : <Globe size={14} />}
              </button>
              <span
                className="flex-1 min-w-[160px] text-sm text-gray-700 px-2 py-1.5 truncate"
                title={row.label}
              >
                {row.label || <span className="text-gray-400 italic">no label</span>}
              </span>
              <div className="flex-1 min-w-[260px] flex items-center gap-1">
                <code className="flex-1 text-xs font-mono bg-gray-50 border border-gray-200 rounded px-2 py-1.5 truncate">
                  {row.reveal ? row.token : maskToken(row.token)}
                </code>
                <button
                  type="button"
                  title={row.reveal ? "Hide" : "Show"}
                  onClick={() => patchRow(row.id, { reveal: !row.reveal })}
                  className="p-1.5 text-gray-500 hover:text-gray-900"
                >
                  {row.reveal ? <EyeOff size={14} /> : <Eye size={14} />}
                </button>
                <button
                  type="button"
                  title="Copy"
                  onClick={() => copyText(row.token)}
                  className="p-1.5 text-gray-500 hover:text-gray-900"
                >
                  <Copy size={14} />
                </button>
              </div>
              <button
                type="button"
                title="(Re)generate"
                onClick={() => setPendingRegenerate(row)}
                className="p-1.5 text-gray-500 hover:text-gray-900"
              >
                <RefreshCw size={14} />
              </button>
              <button
                type="button"
                title="Delete"
                onClick={() => setPendingDelete(row)}
                className="p-1.5 text-gray-500 hover:text-red-600"
              >
                <Trash2 size={14} />
              </button>
            </div>
          ))}
        </div>
      )}

      {eventsOpen && (
        <EventsModal
          instanceId={instanceId}
          initial={data?.recent_logs ?? []}
          onClose={() => setEventsOpen(false)}
        />
      )}

      {addOpen && (
        <AddKeyModal
          onCancel={() => setAddOpen(false)}
          onSave={handleCreate}
          isSaving={createMut.isPending}
        />
      )}

      {pendingRegenerate && (
        <ConfirmDialog
          title="Regenerate API key?"
          message={`The current "${pendingRegenerate.label || "unlabeled"}" key will stop working immediately and a new value will replace it.`}
          confirmLabel="Regenerate"
          onConfirm={() => confirmRegenerate(pendingRegenerate)}
          onCancel={() => setPendingRegenerate(null)}
        />
      )}

      {pendingDelete && (
        <ConfirmDialog
          title="Delete API key?"
          message={`Callers using the "${pendingDelete.label || "unlabeled"}" key will start receiving 401. This cannot be undone.`}
          confirmLabel="Delete"
          onConfirm={() => confirmDelete(pendingDelete)}
          onCancel={() => setPendingDelete(null)}
        />
      )}
    </div>
  );
}

function UrlRow({ label, url, hint }: { label: string; url: string; hint?: string }) {
  return (
    <div>
      <div className="flex items-center gap-2">
        <span className="text-xs font-medium text-gray-700 w-44 shrink-0">{label}</span>
        <code className="flex-1 text-xs font-mono bg-gray-50 border border-gray-200 rounded px-2 py-1.5 truncate">
          {url || "—"}
        </code>
        <CopyMenu url={url} />
      </div>
      {hint && <p className="text-[11px] text-gray-500 mt-0.5 pl-[180px]">{hint}</p>}
    </div>
  );
}

interface SnippetOption {
  variant: SnippetVariant;
  label: string;
}

const SNIPPET_OPTIONS: SnippetOption[] = [
  { variant: "url", label: "Copy URL" },
  { variant: "fetch", label: "Copy as fetch (JavaScript)" },
  { variant: "curl", label: "Copy as cURL" },
  { variant: "powershell", label: "Copy as PowerShell" },
];

function CopyMenu({ url }: { url: string }) {
  const [open, setOpen] = useState(false);
  const wrapperRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (!wrapperRef.current) return;
      if (!wrapperRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onClick);
    return () => document.removeEventListener("mousedown", onClick);
  }, [open]);

  return (
    <div className="relative" ref={wrapperRef}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        disabled={!url}
        className="inline-flex items-center gap-0.5 p-1.5 text-gray-500 hover:text-gray-900 disabled:opacity-40"
        title="Copy as…"
      >
        <Copy size={14} />
        <ChevronDown size={12} />
      </button>
      {open && (
        <div className="absolute right-0 mt-1 z-20 min-w-[220px] bg-white border border-gray-200 rounded-md shadow-lg py-1">
          {SNIPPET_OPTIONS.map((opt) => (
            <button
              key={opt.variant}
              type="button"
              onClick={() => {
                copyText(buildWebhookSnippet(opt.variant, url));
                setOpen(false);
              }}
              className="block w-full text-left px-3 py-1.5 text-xs text-gray-700 hover:bg-gray-50"
            >
              {opt.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function EventsModal({
  instanceId,
  initial,
  onClose,
}: {
  instanceId: number;
  initial: WebhookLog[];
  onClose: () => void;
}) {
  const { data: logs = initial } = useQuery({
    queryKey: ["instance-webhook-logs", instanceId],
    queryFn: () => fetchInstanceWebhookLogs(instanceId, { limit: 100 }),
    refetchInterval: 10000,
    initialData: initial,
  });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="bg-white rounded-xl shadow-xl w-full max-w-4xl mx-4 flex flex-col max-h-[80vh]">
        <div className="px-6 py-4 border-b border-gray-200 flex items-center justify-between">
          <div>
            <h2 className="text-base font-semibold text-gray-900">Webhook events</h2>
            <p className="text-sm text-gray-500 mt-1">Last 100 calls, refreshed every 10 seconds.</p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="p-1 text-gray-400 hover:text-gray-600"
            title="Close"
          >
            <X size={18} />
          </button>
        </div>
        <div className="overflow-y-auto flex-1 px-6 py-4">
          {logs.length === 0 ? (
            <p className="text-sm text-gray-400 italic">No calls recorded yet.</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-xs border border-gray-200 rounded-md">
                <thead className="bg-gray-50 text-gray-600">
                  <tr>
                    <th className="text-left px-2 py-1.5 font-medium">When</th>
                    <th className="text-left px-2 py-1.5 font-medium">Visibility</th>
                    <th className="text-left px-2 py-1.5 font-medium">Source IP</th>
                    <th className="text-left px-2 py-1.5 font-medium">Session</th>
                    <th className="text-left px-2 py-1.5 font-medium">Status</th>
                    <th className="text-left px-2 py-1.5 font-medium">Duration</th>
                    <th className="text-left px-2 py-1.5 font-medium">Bytes (in/out)</th>
                    <th className="text-left px-2 py-1.5 font-medium">Key</th>
                    <th className="text-left px-2 py-1.5 font-medium">Error</th>
                  </tr>
                </thead>
                <tbody>
                  {logs.map((l) => (
                    <tr key={l.id} className="border-t border-gray-100">
                      <td className="px-2 py-1.5 whitespace-nowrap">{new Date(l.created_at).toLocaleString()}</td>
                      <td className="px-2 py-1.5">{l.is_private ? "private" : "public"}</td>
                      <td className="px-2 py-1.5 font-mono">{l.source_ip}</td>
                      <td className="px-2 py-1.5 font-mono truncate max-w-[180px]" title={l.session_name}>{l.session_name}</td>
                      <td className={`px-2 py-1.5 font-medium ${l.status_code >= 400 ? "text-red-600" : "text-green-700"}`}>
                        {l.status_code || "—"}
                      </td>
                      <td className="px-2 py-1.5">{l.duration_ms} ms</td>
                      <td className="px-2 py-1.5">{l.request_bytes}/{l.response_bytes}</td>
                      <td className="px-2 py-1.5 font-mono">{l.key_last4 ? `…${l.key_last4}` : "—"}</td>
                      <td className="px-2 py-1.5 text-red-600 truncate max-w-[200px]" title={l.error_message}>
                        {l.error_message || ""}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function AddKeyModal({
  onCancel,
  onSave,
  isSaving,
}: {
  onCancel: () => void;
  onSave: (label: string, isPrivate: boolean) => void | Promise<void>;
  isSaving: boolean;
}) {
  const [label, setLabel] = useState("");
  const [isPrivate, setIsPrivate] = useState(false);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [onCancel]);

  const canSave = label.trim().length > 0 && !isSaving;
  const submit = (e?: React.FormEvent) => {
    e?.preventDefault();
    if (canSave) onSave(label.trim(), isPrivate);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <form onSubmit={submit} className="bg-white rounded-lg shadow-xl p-6 w-full max-w-md mx-4">
        <h2 className="text-base font-semibold text-gray-900 mb-4">Add API key</h2>
        <div className="space-y-4">
          <div>
            <label className="block text-xs text-gray-500 mb-1">Label *</label>
            <input
              type="text"
              autoFocus
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="e.g. zapier, partner-x"
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          <label className="flex items-center gap-2 text-sm text-gray-700 select-none">
            <input
              type="checkbox"
              checked={isPrivate}
              onChange={(e) => setIsPrivate(e.target.checked)}
              className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
            />
            Accessible only to other AI agents within Claworc
          </label>
        </div>
        <div className="flex items-center justify-between mt-6">
          <button
            type="button"
            onClick={onCancel}
            className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={!canSave}
            className="px-4 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {isSaving ? "Saving..." : "Save"}
          </button>
        </div>
      </form>
    </div>
  );
}
