import { useState, type FormEvent } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Trash2,
  ShieldCheck,
  Shield,
  Fingerprint,
  KeyRound,
  TerminalSquare,
} from "lucide-react";
import { startRegistration } from "@simplewebauthn/browser";
import { successToast, errorToast, infoToast } from "@common/utils/toast";
import { useAuth } from "@common/contexts/AuthContext";
import {
  listWebAuthnCredentials,
  deleteWebAuthnCredential,
  webAuthnRegisterBegin,
  webAuthnRegisterFinish,
  changePassword,
} from "@common/api/auth";
import {
  listSSHKeys,
  generateSSHKey,
  deleteSSHKey,
  getSSHGatewayInfo,
} from "@common/api/sshKeys";
import Page from "@common/components/Page";

export default function AccountPage() {
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const [showRegister, setShowRegister] = useState(false);
  const [showChangePassword, setShowChangePassword] = useState(false);

  const { data: credentials = [], isLoading } = useQuery({
    queryKey: ["webauthn-credentials"],
    queryFn: listWebAuthnCredentials,
  });

  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteWebAuthnCredential(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["webauthn-credentials"] });
      successToast("Passkey deleted");
    },
    onError: (error) => errorToast("Failed to delete passkey", error),
  });

  return (
    <Page title="Profile">
      <div className="bg-white rounded-lg border border-gray-200 p-6 mb-6">
        <h2 className="text-sm font-medium text-gray-500 mb-3">
          Account Info
        </h2>
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <span className="text-lg font-medium text-gray-900">
              {user?.username}
            </span>
            <span
              className={`inline-flex items-center gap-1 px-2 py-0.5 text-xs font-medium rounded-full ${
                user?.role === "admin"
                  ? "bg-purple-50 text-purple-700"
                  : "bg-gray-100 text-gray-600"
              }`}
            >
              {user?.role === "admin" ? (
                <ShieldCheck size={12} />
              ) : (
                <Shield size={12} />
              )}
              {user?.role}
            </span>
          </div>
          <button
            onClick={() => setShowChangePassword(true)}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-gray-700 border border-gray-300 rounded-md hover:bg-gray-50"
          >
            <KeyRound size={16} />
            Change Password
          </button>
        </div>
      </div>

      <SSHAccessCard username={user?.username || ""} />

      <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
        <div className="flex items-center justify-between px-4 py-3 border-b border-gray-200">
          <h2 className="text-sm font-medium text-gray-500">Passkeys</h2>
          <button
            onClick={() => setShowRegister(true)}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
          >
            <Fingerprint size={16} />
            Register Passkey
          </button>
        </div>

        {isLoading ? (
          <div className="px-4 py-6 text-sm text-gray-500">
            Loading passkeys...
          </div>
        ) : credentials.length === 0 ? (
          <div className="px-4 py-6 text-sm text-gray-500">
            No passkeys registered yet.
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead className="bg-gray-50 border-b border-gray-200">
              <tr>
                <th className="text-left px-4 py-3 font-medium text-gray-600">
                  Name
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
              {credentials.map((cred) => (
                <tr
                  key={cred.id}
                  className="border-b border-gray-100 last:border-0"
                >
                  <td className="px-4 py-3 font-medium text-gray-900">
                    {cred.name || "Unnamed"}
                  </td>
                  <td className="px-4 py-3 text-gray-500">
                    {cred.created_at
                      ? new Date(cred.created_at).toLocaleDateString()
                      : "—"}
                  </td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => {
                        if (
                          confirm(
                            `Delete passkey "${cred.name || "Unnamed"}"?`,
                          )
                        ) {
                          deleteMut.mutate(cred.id);
                        }
                      }}
                      className="p-1.5 text-gray-400 hover:text-red-600 rounded"
                      title="Delete passkey"
                    >
                      <Trash2 size={16} />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {showRegister && (
        <RegisterPasskeyDialog
          onClose={() => setShowRegister(false)}
          queryClient={queryClient}
        />
      )}

      {showChangePassword && (
        <ChangePasswordDialog onClose={() => setShowChangePassword(false)} />
      )}
    </Page>
  );
}

function SSHAccessCard({ username }: { username: string }) {
  const queryClient = useQueryClient();
  const [showKeyDialog, setShowKeyDialog] = useState(false);

  const { data: info } = useQuery({
    queryKey: ["ssh-gateway-info"],
    queryFn: getSSHGatewayInfo,
  });
  const { data: keys = [], isLoading } = useQuery({
    queryKey: ["ssh-keys"],
    queryFn: listSSHKeys,
  });

  const keyFileName = `claworc_${username || "user"}.pem`;

  const generateMut = useMutation({
    mutationFn: () => generateSSHKey(),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ["ssh-keys"] });
      // One-time download of the private key: it is never stored server-side.
      const blob = new Blob([res.private_key], {
        type: "application/x-pem-file",
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = keyFileName;
      a.click();
      URL.revokeObjectURL(url);
      setShowKeyDialog(true);
    },
    onError: (error) => errorToast("Failed to generate SSH key", error),
  });

  const deleteMut = useMutation({
    mutationFn: (id: number) => deleteSSHKey(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["ssh-keys"] });
      successToast("SSH key revoked");
    },
    onError: (error) => errorToast("Failed to revoke SSH key", error),
  });

  return (
    <div className="bg-white rounded-lg border border-gray-200 overflow-hidden mb-6">
      <div className="flex items-center justify-between px-4 py-3 border-b border-gray-200">
        <h2 className="text-sm font-medium text-gray-500">SSH Access</h2>
        <button
          onClick={() => generateMut.mutate()}
          disabled={generateMut.isPending || info?.enabled === false}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
        >
          <TerminalSquare size={16} />
          {generateMut.isPending ? "Generating..." : "Generate SSH Key"}
        </button>
      </div>

      {info?.enabled === false ? (
        <div className="px-4 py-6 text-sm text-gray-500">
          SSH access is disabled by the administrator.
        </div>
      ) : isLoading ? (
        <div className="px-4 py-6 text-sm text-gray-500">Loading SSH keys...</div>
      ) : keys.length === 0 ? (
        <div className="px-4 py-6 text-sm text-gray-500">
          No SSH keys yet. Generate one to connect to your agents from a
          terminal. The private key is downloaded once and never stored on the
          server.
        </div>
      ) : (
        <table className="w-full text-sm">
          <thead className="bg-gray-50 border-b border-gray-200">
            <tr>
              <th className="text-left px-4 py-3 font-medium text-gray-600">
                Fingerprint
              </th>
              <th className="text-left px-4 py-3 font-medium text-gray-600">
                Created
              </th>
              <th className="text-left px-4 py-3 font-medium text-gray-600">
                Last Used
              </th>
              <th className="text-right px-4 py-3 font-medium text-gray-600">
                Actions
              </th>
            </tr>
          </thead>
          <tbody>
            {keys.map((key) => (
              <tr key={key.id} className="border-b border-gray-100 last:border-0">
                <td className="px-4 py-3 text-gray-500 font-mono text-xs">
                  {key.fingerprint}
                </td>
                <td className="px-4 py-3 text-gray-500">
                  {key.created_at
                    ? new Date(key.created_at).toLocaleDateString()
                    : "—"}
                </td>
                <td className="px-4 py-3 text-gray-500">
                  {key.last_used_at
                    ? new Date(key.last_used_at).toLocaleString()
                    : "Never"}
                </td>
                <td className="px-4 py-3 text-right">
                  <button
                    onClick={() => {
                      if (confirm("Revoke this SSH key?")) {
                        deleteMut.mutate(key.id);
                      }
                    }}
                    className="p-1.5 text-gray-400 hover:text-red-600 rounded"
                    title="Revoke SSH key"
                  >
                    <Trash2 size={16} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {showKeyDialog && (
        <SSHKeyGeneratedDialog
          username={username}
          keyFileName={keyFileName}
          gatewayPort={info?.port}
          gatewayHost={info?.host || window.location.hostname}
          onClose={() => setShowKeyDialog(false)}
        />
      )}
    </div>
  );
}

function SSHKeyGeneratedDialog({
  username,
  keyFileName,
  gatewayPort,
  gatewayHost,
  onClose,
}: {
  username: string;
  keyFileName: string;
  gatewayPort?: number;
  gatewayHost: string;
  onClose: () => void;
}) {
  const portFlag = gatewayPort && gatewayPort !== 22 ? ` -p ${gatewayPort}` : "";
  const sshCommand = `ssh -i ~/.ssh/${keyFileName}${portFlag} ${username || "<you>"}+<agent-name>@${gatewayHost}`;

  return (
    <div
      className="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
      onKeyDown={(e) => { if (e.key === "Escape") onClose(); }}
    >
      <div className="bg-white rounded-lg shadow-lg w-full max-w-lg p-6">
        <h2 className="text-lg font-semibold mb-2">SSH Key Generated</h2>
        <p className="text-sm text-gray-600 mb-4">
          Your private key <span className="font-mono">{keyFileName}</span> is
          being downloaded. It is not stored on the server, so keep the file
          safe — if you lose it, generate a new key.
        </p>

        <div className="space-y-3 text-sm text-gray-700">
          <div>
            <div className="font-medium text-gray-900 mb-1">
              1. Move the key into place and restrict its permissions:
            </div>
            <code className="block px-3 py-2 text-xs font-mono bg-gray-900 text-gray-100 rounded-md overflow-x-auto whitespace-nowrap">
              mv ~/Downloads/{keyFileName} ~/.ssh/ && chmod 600 ~/.ssh/{keyFileName}
            </code>
          </div>
          <div>
            <div className="font-medium text-gray-900 mb-1">
              2. Connect to an agent by name:
            </div>
            <code className="block px-3 py-2 text-xs font-mono bg-gray-900 text-gray-100 rounded-md overflow-x-auto whitespace-nowrap">
              {sshCommand}
            </code>
          </div>
        </div>

        <div className="flex justify-end pt-5">
          <button
            onClick={onClose}
            autoFocus
            className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
          >
            Done
          </button>
        </div>
      </div>
    </div>
  );
}

function RegisterPasskeyDialog({
  onClose,
  queryClient,
}: {
  onClose: () => void;
  queryClient: ReturnType<typeof useQueryClient>;
}) {
  const [name, setName] = useState("");
  const [registering, setRegistering] = useState(false);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;

    setRegistering(true);
    try {
      const resp = (await webAuthnRegisterBegin()) as { publicKey: Parameters<typeof startRegistration>[0]["optionsJSON"] };
      const credential = await startRegistration({
        optionsJSON: resp.publicKey,
      });
      await webAuthnRegisterFinish(credential, name.trim());
      queryClient.invalidateQueries({ queryKey: ["webauthn-credentials"] });
      successToast("Passkey registered");
      onClose();
    } catch (err) {
      if (
        err instanceof Error &&
        err.name === "NotAllowedError"
      ) {
        infoToast("Registration cancelled");
      } else {
        errorToast("Failed to register passkey", err);
      }
    } finally {
      setRegistering(false);
    }
  };

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-white rounded-lg shadow-lg w-full max-w-sm p-6">
        <h2 className="text-lg font-semibold mb-4">Register Passkey</h2>
        <form onSubmit={handleSubmit} className="space-y-3">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Passkey Name
            </label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. MacBook Touch ID"
              className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm"
              required
              autoFocus
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
              disabled={registering}
              className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {registering ? "Registering..." : "Register"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

function ChangePasswordDialog({ onClose }: { onClose: () => void }) {
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");

    if (newPassword !== confirmPassword) {
      setError("New passwords do not match");
      return;
    }

    setSaving(true);
    try {
      await changePassword({
        current_password: currentPassword,
        new_password: newPassword,
      });
      successToast("Password changed successfully");
      onClose();
    } catch (err) {
      errorToast("Failed to change password", err);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div
      className="fixed inset-0 bg-black/50 flex items-center justify-center z-50"
      onKeyDown={(e) => { if (e.key === "Escape") onClose(); }}
    >
      <div className="bg-white rounded-lg shadow-lg w-full max-w-sm p-6">
        <h2 className="text-lg font-semibold mb-4">Change Password</h2>

        {error && (
          <div className="mb-3 p-3 text-sm text-red-700 bg-red-50 border border-red-200 rounded-md">
            {error}
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-3">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Current Password
            </label>
            <input
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              required
              autoFocus
              autoComplete="current-password"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              New Password
            </label>
            <input
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              required
              autoComplete="new-password"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Confirm New Password
            </label>
            <input
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              required
              autoComplete="new-password"
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
              disabled={saving}
              className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {saving ? "Saving..." : "Change Password"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
