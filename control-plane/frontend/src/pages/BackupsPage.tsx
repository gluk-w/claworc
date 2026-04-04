import { useState, useMemo, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { Trash2, Download, Loader2, Pencil } from "lucide-react";
import FolderInput from "@/components/FolderInput";
import MultiSelect, { type MultiSelectOption } from "@/components/MultiSelect";
import SingleSelect, { type SingleSelectOption } from "@/components/SingleSelect";
import {
  useAllBackups,
  useCreateBackup,
  useDeleteBackup,
  useBackupSchedules,
  useCreateSchedule,
  useUpdateSchedule,
  useDeleteSchedule,
} from "@/hooks/useBackups";
import { useInstances } from "@/hooks/useInstances";
import { getBackupDownloadUrl } from "@/api/backups";
import type { BackupSchedule } from "@/types/backup";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + " " + sizes[i];
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleString();
}

function cronToHuman(cron: string): string {
  const parts = cron.split(" ");
  if (parts.length !== 5) return cron;
  const [min, hour, dom, , dow] = parts;
  if (dom === "1" && dow === "*") return `Monthly on 1st at ${hour}:${min.padStart(2, "0")}`;
  if (dow !== "*") {
    const days = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
    return `Weekly ${days[Number(dow)] || dow} at ${hour}:${min.padStart(2, "0")}`;
  }
  if (hour.startsWith("*/")) return `Every ${hour.slice(2)} hours`;
  return `Daily at ${hour}:${min.padStart(2, "0")}`;
}

const CRON_PRESETS = [
  { label: "Daily at 2:00 AM", value: "0 2 * * *" },
  { label: "Weekly Sunday at 2:00 AM", value: "0 2 * * 0" },
  { label: "Monthly 1st at 2:00 AM", value: "0 2 1 * *" },
  { label: "Every 6 hours", value: "0 */6 * * *" },
];

export default function BackupsPage() {
  const [searchParams] = useSearchParams();
  const instanceFilter = searchParams.get("instance") || "";

  const { data: backups = [], isLoading: backupsLoading } = useAllBackups();
  const { data: schedules = [], isLoading: schedulesLoading } = useBackupSchedules();
  const { data: instances = [] } = useInstances();

  const [showCreateBackup, setShowCreateBackup] = useState(false);
  const [showCreateSchedule, setShowCreateSchedule] = useState(false);
  const [editSchedule, setEditSchedule] = useState<BackupSchedule | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null);
  const [confirmDeleteSchedule, setConfirmDeleteSchedule] = useState<number | null>(null);

  const filteredBackups = instanceFilter
    ? backups.filter((b) => b.instance_name === instanceFilter)
    : backups;

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-gray-900">Backups</h1>
        <button
          onClick={() => setShowCreateBackup(true)}
          className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
        >
          Create Backup
        </button>
      </div>

      <div className="space-y-8 max-w-4xl">
        {/* Schedules Section */}
        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-sm font-medium text-gray-900">Schedules</h3>
            <button
              type="button"
              onClick={() => setShowCreateSchedule(true)}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
            >
              Schedule Backups
            </button>
          </div>

          {schedulesLoading ? (
            <p className="text-xs text-gray-400">Loading...</p>
          ) : schedules.length === 0 ? (
            <p className="text-sm text-gray-400 italic">No backup schedules configured.</p>
          ) : (
            <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
              <table className="w-full text-sm">
                <thead className="bg-gray-50 border-b border-gray-200">
                  <tr>
                    <th className="text-left px-4 py-3 font-medium text-gray-600">Instances</th>
                    <th className="text-left px-4 py-3 font-medium text-gray-600">Schedule</th>
                    <th className="text-left px-4 py-3 font-medium text-gray-600">Paths</th>
                    <th className="text-right px-4 py-3 font-medium text-gray-600">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {schedules.map((s) => (
                    <tr key={s.id} className="border-b border-gray-100 last:border-0">
                      <td className="px-4 py-3 text-gray-900">
                        <ScheduleInstances instanceIDs={s.instance_ids} instances={instances} />
                      </td>
                      <td className="px-4 py-3 text-gray-500">{cronToHuman(s.cron_expression)}</td>
                      <td className="px-4 py-3 text-gray-500">
                        <SchedulePaths paths={s.paths} />
                      </td>
                      <td className="px-4 py-3 text-right">
                        <div className="flex items-center justify-end gap-1">
                          <button
                            onClick={() => setEditSchedule(s)}
                            className="p-1 text-gray-400 hover:text-gray-600 transition-colors"
                            title="Edit"
                          >
                            <Pencil size={14} />
                          </button>
                          {confirmDeleteSchedule === s.id ? (
                            <ConfirmDeleteScheduleInline
                              id={s.id}
                              onCancel={() => setConfirmDeleteSchedule(null)}
                            />
                          ) : (
                            <button
                              onClick={() => setConfirmDeleteSchedule(s.id)}
                              className="p-1 text-gray-400 hover:text-red-600 transition-colors"
                              title="Delete"
                            >
                              <Trash2 size={14} />
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>

        {/* Backups List Section */}
        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h3 className="text-sm font-medium text-gray-900 mb-4">
            Backups
            {instanceFilter && (
              <span className="text-gray-400 font-normal"> — {instanceFilter}</span>
            )}
          </h3>

          {backupsLoading ? (
            <p className="text-xs text-gray-400">Loading...</p>
          ) : filteredBackups.length === 0 ? (
            <p className="text-sm text-gray-400 italic">No backups found.</p>
          ) : (
            <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
              <table className="w-full text-sm">
                <thead className="bg-gray-50 border-b border-gray-200">
                  <tr>
                    <th className="text-left px-4 py-3 font-medium text-gray-600">Name</th>
                    <th className="text-left px-4 py-3 font-medium text-gray-600">Instance</th>
                    <th className="text-left px-4 py-3 font-medium text-gray-600">Status</th>
                    <th className="text-left px-4 py-3 font-medium text-gray-600">Size</th>
                    <th className="text-left px-4 py-3 font-medium text-gray-600">Date</th>
                    <th className="text-right px-4 py-3 font-medium text-gray-600">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredBackups.map((b) => (
                    <tr key={b.id} className="border-b border-gray-100 last:border-0">
                      <td className="px-4 py-3 text-gray-900">
                        {b.instance_name}-{new Date(b.created_at).toISOString().replace(/[T:]/g, "-").slice(0, 19)}
                      </td>
                      <td className="px-4 py-3 text-gray-500">{b.instance_name}</td>
                      <td className="px-4 py-3">
                        <StatusPill status={b.status} />
                      </td>
                      <td className="px-4 py-3 text-gray-500">
                        {b.status === "completed" ? formatBytes(b.size_bytes) : "—"}
                      </td>
                      <td className="px-4 py-3 text-gray-500">{formatDate(b.created_at)}</td>
                      <td className="px-4 py-3 text-right">
                        <div className="flex items-center justify-end gap-1">
                          {b.status === "completed" && (
                            <a
                              href={getBackupDownloadUrl(b.id)}
                              className="p-1 text-gray-400 hover:text-gray-600 transition-colors"
                              title="Download"
                            >
                              <Download size={14} />
                            </a>
                          )}
                          {confirmDelete === b.id ? (
                            <ConfirmDeleteBackupInline
                              id={b.id}
                              onCancel={() => setConfirmDelete(null)}
                            />
                          ) : (
                            <button
                              onClick={() => setConfirmDelete(b.id)}
                              className="p-1 text-gray-400 hover:text-red-600 transition-colors"
                              title="Delete"
                              disabled={b.status === "running"}
                            >
                              <Trash2 size={14} />
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </div>

      {showCreateBackup && (
        <CreateBackupModal
          instances={instances}
          defaultInstance={instanceFilter}
          onClose={() => setShowCreateBackup(false)}
        />
      )}

      {showCreateSchedule && (
        <ScheduleModal
          instances={instances}
          onClose={() => setShowCreateSchedule(false)}
        />
      )}

      {editSchedule && (
        <ScheduleModal
          instances={instances}
          schedule={editSchedule}
          onClose={() => setEditSchedule(null)}
        />
      )}
    </div>
  );
}

function StatusPill({ status }: { status: string }) {
  const colors: Record<string, string> = {
    running: "bg-yellow-100 text-yellow-800",
    completed: "bg-green-100 text-green-800",
    failed: "bg-red-100 text-red-800",
  };
  return (
    <span className={`inline-flex items-center gap-1.5 px-2.5 py-0.5 rounded-full text-xs font-medium ${colors[status] || "bg-gray-100 text-gray-600"}`}>
      {status === "running" && <Loader2 size={10} className="animate-spin" />}
      {status}
    </span>
  );
}

function ScheduleInstances({
  instanceIDs,
  instances,
}: {
  instanceIDs: string;
  instances: { id: number; display_name: string }[];
}) {
  if (instanceIDs === "ALL") {
    return <span className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full bg-purple-50 text-purple-700">All Instances</span>;
  }
  try {
    const ids: number[] = JSON.parse(instanceIDs);
    return (
      <div className="flex flex-wrap gap-1">
        {ids.map((id) => {
          const inst = instances.find((i) => i.id === id);
          return (
            <span key={id} className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full bg-gray-100 text-gray-600">
              {inst?.display_name || `#${id}`}
            </span>
          );
        })}
      </div>
    );
  } catch {
    return <span className="text-gray-500">{instanceIDs}</span>;
  }
}

function SchedulePaths({ paths }: { paths: string }) {
  try {
    const arr: string[] = JSON.parse(paths);
    return <span>{arr.join(", ")}</span>;
  } catch {
    return <span>{paths}</span>;
  }
}

function ConfirmDeleteBackupInline({
  id,
  onCancel,
}: {
  id: number;
  onCancel: () => void;
}) {
  const deleteMutation = useDeleteBackup();
  return (
    <div className="flex items-center gap-1">
      <button
        onClick={() => {
          deleteMutation.mutate(id);
          onCancel();
        }}
        className="px-2 py-0.5 text-xs text-white bg-red-600 rounded hover:bg-red-700"
      >
        Confirm
      </button>
      <button
        onClick={onCancel}
        className="px-2 py-0.5 text-xs text-gray-600 border border-gray-300 rounded hover:bg-gray-50"
      >
        Cancel
      </button>
    </div>
  );
}

function ConfirmDeleteScheduleInline({
  id,
  onCancel,
}: {
  id: number;
  onCancel: () => void;
}) {
  const deleteMutation = useDeleteSchedule();
  return (
    <div className="flex items-center gap-1">
      <button
        onClick={() => {
          deleteMutation.mutate(id);
          onCancel();
        }}
        className="px-2 py-0.5 text-xs text-white bg-red-600 rounded hover:bg-red-700"
      >
        Confirm
      </button>
      <button
        onClick={onCancel}
        className="px-2 py-0.5 text-xs text-gray-600 border border-gray-300 rounded hover:bg-gray-50"
      >
        Cancel
      </button>
    </div>
  );
}

function CreateBackupModal({
  instances,
  defaultInstance,
  onClose,
}: {
  instances: { id: number; name: string; display_name: string }[];
  defaultInstance?: string;
  onClose: () => void;
}) {
  const defaultInst = instances.find((i) => i.name === defaultInstance);
  const instanceOptions = useMemo<SingleSelectOption[]>(
    () => instances.map((i) => ({ value: i.id, label: i.display_name })),
    [instances],
  );
  const [selectedInstance, setSelectedInstance] = useState<SingleSelectOption | null>(
    defaultInst ? { value: defaultInst.id, label: defaultInst.display_name } : null,
  );
  const [paths, setPaths] = useState<string[]>(["HOME"]);
  const [note, setNote] = useState("");
  const createMutation = useCreateBackup();

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!selectedInstance) return;
    const cleanPaths = paths.filter((p) => p.trim() !== "");
    createMutation.mutate(
      { instanceId: selectedInstance.value, paths: cleanPaths.length > 0 ? cleanPaths : undefined, note: note || undefined },
      { onSuccess: () => onClose() },
    );
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onClose();
  };

  return (
    <div className="fixed inset-0 bg-black/40 z-50 flex items-center justify-center" onKeyDown={handleKeyDown}>
      <form onSubmit={handleSubmit} className="bg-white rounded-lg shadow-xl p-6 w-full max-w-md mx-4">
        <h2 className="text-base font-semibold text-gray-900 mb-4">Create Backup</h2>

        <div className="space-y-4">
          <div>
            <label className="block text-xs text-gray-500 mb-1">Instance *</label>
            <SingleSelect
              options={instanceOptions}
              value={selectedInstance}
              onChange={(val) => setSelectedInstance(val)}
              placeholder="Select instance..."
            />
          </div>

          <div>
            <label className="block text-xs text-gray-500 mb-1">Folders to backup</label>
            <FolderInput value={paths} onChange={setPaths} />
          </div>

          <div>
            <label className="block text-xs text-gray-500 mb-1">Note</label>
            <input
              type="text"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="Optional note"
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
        </div>

        <div className="flex items-center justify-between mt-6">
          <button
            type="button"
            onClick={onClose}
            className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={!selectedInstance || createMutation.isPending}
            className="px-4 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {createMutation.isPending ? "Creating..." : "Create Backup"}
          </button>
        </div>
      </form>
    </div>
  );
}

function ScheduleModal({
  instances,
  schedule,
  onClose,
}: {
  instances: { id: number; name: string; display_name: string }[];
  schedule?: BackupSchedule;
  onClose: () => void;
}) {
  const isEdit = !!schedule;

  const parseInstanceIDs = (): { all: boolean; ids: number[] } => {
    if (!schedule) return { all: false, ids: [] };
    if (schedule.instance_ids === "ALL") return { all: true, ids: [] };
    try {
      return { all: false, ids: JSON.parse(schedule.instance_ids) };
    } catch {
      return { all: false, ids: [] };
    }
  };

  const parsePaths = (): string[] => {
    if (!schedule) return ["HOME"];
    try {
      const arr = JSON.parse(schedule.paths);
      return arr.length > 0 ? arr : ["HOME"];
    } catch {
      return ["HOME"];
    }
  };

  const initial = parseInstanceIDs();
  const [allInstances, setAllInstances] = useState(initial.all);
  const [selectedOptions, setSelectedOptions] = useState<MultiSelectOption[]>(
    initial.ids
      .map((id) => {
        const inst = instances.find((i) => i.id === id);
        return inst ? { value: inst.id, label: inst.display_name } : null;
      })
      .filter((o): o is MultiSelectOption => o !== null),
  );
  const [cronExpression, setCronExpression] = useState(schedule?.cron_expression || "0 2 * * *");
  const [paths, setPaths] = useState<string[]>(parsePaths());

  const instanceOptions = useMemo<MultiSelectOption[]>(
    () => instances.map((i) => ({ value: i.id, label: i.display_name })),
    [instances],
  );

  const createMutation = useCreateSchedule();
  const updateMutation = useUpdateSchedule();

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    const instanceIdsValue = allInstances
      ? "ALL"
      : JSON.stringify(selectedOptions.map((o) => o.value));
    const cleanPaths = paths.filter((p) => p.trim() !== "");

    if (isEdit && schedule) {
      updateMutation.mutate(
        {
          id: schedule.id,
          instance_ids: instanceIdsValue,
          cron_expression: cronExpression,
          paths: cleanPaths.length > 0 ? cleanPaths : ["HOME"],
        },
        { onSuccess: () => onClose() },
      );
    } else {
      createMutation.mutate(
        {
          instance_ids: instanceIdsValue,
          cron_expression: cronExpression,
          paths: cleanPaths.length > 0 ? cleanPaths : ["HOME"],
        },
        { onSuccess: () => onClose() },
      );
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onClose();
  };

  const isPending = createMutation.isPending || updateMutation.isPending;
  const canSubmit = (allInstances || selectedOptions.length > 0) && cronExpression.trim() !== "";

  return (
    <div className="fixed inset-0 bg-black/40 z-50 flex items-center justify-center" onKeyDown={handleKeyDown}>
      <form onSubmit={handleSubmit} className="bg-white rounded-lg shadow-xl p-6 w-full max-w-md mx-4">
        <h2 className="text-base font-semibold text-gray-900 mb-4">
          {isEdit ? "Edit Schedule" : "Schedule Backups"}
        </h2>

        <div className="space-y-4">
          <div>
            <label className="block text-xs text-gray-500 mb-1">Instances *</label>
            <div className="space-y-2">
              <label className="flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={allInstances}
                  onChange={(e) => setAllInstances(e.target.checked)}
                  className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                />
                <span className="text-sm text-gray-700">All Instances</span>
              </label>
              {!allInstances && (
                <MultiSelect
                  options={instanceOptions}
                  value={selectedOptions}
                  onChange={(val) => setSelectedOptions([...val])}
                  placeholder="Select instances..."
                />
              )}
            </div>
          </div>

          <div>
            <label className="block text-xs text-gray-500 mb-1">Schedule *</label>
            <div className="flex gap-2">
              <input
                type="text"
                value={cronExpression}
                onChange={(e) => setCronExpression(e.target.value)}
                placeholder="0 2 * * *"
                className="flex-1 px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
              />
              <select
                value=""
                onChange={(e) => {
                  if (e.target.value) setCronExpression(e.target.value);
                }}
                className="px-2 py-1.5 border border-gray-300 rounded-md text-xs text-gray-600 focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white"
              >
                <option value="" disabled>Presets</option>
                {CRON_PRESETS.map((p) => (
                  <option key={p.value} value={p.value}>{p.label}</option>
                ))}
              </select>
            </div>
            <p className="text-xs text-gray-400 mt-1">{cronToHuman(cronExpression)}</p>
          </div>

          <div>
            <label className="block text-xs text-gray-500 mb-1">Folders to backup</label>
            <FolderInput value={paths} onChange={setPaths} />
          </div>
        </div>

        <div className="flex items-center justify-between mt-6">
          <button
            type="button"
            onClick={onClose}
            className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Cancel
          </button>
          <button
            type="submit"
            disabled={!canSubmit || isPending}
            className="px-4 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {isPending ? "Saving..." : isEdit ? "Save" : "Create Schedule"}
          </button>
        </div>
      </form>
    </div>
  );
}
