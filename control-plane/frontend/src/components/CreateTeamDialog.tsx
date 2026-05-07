import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { createTeam } from "@/api/teams";
import { useTeam } from "@/contexts/TeamContext";
import { successToast, errorToast } from "@/utils/toast";

interface CreateTeamDialogProps {
  onClose: () => void;
}

// CreateTeamDialog is the small modal opened from TeamSelector's
// "+ Create a team" entry. Admin-only — render-time guarding is the
// caller's responsibility.
export default function CreateTeamDialog({ onClose }: CreateTeamDialogProps) {
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const qc = useQueryClient();
  const { setActiveTeamId } = useTeam();

  const mutation = useMutation({
    mutationFn: () => createTeam({ name: name.trim(), description }),
    onSuccess: async (team) => {
      successToast("Team created", team.name);
      // Wait for /api/me to refetch so the new team appears in the
      // user's `teams` list — otherwise TeamContext's "fall back to
      // teams[0]" guard would immediately overwrite our selection.
      await qc.invalidateQueries({ queryKey: ["auth", "me"] });
      qc.invalidateQueries({ queryKey: ["teams"] });
      setActiveTeamId(team.id);
      onClose();
    },
    onError: (err) => errorToast("Failed to create team", err),
  });

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
      if (e.key === "Enter" && name.trim() && !mutation.isPending) {
        mutation.mutate();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [name, mutation, onClose]);

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30">
      <div className="bg-white rounded-md shadow-lg w-full max-w-md p-5">
        <h2 className="text-lg font-semibold text-gray-900 mb-3">Create team</h2>
        <label className="block text-sm font-medium text-gray-700 mb-1">Name</label>
        <input
          autoFocus
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          className="w-full border border-gray-300 rounded-md px-2.5 py-1.5 text-sm mb-3"
          placeholder="e.g. Marketing"
        />
        <label className="block text-sm font-medium text-gray-700 mb-1">Description</label>
        <textarea
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={2}
          className="w-full border border-gray-300 rounded-md px-2.5 py-1.5 text-sm mb-4"
        />
        <div className="flex justify-end gap-2">
          <button
            type="button"
            onClick={onClose}
            className="px-3 py-1.5 text-sm text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={!name.trim() || mutation.isPending}
            onClick={() => mutation.mutate()}
            className="px-3 py-1.5 text-sm text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            {mutation.isPending ? "Creating…" : "Create"}
          </button>
        </div>
      </div>
    </div>
  );
}
