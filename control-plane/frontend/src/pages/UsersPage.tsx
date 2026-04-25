import { useState, type FormEvent } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Trash2, ShieldCheck, Shield, Key } from "lucide-react";
import { successToast, errorToast } from "@/utils/toast";
import {
  fetchUsers,
  deleteUser,
  resetUserPassword,
  type UserListItem,
} from "@/api/users";
import UserModal from "@/components/UserModal";
import ConfirmDialog from "@/components/ConfirmDialog";

export default function UsersPage() {
  const queryClient = useQueryClient();
  const { data: users = [], isLoading } = useQuery({
    queryKey: ["users"],
    queryFn: fetchUsers,
  });

  const [showCreate, setShowCreate] = useState(false);
  const [editTarget, setEditTarget] = useState<UserListItem | null>(null);
  const [resetTarget, setResetTarget] = useState<UserListItem | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<UserListItem | null>(null);

  const deleteMut = useMutation({
    mutationFn: (id: number) => deleteUser(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["users"] });
      successToast("User deleted");
      setDeleteTarget(null);
    },
    onError: (error) => {
      errorToast("Failed to delete user", error);
      setDeleteTarget(null);
    },
  });

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
                Last login
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
                onEdit={() => setEditTarget(user)}
                onResetPassword={() => setResetTarget(user)}
                onDelete={() => setDeleteTarget(user)}
              />
            ))}
          </tbody>
        </table>
      </div>

      {showCreate && (
        <UserModal
          mode={{ kind: "create" }}
          onClose={() => setShowCreate(false)}
        />
      )}

      {editTarget && (
        <UserModal
          mode={{ kind: "edit", user: editTarget }}
          onClose={() => setEditTarget(null)}
        />
      )}

      {resetTarget && (
        <ResetPasswordDialog
          user={resetTarget}
          onClose={() => setResetTarget(null)}
          queryClient={queryClient}
        />
      )}

      {deleteTarget && (
        <ConfirmDialog
          title="Delete user"
          message={`Delete user "${deleteTarget.username}"? This action cannot be undone.`}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => deleteMut.mutate(deleteTarget.id)}
        />
      )}
    </div>
  );
}

function UserRow({
  user,
  onEdit,
  onResetPassword,
  onDelete,
}: {
  user: UserListItem;
  onEdit: () => void;
  onResetPassword: () => void;
  onDelete: () => void;
}) {
  return (
    <tr className="border-b border-gray-100 last:border-0">
      <td className="px-4 py-3">
        <button
          onClick={onEdit}
          className="font-medium text-blue-600 hover:text-blue-800"
        >
          {user.username}
        </button>
      </td>
      <td className="px-4 py-3">
        <div className="flex items-center gap-1.5 flex-wrap">
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
          {user.role === "user" && user.can_create_instances && (
            <span
              className="inline-flex items-center px-2 py-0.5 text-xs font-medium rounded-full bg-blue-50 text-blue-700"
              title="Can create instances and restore from backups"
            >
              can create instances
            </span>
          )}
        </div>
      </td>
      <td className="px-4 py-3 text-gray-500">
        {user.last_login_at ? (
          <span title={new Date(user.last_login_at).toLocaleString()}>
            {new Date(user.last_login_at).toLocaleDateString()}
          </span>
        ) : (
          <span className="text-xs text-gray-400">Never</span>
        )}
      </td>
      <td className="px-4 py-3 text-gray-500">
        {user.created_at
          ? new Date(user.created_at).toLocaleDateString()
          : "—"}
      </td>
      <td className="px-4 py-3 text-right">
        <div className="flex items-center justify-end gap-1">
          <button
            onClick={onResetPassword}
            className="p-1.5 text-gray-400 hover:text-gray-600 rounded"
            title="Reset password"
            aria-label="Reset password"
          >
            <Key size={16} />
          </button>
          <button
            onClick={onDelete}
            className="p-1.5 text-gray-400 hover:text-red-600 rounded"
            title="Delete user"
            aria-label="Delete user"
          >
            <Trash2 size={16} />
          </button>
        </div>
      </td>
    </tr>
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
    <div className="fixed inset-0 bg-black/40 z-50 flex items-center justify-center">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-md p-6 mx-4">
        <h2 className="text-base font-semibold text-gray-900 mb-4">
          Reset password: {user.username}
        </h2>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              New password *
            </label>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              required
              autoFocus
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Confirm password *
            </label>
            <input
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              required
            />
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
              disabled={mutation.isPending}
              className="px-4 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {mutation.isPending ? "Resetting..." : "Reset"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
