import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Pencil, Trash2 } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import CreateTeamDialog from "@/components/CreateTeamDialog";
import TeamMembersPanel from "@/components/TeamMembersPanel";
import {
  fetchTeams,
  updateTeam,
  deleteTeam,
  type Team,
} from "@/api/teams";
import { useTeam } from "@/contexts/TeamContext";
import { successToast, errorToast } from "@/utils/toast";

export default function TeamsPage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { setActiveTeamId } = useTeam();
  const { data: teams = [], isLoading } = useQuery({
    queryKey: ["teams"],
    queryFn: fetchTeams,
  });

  const [showCreate, setShowCreate] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editingName, setEditingName] = useState("");
  const [membersTeam, setMembersTeam] = useState<Team | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Team | null>(null);

  const renameMut = useMutation({
    mutationFn: ({ id, name }: { id: number; name: string }) =>
      updateTeam(id, { name }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["teams"] });
      setEditingId(null);
      successToast("Team renamed");
    },
    onError: (err) => errorToast("Failed to rename team", err),
  });

  const deleteMut = useMutation({
    mutationFn: (id: number) => deleteTeam(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["teams"] });
      setDeleteTarget(null);
      successToast("Team deleted");
    },
    onError: (err) => {
      errorToast("Failed to delete team", err);
      setDeleteTarget(null);
    },
  });

  const startEdit = (t: Team) => {
    setEditingId(t.id);
    setEditingName(t.name);
  };

  const commitEdit = () => {
    if (!editingId) return;
    const name = editingName.trim();
    const target = teams.find((t) => t.id === editingId);
    if (!name || !target || name === target.name) {
      setEditingId(null);
      return;
    }
    renameMut.mutate({ id: editingId, name });
  };

  const goToInstances = (teamId: number) => {
    setActiveTeamId(teamId);
    navigate("/");
  };

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-gray-900">Teams</h1>
        <button
          type="button"
          onClick={() => setShowCreate(true)}
          className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
        >
          Create Team
        </button>
      </div>

      <p className="text-sm text-gray-500 mb-4">
        Teams group instances and members. Each user can be a manager or a regular user of any team.
      </p>

      {isLoading ? (
        <div className="text-gray-500">Loading teams...</div>
      ) : (
        <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50 border-b border-gray-200">
              <tr>
                <th className="text-left px-4 py-3 font-medium text-gray-600">Name</th>
                <th className="text-left px-4 py-3 font-medium text-gray-600">Members</th>
                <th className="text-left px-4 py-3 font-medium text-gray-600">Instances</th>
                <th className="text-right px-4 py-3 font-medium text-gray-600">Actions</th>
              </tr>
            </thead>
            <tbody>
              {teams.map((team) => (
                <tr key={team.id} className="border-b border-gray-100 last:border-0">
                  <td className="px-4 py-3">
                    {editingId === team.id ? (
                      <input
                        autoFocus
                        type="text"
                        value={editingName}
                        onChange={(e) => setEditingName(e.target.value)}
                        onBlur={commitEdit}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") commitEdit();
                          if (e.key === "Escape") setEditingId(null);
                        }}
                        className="px-2 py-1 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                      />
                    ) : (
                      <button
                        onClick={() => startEdit(team)}
                        className="font-medium text-blue-600 hover:text-blue-800"
                        title="Click to rename"
                      >
                        {team.name}
                      </button>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    <button
                      onClick={() => setMembersTeam(team)}
                      className="text-blue-600 hover:text-blue-800"
                      title="Manage members"
                    >
                      {team.member_count ?? 0}
                    </button>
                  </td>
                  <td className="px-4 py-3">
                    <button
                      onClick={() => goToInstances(team.id)}
                      className="text-blue-600 hover:text-blue-800"
                      title="View instances"
                    >
                      {team.instance_count ?? 0}
                    </button>
                  </td>
                  <td className="px-4 py-3 text-right">
                    <div className="flex items-center justify-end gap-1">
                      <button
                        onClick={() => startEdit(team)}
                        className="p-1.5 text-gray-400 hover:text-gray-600 rounded"
                        title="Rename team"
                      >
                        <Pencil size={16} />
                      </button>
                      <button
                        onClick={() => setDeleteTarget(team)}
                        className="p-1.5 text-gray-400 hover:text-red-600 rounded"
                        title="Delete team"
                      >
                        <Trash2 size={16} />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
              {teams.length === 0 && (
                <tr>
                  <td colSpan={4} className="text-center text-gray-400 py-6">
                    No teams yet.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      )}

      {showCreate && <CreateTeamDialog onClose={() => setShowCreate(false)} />}

      {membersTeam && (
        <TeamMembersModal team={membersTeam} onClose={() => setMembersTeam(null)} />
      )}

      {deleteTarget && (
        <DeleteTeamDialog
          team={deleteTarget}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => deleteMut.mutate(deleteTarget.id)}
          isPending={deleteMut.isPending}
        />
      )}
    </div>
  );
}

function TeamMembersModal({ team, onClose }: { team: Team; onClose: () => void }) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onClick={onClose}
      onKeyDown={(e) => {
        if (e.key === "Escape") onClose();
      }}
    >
      <div
        className="bg-white rounded-xl shadow-xl w-full max-w-md mx-4 flex flex-col max-h-[80vh]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="px-6 py-4 border-b border-gray-200">
          <h2 className="text-base font-semibold text-gray-900">{team.name}</h2>
        </div>
        <div className="overflow-y-auto flex-1 px-6 py-4">
          <TeamMembersPanel team={team} />
        </div>
        <div className="px-6 py-4 border-t border-gray-200 flex items-center justify-end">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 text-sm text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}

function DeleteTeamDialog({
  team,
  onCancel,
  onConfirm,
  isPending,
}: {
  team: Team;
  onCancel: () => void;
  onConfirm: () => void;
  isPending: boolean;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={onCancel}>
      <div
        className="bg-white rounded-xl shadow-xl w-full max-w-md mx-4 p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="text-base font-semibold text-gray-900 mb-2">Delete team</h2>
        <p className="text-sm text-gray-600 mb-4">
          Delete team "{team.name}"? Instances belonging to this team will need to be reassigned.
        </p>
        <div className="flex justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="px-3 py-1.5 text-sm text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={isPending}
            className="px-3 py-1.5 text-sm text-white bg-red-600 rounded-md hover:bg-red-700 disabled:opacity-50"
          >
            {isPending ? "Deleting..." : "Delete"}
          </button>
        </div>
      </div>
    </div>
  );
}
