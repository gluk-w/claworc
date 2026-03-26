import { useState, useEffect, type FormEvent } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Trash2, ShieldCheck, Shield, Key, Server } from "lucide-react";
import { successToast, errorToast } from "@/utils/toast";
import {
  fetchUsers,
  createUser,
  deleteUser,
  updateUserRole,
  resetUserPassword,
  getUserInstances,
  setUserInstances,
  type UserListItem,
} from "@/api/users";
import { fetchInstances } from "@/api/instances";

export default function UsersPage() {
  const queryClient = useQueryClient();
  const { data: users = [], isLoading } = useQuery({
    queryKey: ["users"],
    queryFn: fetchUsers,
  });

  const [showCreate, setShowCreate] = useState(false);
  const [resetTarget, setResetTarget] = useState<UserListItem | null>(null);
  const [assignTarget, setAssignTarget] = useState<UserListItem | null>(null);

  if (isLoading) {
    return <div className="text-gray-500">Loading users...</div>;
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-gray-900">Users</h1>
        <button
          onClick={() => setShowCreate(true)}
          className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
        >
          Create User
        </button>
      </div>

      <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 border-b border-gray-200">
            <tr>
              <th className="text-left px-4 py-3 font-medium text-gray-600">
                Username
              </th>
              <th className="text-left px-4 py-3 font-medium text-gray-600">
                Role
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
            {users.map((user) => (
              <UserRow
                key={user.id}
                user={user}
                onResetPassword={() => setResetTarget(user)}
                onAssignInstances={() => setAssignTarget(user)}
                queryClient={queryClient}
              />
            ))}
          </tbody>
        </table>
      </div>

      {showCreate && (
        <CreateUserDialog
          onClose={() => setShowCreate(false)}
          queryClient={queryClient}
        />
      )}

      {resetTarget && (
        <ResetPasswordDialog
          user={resetTarget}
          onClose={() => setResetTarget(null)}
          queryClient={queryClient}
        />
      )}

      {assignTarget && (
        <AssignInstancesDialog
          user={assignTarget}
          onClose={() => setAssignTarget(null)}
        />
      )}
    </div>
  );
}

function UserRow({
  user,
  onResetPassword,
  onAssignInstances,
  queryClient,
}: {
  user: UserListItem;
  onResetPassword: () => void;
  onAssignInstances: () => void;
  queryClient: ReturnType<typeof useQueryClient>;
}) {
  const deleteMut = useMutation({
    mutationFn: () => deleteUser(user.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      successToast("User deleted");
    },
    onError: (error) => errorToast("Failed to delete user", error),
  });

  const toggleRole = useMutation({
    mutationFn: () =>
      updateUserRole(user.id, user.role === "admin" ? "user" : "admin"),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      successToast("Role updated");
    },
    onError: (error) => errorToast("Failed to update role", error),
  });

  return (
    <tr className="border-b border-gray-100 last:border-0">
      <td className="px-4 py-3 font-medium text-gray-900">{user.username}</td>
      <td className="px-4 py-3">
        <span
          className={`inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium rounded-full ${
            user.role === "admin"
              ? "bg-purple-50 text-purple-700"
              : "bg-gray-100 text-gray-600"
          }`}
        >
          {user.role === "admin" ? (
            <ShieldCheck size={12} />
          ) : (
            <Shield size={12} />
          )}
          {user.role}
        </span>
      </td>
      <td className="px-4 py-3 text-gray-500">
        {user.created_at
          ? new Date(user.created_at).toLocaleDateString()
          : "—"}
      </td>
      <td className="px-4 py-3 text-right">
        <div className="flex items-center justify-end gap-1">
          <button
            onClick={() => toggleRole.mutate()}
            className="p-1.5 text-gray-400 hover:text-gray-600 rounded"
            title={
              user.role === "admin" ? "Demote to user" : "Promote to admin"
            }
          >
            {user.role === "admin" ? (
              <Shield size={16} />
            ) : (
              <ShieldCheck size={16} />
            )}
          </button>
          {user.role === "user" && (
            <button
              onClick={onAssignInstances}
              className="p-1.5 text-gray-400 hover:text-gray-600 rounded"
              title="Assign instances"
            >
              <Server size={16} />
            </button>
          )}
          <button
            onClick={onResetPassword}
            className="p-1.5 text-gray-400 hover:text-gray-600 rounded"
            title="Reset password"
          >
            <Key size={16} />
          </button>
          <button
            onClick={() => {
              if (confirm(`Delete user "${user.username}"?`)) {
                deleteMut.mutate();
              }
            }}
            className="p-1.5 text-gray-400 hover:text-red-600 rounded"
            title="Delete user"
          >
            <Trash2 size={16} />
          </button>
        </div>
      </td>
    </tr>
  );
}

function AssignInstancesDialog({
  user,
  onClose,
}: {
  user: UserListItem;
  onClose: () => void;
}) {
  const [selectedIds, setSelectedIds] = useState<number[]>([]);
  const [loading, setLoading] = useState(true);

  const { data: instances = [] } = useQuery({
    queryKey: ["instances"],
    queryFn: fetchInstances,
  });

  useEffect(() => {
    getUserInstances(user.id)
      .then((res) => {
        setSelectedIds(res.instance_ids || []);
      })
      .catch(() => {
        errorToast("Failed to load user instances");
      })
      .finally(() => setLoading(false));
  }, [user.id]);

  const mutation = useMutation({
    mutationFn: () => setUserInstances(user.id, selectedIds),
    onSuccess: () => {
      successToast("Instances assigned");
      onClose();
    },
    onError: (error) => errorToast("Failed to assign instances", error),
  });

  const toggleInstance = (id: number) => {
    setSelectedIds((prev) =>
      prev.includes(id) ? prev.filter((i) => i !== id) : [...prev, id],
    );
  };

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    mutation.mutate();
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg shadow-lg w-full max-w-sm p-6">
        <h2 className="text-lg font-semibold mb-4">
          Assign Instances: {user.username}
        </h2>
        <form onSubmit={handleSubmit} className="space-y-3">
          {loading ? (
            <div className="text-gray-500 text-sm py-4 text-center">
              Loading...
            </div>
          ) : instances.length === 0 ? (
            <div className="text-gray-500 text-sm py-4 text-center">
              No instances available
            </div>
          ) : (
            <div className="max-h-64 overflow-y-auto space-y-1">
              {instances.map((inst) => (
                <label
                  key={inst.id}
                  className="flex items-center gap-2 px-2 py-1.5 rounded hover:bg-gray-50 cursor-pointer"
                >
                  <input
                    type="checkbox"
                    checked={selectedIds.includes(inst.id)}
                    onChange={() => toggleInstance(inst.id)}
                    className="rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                  />
                  <span className="text-sm text-gray-900">
                    {inst.display_name || inst.name}
                  </span>
                </label>
              ))}
            </div>
          )}
          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 text-sm text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={mutation.isPending || loading}
              className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              Save
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function CreateUserDialog({
  onClose,
  queryClient,
}: {
  onClose: () => void;
  queryClient: ReturnType<typeof useQueryClient>;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState("user");

  const mutation = useMutation({
    mutationFn: () => createUser({ username, password, role }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      successToast("User created");
      onClose();
    },
    onError: (error) => errorToast("Failed to create user", error),
  });

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    mutation.mutate();
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg shadow-lg w-full max-w-sm p-6">
        <h2 className="text-lg font-semibold mb-4">Create User</h2>
        <form onSubmit={handleSubmit} className="space-y-3">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Username
            </label>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm"
              required
              autoFocus
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Password
            </label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm"
              required
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Role
            </label>
            <select
              value={role}
              onChange={(e) => setRole(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm"
            >
              <option value="user">User</option>
              <option value="admin">Admin</option>
            </select>
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 text-sm text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={mutation.isPending}
              className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              Create
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function ResetPasswordDialog({
  user,
  onClose,
  queryClient,
}: {
  user: UserListItem;
  onClose: () => void;
  queryClient: ReturnType<typeof useQueryClient>;
}) {
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");

  const mutation = useMutation({
    mutationFn: () => resetUserPassword(user.id, password),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      successToast("Password reset");
      onClose();
    },
    onError: (error) => errorToast("Failed to reset password", error),
  });

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (password !== confirm) {
      errorToast("Passwords do not match");
      return;
    }
    mutation.mutate();
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg shadow-lg w-full max-w-sm p-6">
        <h2 className="text-lg font-semibold mb-4">
          Reset Password: {user.username}
        </h2>
        <form onSubmit={handleSubmit} className="space-y-3">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              New Password
            </label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm"
              required
              autoFocus
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Confirm Password
            </label>
            <input
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm"
              required
            />
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="px-3 py-1.5 text-sm text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={mutation.isPending}
              className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              Reset
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
