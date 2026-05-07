import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { EyeIcon, EyeOffIcon } from "lucide-react";
import {
  createUser,
  getUserInstances,
  getUserTeams,
  setUserInstances,
  updateUserRole,
  type UserListItem,
} from "@/api/users";
import { fetchInstances } from "@/api/instances";
import { fetchTeams, removeTeamMember, setTeamMember } from "@/api/teams";
import MultiSelect from "@/components/MultiSelect";
import { errorToast, successToast } from "@/utils/toast";

type Mode = { kind: "create" } | { kind: "edit"; user: UserListItem };

interface UserModalProps {
  mode: Mode;
  onClose: () => void;
}

export default function UserModal({ mode, onClose }: UserModalProps) {
  const queryClient = useQueryClient();
  const isEdit = mode.kind === "edit";
  const editingUser = mode.kind === "edit" ? mode.user : null;

  const [username, setUsername] = useState(editingUser?.username ?? "");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);

  // Unified permissions UI: a Full access (admin) toggle, plus two
  // multi-selects when off: which teams the user manages and which
  // instances they're assigned. User-role team memberships are derived
  // automatically from instance picks (manager precedence).
  const [fullAccess, setFullAccess] = useState<boolean>(
    editingUser ? editingUser.role === "admin" : true,
  );
  const [managedTeamIds, setManagedTeamIds] = useState<number[]>([]);
  const [assignedInstanceIds, setAssignedInstanceIds] = useState<number[]>([]);

  // Hydrated baseline used to compute the membership diff on edit.
  const [hydratedMemberships, setHydratedMemberships] = useState<
    { team_id: number; role: "user" | "manager" }[]
  >([]);
  const [hydratedInstanceIds, setHydratedInstanceIds] = useState<number[]>([]);
  const [hydrating, setHydrating] = useState<boolean>(isEdit);

  useEffect(() => {
    if (!editingUser) return;
    setHydrating(true);
    Promise.all([
      getUserTeams(editingUser.id),
      getUserInstances(editingUser.id),
    ])
      .then(([teams, ins]) => {
        const memberships = teams.map((t) => ({
          team_id: t.team_id,
          role: t.role,
        }));
        setHydratedMemberships(memberships);
        const instanceIds = ins.instance_ids || [];
        setHydratedInstanceIds(instanceIds);

        if (editingUser.role !== "admin") {
          setManagedTeamIds(
            memberships.filter((m) => m.role === "manager").map((m) => m.team_id),
          );
          setAssignedInstanceIds(instanceIds);
        }
      })
      .catch(() => errorToast("Failed to load user permissions"))
      .finally(() => setHydrating(false));
  }, [editingUser]);

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [onClose]);

  const { data: instances = [] } = useQuery({
    queryKey: ["instances"],
    queryFn: () => fetchInstances(),
  });
  const { data: teams = [] } = useQuery({
    queryKey: ["teams"],
    queryFn: fetchTeams,
  });

  const teamNameById = useMemo(() => {
    const m = new Map<number, string>();
    for (const t of teams) m.set(t.id, t.name);
    return m;
  }, [teams]);

  const teamOptions = useMemo(
    () => teams.map((t) => ({ value: t.id, label: t.name })),
    [teams],
  );
  const instanceOptions = useMemo(
    () =>
      instances.map((inst) => {
        const teamName = teamNameById.get(inst.team_id);
        const baseLabel = inst.display_name || inst.name;
        return {
          value: inst.id,
          label: teamName ? `[${teamName}] ${baseLabel}` : baseLabel,
        };
      }),
    [instances, teamNameById],
  );

  const selectedTeamOptions = teamOptions.filter((o) =>
    managedTeamIds.includes(o.value),
  );
  const selectedInstanceOptions = instanceOptions.filter((o) =>
    assignedInstanceIds.includes(o.value),
  );

  // Map instance id → team id for auto-deriving user-role memberships.
  const instanceTeam = useMemo(() => {
    const m = new Map<number, number>();
    for (const inst of instances) m.set(inst.id, inst.team_id);
    return m;
  }, [instances]);

  // Compute the desired final state from the form. Returns the desired
  // membership map (team_id → role) and the union of selected instances.
  const computeDesired = () => {
    const memberships = new Map<number, "manager" | "user">();
    for (const teamId of managedTeamIds) memberships.set(teamId, "manager");
    for (const iid of assignedInstanceIds) {
      const tid = instanceTeam.get(iid);
      if (tid != null && memberships.get(tid) !== "manager") {
        memberships.set(tid, "user");
      }
    }
    return { memberships, instanceIds: assignedInstanceIds };
  };

  // Apply membership/instance side-effects for both create and edit.
  // Returns the team IDs touched so the caller can invalidate per-team
  // member queries.
  const applyPermissions = async (
    userId: number,
    current: { team_id: number; role: "user" | "manager" }[],
    currentInstanceIds: number[],
    nextFullAccess: boolean,
  ): Promise<number[]> => {
    const touchedTeams = new Set<number>();
    if (nextFullAccess) {
      for (const m of current) {
        await removeTeamMember(m.team_id, userId);
        touchedTeams.add(m.team_id);
      }
      if (currentInstanceIds.length > 0) {
        await setUserInstances(userId, []);
      }
      return [...touchedTeams];
    }

    const desired = computeDesired();
    const currentMap = new Map<number, "manager" | "user">();
    for (const m of current) currentMap.set(m.team_id, m.role);

    for (const [teamId] of currentMap) {
      if (!desired.memberships.has(teamId)) {
        await removeTeamMember(teamId, userId);
        touchedTeams.add(teamId);
      }
    }
    for (const [teamId, role] of desired.memberships) {
      if (currentMap.get(teamId) !== role) {
        await setTeamMember(teamId, { user_id: userId, role });
        touchedTeams.add(teamId);
      }
    }
    const sortedNext = [...desired.instanceIds].sort((a, b) => a - b);
    const sortedCur = [...currentInstanceIds].sort((a, b) => a - b);
    const sameInstances =
      sortedNext.length === sortedCur.length &&
      sortedNext.every((v, i) => v === sortedCur[i]);
    if (!sameInstances) {
      await setUserInstances(userId, desired.instanceIds);
    }
    return [...touchedTeams];
  };

  const createMutation = useMutation({
    mutationFn: async () => {
      const newUser = await createUser({
        username,
        password,
        role: fullAccess ? "admin" : "user",
      });
      const touched = await applyPermissions(newUser.id, [], [], fullAccess);
      return { touchedTeamIds: touched };
    },
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      for (const teamId of result.touchedTeamIds) {
        queryClient.invalidateQueries({
          queryKey: ["teams", teamId, "members"],
        });
      }
      queryClient.invalidateQueries({ queryKey: ["instances"] });
      successToast("User created");
      onClose();
    },
    onError: (error) => errorToast("Failed to create user", error),
  });

  const editMutation = useMutation({
    mutationFn: async () => {
      if (!editingUser) return { touchedTeamIds: [] as number[] };
      const desiredRole = fullAccess ? "admin" : "user";
      if (desiredRole !== editingUser.role) {
        await updateUserRole(editingUser.id, desiredRole);
      }
      const touched = await applyPermissions(
        editingUser.id,
        hydratedMemberships,
        hydratedInstanceIds,
        fullAccess,
      );
      return { touchedTeamIds: touched };
    },
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      for (const teamId of result.touchedTeamIds) {
        queryClient.invalidateQueries({
          queryKey: ["teams", teamId, "members"],
        });
      }
      queryClient.invalidateQueries({ queryKey: ["instances"] });
      successToast("User updated");
      onClose();
    },
    onError: (error) => errorToast("Failed to update user", error),
  });

  const isPending = createMutation.isPending || editMutation.isPending;

  const canSubmit = isEdit
    ? !isPending && !hydrating
    : username.trim().length > 0 && password.length > 0 && !isPending;

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    if (isEdit) editMutation.mutate();
    else createMutation.mutate();
  };

  const title = isEdit ? `Edit user: ${editingUser?.username}` : "Create user";
  const submitLabel = isEdit
    ? editMutation.isPending
      ? "Saving..."
      : "Save"
    : createMutation.isPending
      ? "Creating..."
      : "Create";

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="bg-white rounded-xl shadow-xl w-full max-w-md mx-4 flex flex-col max-h-[80vh]">
        <form onSubmit={handleSubmit} className="flex flex-col min-h-0 flex-1">
          {/* Header */}
          <div className="px-6 py-4 border-b border-gray-200">
            <h2 className="text-base font-semibold text-gray-900">{title}</h2>
          </div>

          {/* Scrollable body */}
          <div className="overflow-y-auto flex-1 px-6 py-4 space-y-4">
            {!isEdit && (
              <div>
                <label className="block text-xs text-gray-500 mb-1">
                  Username *
                </label>
                <input
                  type="text"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="username"
                  autoFocus
                />
              </div>
            )}

            {!isEdit && (
              <div>
                <label className="block text-xs text-gray-500 mb-1">
                  Password *
                </label>
                <div className="relative">
                  <input
                    type={showPassword ? "text" : "password"}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    className="w-full px-3 py-1.5 pr-10 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                  <button
                    type="button"
                    onClick={() => setShowPassword(!showPassword)}
                    className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
                    aria-label={
                      showPassword ? "Hide password" : "Show password"
                    }
                  >
                    {showPassword ? (
                      <EyeOffIcon size={14} />
                    ) : (
                      <EyeIcon size={14} />
                    )}
                  </button>
                </div>
              </div>
            )}

            <label className="flex items-center gap-2 text-sm text-gray-700">
              <input
                type="checkbox"
                checked={fullAccess}
                onChange={(e) => setFullAccess(e.target.checked)}
                className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                disabled={hydrating}
              />
              Full access (admin)
            </label>

            {!fullAccess && (
              <>
                <div>
                  <label className="block text-xs text-gray-500 mb-1">
                    Manage Teams
                  </label>
                  <MultiSelect
                    options={teamOptions}
                    value={selectedTeamOptions}
                    onChange={(sel) =>
                      setManagedTeamIds(sel.map((s) => s.value))
                    }
                    placeholder={
                      hydrating ? "Loading..." : "Select teams to manage..."
                    }
                    isDisabled={hydrating}
                    isLoading={hydrating}
                    noOptionsMessage={() => "No teams available"}
                  />
                </div>

                <div>
                  <label className="block text-xs text-gray-500 mb-1">
                    Use Instances
                  </label>
                  <MultiSelect
                    options={instanceOptions}
                    value={selectedInstanceOptions}
                    onChange={(sel) =>
                      setAssignedInstanceIds(sel.map((s) => s.value))
                    }
                    placeholder={
                      hydrating ? "Loading..." : "Select instances..."
                    }
                    isDisabled={hydrating}
                    isLoading={hydrating}
                    noOptionsMessage={() => "No instances available"}
                  />
                </div>
              </>
            )}
          </div>

          {/* Footer */}
          <div className="px-6 py-4 border-t border-gray-200 flex items-center justify-end gap-3">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 text-sm font-medium text-gray-700 hover:text-gray-900 transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!canSubmit}
              className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              {submitLabel}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
