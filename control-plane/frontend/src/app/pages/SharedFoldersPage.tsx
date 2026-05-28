import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  FolderOpen,
  Trash2,
  AlertTriangle,
  ChevronRight,
  ChevronDown,
} from "lucide-react";
import AgentTeamPicker from "@common/components/AgentTeamPicker";
import { useTeam } from "@common/contexts/TeamContext";
import {
  fetchSharedFolders,
  fetchHostMountConfig,
  createSharedFolder,
  updateSharedFolder,
  deleteSharedFolder,
  type SharedFolder,
} from "@common/api/sharedFolders";
import { fetchInstances } from "@common/api/instances";
import { successToast, errorToast } from "@common/utils/toast";
import Page from "@common/components/Page";
import EmptyState from "@common/components/EmptyState";

export default function SharedFoldersPage() {
  const queryClient = useQueryClient();
  const [editFolder, setEditFolder] = useState<SharedFolder | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<SharedFolder | null>(null);

  const { data: folders = [], isLoading } = useQuery({
    queryKey: ["shared-folders"],
    queryFn: fetchSharedFolders,
  });

  const { data: instances = [] } = useQuery({
    queryKey: ["instances"],
    queryFn: fetchInstances,
  });

  const instanceMap = new Map(instances.map((i) => [i.id, i.display_name]));

  const deleteMutation = useMutation({
    mutationFn: (id: number) => deleteSharedFolder(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["shared-folders"] });
      successToast("Shared folder deleted");
      setDeleteTarget(null);
    },
    onError: (err) => errorToast("Failed to delete shared folder", err),
  });

  if (isLoading) {
    return (
      <div className="text-center py-12 text-gray-500">Loading...</div>
    );
  }

  return (
    <Page
      title="Shared Folders"
      actions={
        <button
          onClick={() => setShowCreate(true)}
          className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
        >
          New Folder
        </button>
      }
    >
      {folders.length === 0 ? (
        <EmptyState
          title="No shared folders yet."
          hint="Create one to share data between instances."
        />
      ) : (
        <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 border-b border-gray-200">
              <tr>
                <th className="text-left px-4 py-3 font-medium text-gray-600">
                  Name
                </th>
                <th className="text-left px-4 py-3 font-medium text-gray-600">
                  Mount Path
                </th>
                <th className="text-left px-4 py-3 font-medium text-gray-600">
                  Instances
                </th>
                <th className="text-left px-4 py-3 font-medium text-gray-600">
                  Created
                </th>
                <th className="text-right px-4 py-3 font-medium text-gray-600">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody>
              {folders.map((f) => (
                <tr
                  key={f.id}
                  className="border-b border-gray-100 last:border-0"
                >
                  <td className="px-4 py-3 text-gray-900 font-medium">
                    <button
                      type="button"
                      onClick={() => setEditFolder(f)}
                      className="inline-flex items-center gap-1.5 text-left text-blue-600 hover:text-blue-700 hover:underline transition-colors"
                      title="Edit"
                    >
                      <FolderOpen size={14} className="text-gray-400" />
                      {f.name}
                    </button>
                  </td>
                  <td className="px-4 py-3 text-gray-500 font-mono text-xs">
                    {f.mount_path}
                  </td>
                  <td className="px-4 py-3 text-gray-500">
                    {f.instance_ids
                      .map((id) => instanceMap.get(id))
                      .filter(Boolean)
                      .join(", ") || (
                      <span className="text-gray-300">None</span>
                    )}
                  </td>
                  <td className="px-4 py-3 text-gray-500">
                    {new Date(f.created_at).toLocaleDateString()}
                  </td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => setDeleteTarget(f)}
                      className="p-1 text-gray-400 hover:text-red-600 transition-colors"
                      title="Delete"
                    >
                      <Trash2 size={14} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Create / Edit Modal */}
      {(showCreate || editFolder) && (
        <FolderModal
          folder={editFolder}
          onClose={() => {
            setShowCreate(false);
            setEditFolder(null);
          }}
        />
      )}

      {/* Delete Confirmation */}
      {deleteTarget && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div
            className="fixed inset-0 bg-black/50"
            onClick={() => setDeleteTarget(null)}
          />
          <div className="relative bg-white rounded-lg shadow-lg p-6 max-w-sm w-full mx-4">
            <h3 className="text-lg font-semibold text-gray-900 mb-2">
              Delete Shared Folder
            </h3>
            <p className="text-sm text-gray-600 mb-2">
              Are you sure you want to delete &ldquo;{deleteTarget.name}&rdquo;?
            </p>
            {(() => {
              const names = deleteTarget.instance_ids
                .map((id) => instanceMap.get(id))
                .filter(Boolean);
              return names.length > 0 ? (
                <div className="flex items-center gap-2 px-3 py-2 mb-4 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800">
                  <AlertTriangle size={16} className="shrink-0" />
                  <span>
                    The following instances will be automatically restarted:{" "}
                    <strong>{names.join(", ")}</strong>
                  </span>
                </div>
              ) : <div className="mb-4" />;
            })()}
            <div className="flex justify-end gap-3">
              <button
                onClick={() => setDeleteTarget(null)}
                className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
              >
                Cancel
              </button>
              <button
                onClick={() => deleteMutation.mutate(deleteTarget.id)}
                disabled={deleteMutation.isPending}
                className="px-4 py-2 text-sm font-medium text-white bg-red-600 rounded-md hover:bg-red-700"
              >
                {deleteMutation.isPending ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}
    </Page>
  );
}

function FolderModal({
  folder,
  onClose,
}: {
  folder: SharedFolder | null;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const isEdit = folder !== null;

  const [name, setName] = useState(folder?.name ?? "");
  const [mountPath, setMountPath] = useState(folder?.mount_path ?? "");
  const [mountToHost, setMountToHost] = useState(
    !!folder?.host_path,
  );
  const [hostPath, setHostPath] = useState(folder?.host_path ?? "");
  const [readOnly, setReadOnly] = useState(folder?.read_only ?? true);
  const [selectedInstances, setSelectedInstances] = useState<number[]>(
    folder?.instance_ids ?? [],
  );
  const [selectedTeamIds, setSelectedTeamIds] = useState<number[]>(
    folder?.team_ids ?? [],
  );

  const { data: instances = [] } = useQuery({
    queryKey: ["instances"],
    queryFn: fetchInstances,
  });

  const { data: existingFolders = [] } = useQuery({
    queryKey: ["shared-folders"],
    queryFn: fetchSharedFolders,
  });

  const { data: hostMountConfig } = useQuery({
    queryKey: ["host-mount-config"],
    queryFn: fetchHostMountConfig,
  });

  const trimmedMountPath = mountPath.trim();
  const duplicateMountPath =
    trimmedMountPath !== "" &&
    existingFolders.some(
      (f) => f.mount_path === trimmedMountPath && f.id !== folder?.id,
    );

  const trimmedHostPath = hostPath.trim();
  const allowedPrefixes = hostMountConfig?.allowed_prefixes ?? [];
  const hostPathWithinAllowlist =
    trimmedHostPath.startsWith("/") &&
    !trimmedHostPath.includes("..") &&
    allowedPrefixes.some(
      (p) => trimmedHostPath === p || trimmedHostPath.startsWith(p + "/"),
    );
  const hostPathInvalid = mountToHost && !hostPathWithinAllowlist;

  const createMutation = useMutation({
    mutationFn: () =>
      createSharedFolder({
        name,
        mount_path: mountPath,
        ...(mountToHost
          ? { host_path: trimmedHostPath, read_only: readOnly }
          : {}),
      }),
    onSuccess: (created) => {
      // If anything is selected, attach it in a second PUT.
      if (selectedInstances.length > 0 || selectedTeamIds.length > 0) {
        updateSharedFolder(created.id, {
          instance_ids: selectedInstances,
          team_ids: selectedTeamIds,
        }).then(() =>
          queryClient.invalidateQueries({ queryKey: ["shared-folders"] }),
        );
      } else {
        queryClient.invalidateQueries({ queryKey: ["shared-folders"] });
      }
      successToast("Shared folder created");
      onClose();
    },
    onError: (err) => errorToast("Failed to create shared folder", err),
  });

  const updateMutation = useMutation({
    mutationFn: () =>
      updateSharedFolder(folder!.id, {
        name,
        instance_ids: selectedInstances,
        team_ids: selectedTeamIds,
        ...(folder?.host_path ? { read_only: readOnly } : {}),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["shared-folders"] });
      successToast("Shared folder updated");
      onClose();
    },
    onError: (err) => errorToast("Failed to update shared folder", err),
  });

  const isPending = createMutation.isPending || updateMutation.isPending;
  const canSave =
    name.trim() !== "" &&
    mountPath.startsWith("/") &&
    !duplicateMountPath &&
    !hostPathInvalid;

  const origIds = folder?.instance_ids ?? [];
  const hasInstanceChanges =
    selectedInstances.length !== origIds.length ||
    selectedInstances.some((id) => !origIds.includes(id));

  const handleSubmit = () => {
    if (isEdit) {
      updateMutation.mutate();
    } else {
      createMutation.mutate();
    }
  };

  const { teams } = useTeam();

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      onClose();
    } else if (e.key === "Enter" && canSave && !isPending && !e.shiftKey) {
      handleSubmit();
    }
  };

  return (
    <div
      className="fixed inset-0 bg-black/40 z-50 flex items-center justify-center"
      onKeyDown={handleKeyDown}
    >
      <div className="bg-white rounded-lg shadow-xl p-6 w-full max-w-md mx-4">
        <h2 className="text-base font-semibold text-gray-900 mb-4">
          {isEdit ? "Edit Shared Folder" : "New Shared Folder"}
        </h2>

        <div className="space-y-4">
          <div>
            <label className="block text-xs text-gray-500 mb-1">Name</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              placeholder="My shared data"
              autoFocus
            />
          </div>

          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Mount Path
            </label>
            <input
              type="text"
              value={mountPath}
              onChange={(e) => setMountPath(e.target.value)}
              readOnly={!!folder}
              className={`w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500 ${
                folder ? "bg-gray-50 text-gray-500 cursor-not-allowed" : ""
              }`}
              placeholder="/shared/data"
            />
            {folder ? (
              <p className="text-xs text-gray-400 mt-1">
                Mount path cannot be changed after creation.
              </p>
            ) : duplicateMountPath ? (
              <p className="text-xs text-red-600 mt-1">
                Another shared folder already uses this mount path.
              </p>
            ) : (
              <p className="text-xs text-gray-400 mt-1">
                Same path on all mapped instances. Must be unique across shared
                folders.
              </p>
            )}
          </div>

          {hostMountConfig?.enabled && (
            <div>
              <button
                type="button"
                onClick={() => !folder && setMountToHost((v) => !v)}
                disabled={!!folder}
                aria-expanded={mountToHost}
                className="flex items-center gap-1.5 text-sm text-gray-700 hover:text-gray-900 disabled:cursor-not-allowed"
              >
                {mountToHost ? (
                  <ChevronDown size={16} className="text-gray-400" />
                ) : (
                  <ChevronRight size={16} className="text-gray-400" />
                )}
                <span>Mount to Host</span>
              </button>
              <p className="text-xs text-gray-400 mt-1">
                Back this folder with a directory on the host instead of a
                managed volume.
              </p>

              {mountToHost && (
                <div className="mt-3 space-y-3">
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">
                      Host Path
                    </label>
                    <input
                      type="text"
                      value={hostPath}
                      onChange={(e) => setHostPath(e.target.value)}
                      readOnly={!!folder}
                      className={`w-full px-3 py-1.5 border rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500 ${
                        folder
                          ? "bg-gray-50 text-gray-500 cursor-not-allowed border-gray-300"
                          : hostPathInvalid
                            ? "border-red-300"
                            : "border-gray-300"
                      }`}
                      placeholder="/Users/example/shared/obsidian"
                    />
                    {folder ? (
                      <p className="text-xs text-gray-400 mt-1">
                        Host path cannot be changed after creation.
                      </p>
                    ) : hostPathInvalid ? (
                      <p className="text-xs text-red-600 mt-1">
                        Host path must be within an allowed location:{" "}
                        {allowedPrefixes.join(", ")}
                      </p>
                    ) : (
                      <p className="text-xs text-gray-400 mt-1">
                        Allowed locations: {allowedPrefixes.join(", ")}
                      </p>
                    )}
                  </div>

                  <div>
                    <label className="block text-xs text-gray-500 mb-1">
                      Access Mode
                    </label>
                    <select
                      value={readOnly ? "ro" : "rw"}
                      onChange={(e) => setReadOnly(e.target.value === "ro")}
                      className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                    >
                      <option value="ro">Read-only</option>
                      <option value="rw">Read-write</option>
                    </select>
                  </div>
                </div>
              )}
            </div>
          )}

          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Instances
            </label>
            <AgentTeamPicker
              mode="multi"
              instances={instances}
              teams={teams}
              selectedInstanceIds={selectedInstances}
              onChange={setSelectedInstances}
              selectedTeamIds={selectedTeamIds}
              onTeamsChange={setSelectedTeamIds}
              placeholder="Select instances or teams..."
            />
          </div>

          {(selectedInstances.length > 0 || selectedTeamIds.length > 0) && (
            <div className="flex items-center gap-2 px-3 py-2 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800">
              <AlertTriangle size={16} className="shrink-0" />
              {isEdit && hasInstanceChanges
                ? "Affected instances will be automatically restarted."
                : "Selected instances will be automatically restarted for the changes to take effect."}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between mt-6">
          <button
            type="button"
            onClick={onClose}
            className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleSubmit}
            disabled={!canSave || isPending}
            className="px-4 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {isPending
              ? isEdit
                ? "Saving..."
                : "Creating..."
              : isEdit
                ? "Save"
                : "Create"}
          </button>
        </div>
      </div>
    </div>
  );
}
