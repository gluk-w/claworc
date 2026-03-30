import { useState } from "react";
import { Download, Trash2, RotateCcw, Loader2 } from "lucide-react";
import {
  useInstanceBackups,
  useCreateBackup,
  useDeleteBackup,
  useRestoreBackup,
} from "@/hooks/useBackups";
import { getBackupDownloadUrl } from "@/api/backups";
import type { Backup } from "@/types/backup";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleString();
}

interface Props {
  instanceId: number;
}

export default function BackupPanel({ instanceId }: Props) {
  const { data: backups, isLoading } = useInstanceBackups(instanceId);
  const createMutation = useCreateBackup(instanceId);
  const deleteMutation = useDeleteBackup(instanceId);
  const restoreMutation = useRestoreBackup(instanceId);
  const [note, setNote] = useState("");
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null);
  const [confirmRestore, setConfirmRestore] = useState<number | null>(null);

  const handleCreate = (type: "full" | "incremental") => {
    createMutation.mutate({ type, note: note || undefined });
    setNote("");
  };

  return (
    <div className="space-y-6">
      {/* Create backup */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-4">Create Backup</h3>
        <div className="flex items-end gap-4">
          <div className="flex-1">
            <label className="block text-xs text-gray-500 mb-1">Note (optional)</label>
            <input
              type="text"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="e.g. Before upgrading packages"
              className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
            />
          </div>
          <button
            onClick={() => handleCreate("full")}
            disabled={createMutation.isPending}
            className="px-4 py-2 bg-blue-600 text-white text-sm font-medium rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            Full Backup
          </button>
          <button
            onClick={() => handleCreate("incremental")}
            disabled={createMutation.isPending}
            className="px-4 py-2 bg-gray-600 text-white text-sm font-medium rounded-md hover:bg-gray-700 disabled:opacity-50"
          >
            Incremental
          </button>
        </div>
      </div>

      {/* Backup list */}
      <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
        <div className="px-6 py-4 border-b border-gray-200">
          <h3 className="text-sm font-medium text-gray-900">Backups</h3>
        </div>

        {isLoading ? (
          <div className="p-6 text-center text-sm text-gray-500">Loading...</div>
        ) : !backups?.length ? (
          <div className="p-6 text-center text-sm text-gray-500">No backups yet</div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-gray-50 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                <th className="px-6 py-3">Type</th>
                <th className="px-6 py-3">Status</th>
                <th className="px-6 py-3">Size</th>
                <th className="px-6 py-3">Date</th>
                <th className="px-6 py-3">Note</th>
                <th className="px-6 py-3 text-right">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-200">
              {backups.map((b: Backup) => (
                <tr key={b.id} className="hover:bg-gray-50">
                  <td className="px-6 py-3">
                    <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
                      b.type === "full"
                        ? "bg-blue-100 text-blue-800"
                        : "bg-purple-100 text-purple-800"
                    }`}>
                      {b.type}
                    </span>
                  </td>
                  <td className="px-6 py-3">
                    <StatusPill status={b.status} error={b.error_message} />
                  </td>
                  <td className="px-6 py-3 text-gray-600">
                    {b.status === "completed" ? formatBytes(b.size_bytes) : "-"}
                  </td>
                  <td className="px-6 py-3 text-gray-600">{formatDate(b.created_at)}</td>
                  <td className="px-6 py-3 text-gray-600 max-w-[200px] truncate">{b.note || "-"}</td>
                  <td className="px-6 py-3">
                    <div className="flex items-center justify-end gap-2">
                      {b.status === "completed" && (
                        <>
                          <a
                            href={getBackupDownloadUrl(b.id)}
                            className="p-1 text-gray-400 hover:text-gray-600"
                            title="Download"
                          >
                            <Download className="w-4 h-4" />
                          </a>
                          {confirmRestore === b.id ? (
                            <span className="flex items-center gap-1">
                              <button
                                onClick={() => { restoreMutation.mutate(b.id); setConfirmRestore(null); }}
                                className="text-xs text-orange-600 hover:text-orange-800 font-medium"
                              >
                                Confirm
                              </button>
                              <button
                                onClick={() => setConfirmRestore(null)}
                                className="text-xs text-gray-500 hover:text-gray-700"
                              >
                                Cancel
                              </button>
                            </span>
                          ) : (
                            <button
                              onClick={() => setConfirmRestore(b.id)}
                              className="p-1 text-gray-400 hover:text-orange-600"
                              title="Restore"
                            >
                              <RotateCcw className="w-4 h-4" />
                            </button>
                          )}
                        </>
                      )}
                      {confirmDelete === b.id ? (
                        <span className="flex items-center gap-1">
                          <button
                            onClick={() => { deleteMutation.mutate(b.id); setConfirmDelete(null); }}
                            className="text-xs text-red-600 hover:text-red-800 font-medium"
                          >
                            Delete
                          </button>
                          <button
                            onClick={() => setConfirmDelete(null)}
                            className="text-xs text-gray-500 hover:text-gray-700"
                          >
                            Cancel
                          </button>
                        </span>
                      ) : (
                        <button
                          onClick={() => setConfirmDelete(b.id)}
                          className="p-1 text-gray-400 hover:text-red-600"
                          title="Delete"
                          disabled={b.status === "running"}
                        >
                          <Trash2 className="w-4 h-4" />
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function StatusPill({ status, error }: { status: string; error?: string }) {
  const styles: Record<string, string> = {
    running: "bg-yellow-100 text-yellow-800",
    completed: "bg-green-100 text-green-800",
    failed: "bg-red-100 text-red-800",
  };
  return (
    <span
      className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium ${styles[status] || "bg-gray-100 text-gray-800"}`}
      title={error || undefined}
    >
      {status === "running" && <Loader2 className="w-3 h-3 animate-spin" />}
      {status}
    </span>
  );
}
