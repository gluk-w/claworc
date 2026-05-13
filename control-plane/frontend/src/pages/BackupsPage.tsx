import { useEffect, useState, useMemo, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { Trash2, Download, Loader2, Pencil, Square, ChevronLeft, ChevronRight } from "lucide-react";
import FolderInput from "@/components/FolderInput";
import InstanceTeamPicker from "@/components/InstanceTeamPicker";
import { useTeam } from "@/contexts/TeamContext";
import type { Instance } from "@/types/instance";
import {
  useAllBackups,
  useCreateBackup,
  useDeleteBackup,
  useCancelBackup,
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

  const PAGE_SIZE_OPTIONS = [25, 50, 100];
  const [pageSize, setPageSize] = useState<number>(() => {
    const stored = Number(localStorage.getItem("backups.pageSize"));
    return PAGE_SIZE_OPTIONS.includes(stored) ? stored : 50;
  });
  const [page, setPage] = useState(0);

  useEffect(() => {
    localStorage.setItem("backups.pageSize", String(pageSize));
  }, [pageSize]);

  useEffect(() => {
    setPage(0);
  }, [instanceFilter, pageSize]);

  const { data: backupsPage, isLoading: backupsLoading } = useAllBackups({
    limit: pageSize,
    offset: page * pageSize,
    instance: instanceFilter || undefined,
  });
  const backups = backupsPage?.backups ?? [];
  const total = backupsPage?.total ?? 0;
  const { data: schedules = [], isLoading: schedulesLoading } = useBackupSchedules();
  const { data: instances = [] } = useInstances();
  const { teams } = useTeam();

  const [showCreateBackup, setShowCreateBackup] = useState(false);
  const [showCreateSchedule, setShowCreateSchedule] = useState(false);
  const [editSchedule, setEditSchedule] = useState<BackupSchedule | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null);
  const [confirmDeleteSchedule, setConfirmDeleteSchedule] = useState<number | null>(null);

  const filteredBackups = backups;
  const offset = page * pageSize;
  const rangeStart = total === 0 ? 0 : offset + 1;
  const rangeEnd = Math.min(offset + backups.length, total);
  const canPrev = page > 0;
  const canNext = offset + backups.length < total;

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-gray-900">Backups</h1>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setShowCreateSchedule(true)}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Schedule Backups
          </button>
          <button
            onClick={() => setShowCreateBackup(true)}
            className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
          >
            Create Backup
          </button>
        </div>
      </div>

      <div className="space-y-8">
        {/* Schedules Section */}
        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h3 className="text-sm font-medium text-gray-900 mb-4">Schedules</h3>

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
                    <th className="text-left px-4 py-3 font-medium text-gray-600">Retention</th>
                    <th className="text-right px-4 py-3 font-medium text-gray-600">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {schedules.map((s) => (
                    <tr key={s.id} className="border-b border-gray-100 last:border-0">
                      <td className="px-4 py-3 text-gray-900">
                        <ScheduleInstances instanceIDs={s.instance_ids} teamIDs={s.team_ids} instances={instances} teams={teams} />
                      </td>
                      <td className="px-4 py-3 text-gray-500">{cronToHuman(s.cron_expression)}</td>
                      <td className="px-4 py-3 text-gray-500">
                        <SchedulePaths paths={s.paths} />
                      </td>
                      <td className="px-4 py-3 text-gray-500">
                        {s.retention_days > 0 ? `${s.retention_days}d` : "—"}
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

          {backupsLoading && !backupsPage ? (
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
                          {b.status === "running" ? (
                            <CancelRunningBackupButton id={b.id} />
                          ) : confirmDelete === b.id ? (
                            <ConfirmDeleteBackupInline
                              id={b.id}
                              onCancel={() => setConfirmDelete(null)}
                            />
                          ) : (
                            <button
                              onClick={() => setConfirmDelete(b.id)}
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

          {total > 0 && (
            <div className="flex items-center justify-between mt-3 text-xs text-gray-500">
              <div className="flex items-center gap-2">
                <label htmlFor="backups-page-size">Rows per page:</label>
                <select
                  id="backups-page-size"
                  value={pageSize}
                  onChange={(e) => setPageSize(Number(e.target.value))}
                  className="border border-gray-300 rounded-md px-2 py-1 text-xs bg-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                >
                  {PAGE_SIZE_OPTIONS.map((n) => (
                    <option key={n} value={n}>{n}</option>
                  ))}
                </select>
              </div>
              <div>
                Showing {rangeStart}–{rangeEnd} of {total}
              </div>
              <div className="flex items-center gap-1">
                <button
                  type="button"
                  onClick={() => setPage((p) => Math.max(0, p - 1))}
                  disabled={!canPrev}
                  className="p-1 text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-40 disabled:cursor-not-allowed"
                  title="Previous page"
                >
                  <ChevronLeft size={14} />
                </button>
                <button
                  type="button"
                  onClick={() => setPage((p) => p + 1)}
                  disabled={!canNext}
                  className="p-1 text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-40 disabled:cursor-not-allowed"
                  title="Next page"
                >
                  <ChevronRight size={14} />
                </button>
              </div>
            </div>
          )}
        </div>
      </div>

      {showCreateBackup && (
        <CreateBackupModal
          instances={instances}
          teams={teams}
          defaultInstance={instanceFilter}
          onClose={() => setShowCreateBackup(false)}
        />
      )}

      {showCreateSchedule && (
        <ScheduleModal
          instances={instances}
          teams={teams}
          onClose={() => setShowCreateSchedule(false)}
        />
      )}

      {editSchedule && (
        <ScheduleModal
          instances={instances}
          teams={teams}
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
  teamIDs,
  instances,
  teams,
}: {
  instanceIDs: string;
  teamIDs?: string;
  instances: { id: number; display_name: string }[];
  teams: { id: number; name: string }[];
}) {
  if (instanceIDs === "ALL") {
    return <span className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full bg-purple-50 text-purple-700">All Instances</span>;
  }
  let instIds: number[] = [];
  try {
    instIds = JSON.parse(instanceIDs);
  } catch {
    // fall through
  }
  let parsedTeamIds: number[] = [];
  if (teamIDs) {
    try {
      const v = JSON.parse(teamIDs);
      if (Array.isArray(v)) parsedTeamIds = v as number[];
    } catch {
      // ignore
    }
  }
  if (instIds.length === 0 && parsedTeamIds.length === 0) {
    return <span className="text-gray-500">{instanceIDs}</span>;
  }
  return (
    <div className="flex flex-wrap gap-1">
      {parsedTeamIds.map((id) => {
        const team = teams.find((t) => t.id === id);
        return (
          <span key={`t-${id}`} className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full bg-blue-50 text-blue-700">
            Team: {team?.name || `#${id}`}
          </span>
        );
      })}
      {instIds.map((id) => {
        const inst = instances.find((i) => i.id === id);
        return (
          <span key={id} className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full bg-gray-100 text-gray-600">
            {inst?.display_name || `#${id}`}
          </span>
        );
      })}
    </div>
  );
}

function SchedulePaths({ paths }: { paths: string }) {
  try {
    const arr: string[] = JSON.parse(paths);
    return <span>{arr.join(", ")}</span>;
  } catch {
    return <span>{paths}</span>;
  }
}

function CancelRunningBackupButton({ id }: { id: number }) {
  const cancelMutation = useCancelBackup();
  return (
    <button
      onClick={() => cancelMutation.mutate(id)}
      disabled={cancelMutation.isPending}
      className="p-1 text-gray-400 hover:text-red-600 transition-colors"
      title="Cancel backup"
    >
      <Square size={14} />
    </button>
  );
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
  teams,
  defaultInstance,
  onClose,
}: {
  instances: Instance[];
  teams: { id: number; name: string }[];
  defaultInstance?: string;
  onClose: () => void;
}) {
  const defaultInst = instances.find((i) => i.name === defaultInstance);
  const [selectedInstanceId, setSelectedInstanceId] = useState<number | null>(
    defaultInst ? defaultInst.id : null,
  );
  const [paths, setPaths] = useState<string[]>(["HOME"]);
  const [note, setNote] = useState("");
  const createMutation = useCreateBackup();

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (selectedInstanceId == null) return;
    const cleanPaths = paths.filter((p) => p.trim() !== "");
    createMutation.mutate(
      { instanceId: selectedInstanceId, paths: cleanPaths.length > 0 ? cleanPaths : undefined, note: note || undefined },
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
            <InstanceTeamPicker
              mode="single"
              instances={instances}
              teams={teams}
              allowAll={false}
              allowTeamSelect={false}
              placeholder="Select instance..."
              selected={
                selectedInstanceId != null
                  ? { kind: "instance", instanceId: selectedInstanceId }
                  : { kind: "all" }
              }
              onChange={(sel) => {
                if (sel.kind === "instance") setSelectedInstanceId(sel.instanceId);
              }}
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
            disabled={selectedInstanceId == null || createMutation.isPending}
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
  teams,
  schedule,
  onClose,
}: {
  instances: Instance[];
  teams: { id: number; name: string }[];
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

  const parseTeamIDs = (): number[] => {
    if (!schedule || !schedule.team_ids) return [];
    try {
      const parsed = JSON.parse(schedule.team_ids);
      return Array.isArray(parsed) ? (parsed as number[]) : [];
    } catch {
      return [];
    }
  };

  const initial = parseInstanceIDs();
  const [allInstances, setAllInstances] = useState(initial.all);
  const [selectedInstanceIds, setSelectedInstanceIds] = useState<number[]>(
    initial.ids.filter((id) => instances.some((i) => i.id === id)),
  );
  const [selectedTeamIds, setSelectedTeamIds] = useState<number[]>(parseTeamIDs());
  const [cronExpression, setCronExpression] = useState(schedule?.cron_expression || "0 2 * * *");
  const [paths, setPaths] = useState<string[]>(parsePaths());
  const [retentionDays, setRetentionDays] = useState<string>(String(schedule?.retention_days ?? 0));

  const createMutation = useCreateSchedule();
  const updateMutation = useUpdateSchedule();

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    const instanceIdsValue = allInstances
      ? "ALL"
      : JSON.stringify(selectedInstanceIds);
    const cleanPaths = paths.filter((p) => p.trim() !== "");
    const retentionDaysValue = Math.max(0, parseInt(retentionDays, 10) || 0);

    const teamIdsValue = allInstances ? [] : selectedTeamIds;

    if (isEdit && schedule) {
      updateMutation.mutate(
        {
          id: schedule.id,
          instance_ids: instanceIdsValue,
          team_ids: teamIdsValue,
          cron_expression: cronExpression,
          paths: cleanPaths.length > 0 ? cleanPaths : ["HOME"],
          retention_days: retentionDaysValue,
        },
        { onSuccess: () => onClose() },
      );
    } else {
      createMutation.mutate(
        {
          instance_ids: instanceIdsValue,
          team_ids: teamIdsValue,
          cron_expression: cronExpression,
          paths: cleanPaths.length > 0 ? cleanPaths : ["HOME"],
          retention_days: retentionDaysValue,
        },
        { onSuccess: () => onClose() },
      );
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onClose();
  };

  const isPending = createMutation.isPending || updateMutation.isPending;
  const canSubmit =
    (allInstances ||
      selectedInstanceIds.length > 0 ||
      selectedTeamIds.length > 0) &&
    cronExpression.trim() !== "";

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
                <InstanceTeamPicker
                  mode="multi"
                  instances={instances}
                  teams={teams}
                  selectedInstanceIds={selectedInstanceIds}
                  onChange={setSelectedInstanceIds}
                  selectedTeamIds={selectedTeamIds}
                  onTeamsChange={setSelectedTeamIds}
                  placeholder="Select instances or teams..."
                />
              )}
            </div>
          </div>

          <div>
            <label className="block text-xs text-gray-500 mb-1">Schedule *</label>
            <input
              type="text"
              value={cronExpression}
              onChange={(e) => setCronExpression(e.target.value)}
              placeholder="0 2 * * *"
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 font-mono"
            />
            <div className="flex flex-wrap gap-x-2 gap-y-0.5 mt-1">
              {CRON_PRESETS.map((p) => (
                <button
                  key={p.value}
                  type="button"
                  onClick={() => setCronExpression(p.value)}
                  className="text-xs text-blue-500 hover:text-blue-700 cursor-pointer"
                >
                  {p.label}
                </button>
              ))}
            </div>
          </div>

          <div>
            <label className="block text-xs text-gray-500 mb-1">Folders to backup</label>
            <FolderInput value={paths} onChange={setPaths} />
          </div>

          <div>
            <label className="block text-xs text-gray-500 mb-1">Retention (days)</label>
            <input
              type="number"
              min={0}
              value={retentionDays}
              onChange={(e) => setRetentionDays(e.target.value.replace(/[^0-9]/g, ""))}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
            <p className="mt-1 text-xs text-gray-500">
              Scheduled backups older than this many days will be deleted automatically. Set to 0 to keep forever.
            </p>
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
