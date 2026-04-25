import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { EyeIcon, EyeOffIcon } from "lucide-react";
import {
  createUser,
  getUserInstances,
  setUserInstances,
  updateUserPermissions,
  updateUserRole,
  type UserListItem,
} from "@/api/users";
import { fetchInstances } from "@/api/instances";
import MultiSelect from "@/components/MultiSelect";
import { errorToast, successToast } from "@/utils/toast";

type Mode = { kind: "create" } | { kind: "edit"; user: UserListItem };

interface UserModalProps {
  mode: Mode;
  onClose: () => void;
}

function arraysEqualUnordered(a: number[], b: number[]): boolean {
  if (a.length !== b.length) return false;
  const sa = [...a].sort((x, y) => x - y);
  const sb = [...b].sort((x, y) => x - y);
  return sa.every((v, i) => v === sb[i]);
}

export default function UserModal({ mode, onClose }: UserModalProps) {
  const queryClient = useQueryClient();
  const isEdit = mode.kind === "edit";
  const editingUser = mode.kind === "edit" ? mode.user : null;

  const [username, setUsername] = useState(editingUser?.username ?? "");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [role, setRole] = useState<string>(editingUser?.role ?? "user");
  const [canCreateInstances, setCanCreateInstances] = useState<boolean>(
    editingUser?.can_create_instances ?? false,
  );
  const [selectedInstanceIds, setSelectedInstanceIds] = useState<number[]>([]);
  const [initialInstanceIds, setInitialInstanceIds] = useState<number[]>([]);
  const [instancesLoading, setInstancesLoading] = useState<boolean>(isEdit);

  // Load existing instance assignments for edit mode.
  useEffect(() => {
    if (!editingUser) return;
    setInstancesLoading(true);
    getUserInstances(editingUser.id)
      .then((res) => {
        const ids = res.instance_ids || [];
        setSelectedInstanceIds(ids);
        setInitialInstanceIds(ids);
      })
      .catch(() => errorToast("Failed to load user instances"))
      .finally(() => setInstancesLoading(false));
  }, [editingUser]);

  // Escape closes modal.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", handler);
    return () => document.removeEventListener("keydown", handler);
  }, [onClose]);

  const { data: instances = [] } = useQuery({
    queryKey: ["instances"],
    queryFn: fetchInstances,
  });

  const instanceOptions = useMemo(
    () =>
      instances.map((inst) => ({
        value: inst.id,
        label: inst.display_name || inst.name,
      })),
    [instances],
  );
  const selectedInstanceOptions = instanceOptions.filter((o) =>
    selectedInstanceIds.includes(o.value),
  );

  const effectiveCanCreate = role === "user" ? canCreateInstances : false;

  const hasChanges = useMemo(() => {
    if (!editingUser) return true;
    if (role !== editingUser.role) return true;
    if (effectiveCanCreate !== editingUser.can_create_instances) return true;
    if (
      role === "user" &&
      !arraysEqualUnordered(selectedInstanceIds, initialInstanceIds)
    ) {
      return true;
    }
    return false;
  }, [
    editingUser,
    role,
    effectiveCanCreate,
    selectedInstanceIds,
    initialInstanceIds,
  ]);

  const createMutation = useMutation({
    mutationFn: async () => {
      const created = await createUser({
        username,
        password,
        role,
        can_create_instances: effectiveCanCreate,
      });
      if (role === "user" && selectedInstanceIds.length > 0) {
        await setUserInstances(created.id, selectedInstanceIds);
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      successToast("User created");
      onClose();
    },
    onError: (error) => errorToast("Failed to create user", error),
  });

  const editMutation = useMutation({
    mutationFn: async () => {
      if (!editingUser) return;
      if (role !== editingUser.role) {
        await updateUserRole(editingUser.id, role);
      }
      if (effectiveCanCreate !== editingUser.can_create_instances) {
        await updateUserPermissions(editingUser.id, {
          can_create_instances: effectiveCanCreate,
        });
      }
      if (
        role === "user" &&
        !arraysEqualUnordered(selectedInstanceIds, initialInstanceIds)
      ) {
        await setUserInstances(editingUser.id, selectedInstanceIds);
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      successToast("User updated");
      onClose();
    },
    onError: (error) => errorToast("Failed to update user", error),
  });

  const isPending = createMutation.isPending || editMutation.isPending;

  const canSubmit = isEdit
    ? hasChanges && !isPending
    : username.trim().length > 0 && password.length > 0 && !isPending;

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (!canSubmit) return;
    if (isEdit) {
      editMutation.mutate();
    } else {
      createMutation.mutate();
    }
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
    <div className="fixed inset-0 bg-black/40 z-50 flex items-center justify-center">
      <div className="bg-white rounded-lg shadow-xl p-6 w-full max-w-md mx-4">
        <h2 className="text-base font-semibold text-gray-900 mb-4">{title}</h2>
        <form onSubmit={handleSubmit} className="space-y-4">
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
                  aria-label={showPassword ? "Hide password" : "Show password"}
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

          <div>
            <label className="block text-xs text-gray-500 mb-1">Role</label>
            <select
              value={role}
              onChange={(e) => setRole(e.target.value)}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white"
            >
              <option value="user">User</option>
              <option value="admin">Admin</option>
            </select>
          </div>

          <label
            className={`flex items-center gap-2 text-sm text-gray-700 ${
              role === "admin" ? "opacity-60 cursor-not-allowed" : ""
            }`}
          >
            <input
              type="checkbox"
              checked={role === "admin" ? true : canCreateInstances}
              disabled={role === "admin"}
              onChange={(e) => setCanCreateInstances(e.target.checked)}
              className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500 disabled:cursor-not-allowed"
            />
            Can create instances and restore from backups
          </label>

          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Assigned instances
            </label>
            {role === "admin" ? (
              <div className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm bg-gray-50 text-gray-500 cursor-not-allowed">
                All instances
              </div>
            ) : (
              <MultiSelect
                options={instanceOptions}
                value={selectedInstanceOptions}
                onChange={(sel) =>
                  setSelectedInstanceIds(sel.map((s) => s.value))
                }
                placeholder={
                  instancesLoading ? "Loading..." : "Select instances..."
                }
                isDisabled={instancesLoading}
                isLoading={instancesLoading}
                noOptionsMessage={() => "No instances available"}
              />
            )}
          </div>

          <div className="flex items-center justify-between pt-2">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!canSubmit}
              className="px-4 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {submitLabel}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
