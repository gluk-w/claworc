import { useState } from "react";
import { Trash2 } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { deleteInstancePasskey, fetchInstancePasskeys } from "@common/api/instances";
import { useUpdateInstance } from "@common/hooks/useInstances";
import type { BrowserPasskey, VirtualAuthenticator } from "@common/types/instance";
import { errorToast, successToast } from "@common/utils/toast";
import ConfirmDialog from "@common/components/ConfirmDialog";

interface Props {
  instanceId: number;
  value: VirtualAuthenticator;
  /** Legacy embedded-browser Agents have no on-demand browser to inject into. */
  isLegacy: boolean;
}

export const VIRTUAL_AUTHENTICATOR_OPTIONS: { value: VirtualAuthenticator; label: string }[] = [
  { value: "", label: "Off" },
  { value: "platform", label: "Fingerprint (built-in)" },
  { value: "security_key", label: "Security key (USB)" },
  { value: "both", label: "Fingerprint and security key" },
];

export const VIRTUAL_AUTHENTICATOR_HINT =
  "Simulates a fingerprint reader or security key in the Agent's browser so sites can use passkeys. " +
  "Passkeys are stored encrypted by Claworc and survive browser restarts. Anyone who can control this Agent can use them.";

const KIND_LABEL: Record<BrowserPasskey["authenticator"], string> = {
  platform: "Fingerprint",
  security_key: "Security key",
};

function formatDate(s?: string) {
  return s ? new Date(s).toLocaleString() : "Never";
}

export default function VirtualAuthenticatorSection({ instanceId, value, isLegacy }: Props) {
  const qc = useQueryClient();
  const updateMutation = useUpdateInstance();
  const [pendingDelete, setPendingDelete] = useState<BrowserPasskey | null>(null);

  const passkeys = useQuery({
    queryKey: ["instances", instanceId, "passkeys"],
    queryFn: () => fetchInstancePasskeys(instanceId),
    refetchInterval: 15_000,
  });

  const deleteMutation = useMutation({
    mutationFn: (p: BrowserPasskey) => deleteInstancePasskey(instanceId, p.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["instances", instanceId, "passkeys"] });
      successToast("Passkey deleted");
    },
    onError: (err) => errorToast("Failed to delete passkey", err),
  });

  const rows = passkeys.data ?? [];

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <h3 className="text-sm font-medium text-gray-900 mb-4">Passkeys</h3>

      {isLegacy ? (
        <p className="text-xs text-gray-500">
          Migrate this Agent to the on-demand browser to use a virtual fingerprint reader or security key.
        </p>
      ) : (
        <>
          <div>
            <label htmlFor="virtual-authenticator" className="block text-xs text-gray-500 mb-1">
              Virtual passkey device
            </label>
            <select
              id="virtual-authenticator"
              value={value}
              disabled={updateMutation.isPending}
              onChange={(e) =>
                updateMutation.mutate({
                  id: instanceId,
                  payload: { virtual_authenticator: e.target.value as VirtualAuthenticator },
                })
              }
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white disabled:opacity-50"
            >
              {VIRTUAL_AUTHENTICATOR_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
            <p className="text-xs text-gray-400 mt-1">
              {VIRTUAL_AUTHENTICATOR_HINT} Changes apply to the running browser immediately.
            </p>
          </div>

          <div className="mt-4">
            {passkeys.isLoading ? (
              <p className="text-xs text-gray-400">Loading passkeys...</p>
            ) : passkeys.isError ? (
              <p className="text-xs text-red-600">Failed to load passkeys.</p>
            ) : rows.length === 0 ? (
              <p className="text-xs text-gray-400">
                No passkeys yet. Passkeys created in the Agent's browser appear here.
              </p>
            ) : (
              <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
                <table className="w-full text-sm">
                  <thead className="bg-gray-50 border-b border-gray-200">
                    <tr>
                      <th className="text-left px-4 py-3 font-medium text-gray-600">Site</th>
                      <th className="text-left px-4 py-3 font-medium text-gray-600">Account</th>
                      <th className="text-left px-4 py-3 font-medium text-gray-600">Device</th>
                      <th className="text-left px-4 py-3 font-medium text-gray-600">Last used</th>
                      <th className="px-4 py-3" />
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((p) => (
                      <tr key={p.id} className="border-b border-gray-100 last:border-0">
                        <td className="px-4 py-3 text-gray-900">{p.rp_id || "Unknown"}</td>
                        <td className="px-4 py-3 text-gray-500">
                          {p.user_display_name || p.user_name || (p.is_resident ? "Unnamed" : "Second factor")}
                        </td>
                        <td className="px-4 py-3 text-gray-500">{KIND_LABEL[p.authenticator] ?? p.authenticator}</td>
                        <td className="px-4 py-3 text-gray-500" title={`Created ${formatDate(p.created_at)}`}>
                          {formatDate(p.last_used_at)}
                        </td>
                        <td className="px-4 py-3 text-right">
                          <button
                            type="button"
                            onClick={() => setPendingDelete(p)}
                            disabled={deleteMutation.isPending}
                            className="p-1 text-gray-400 hover:text-red-600 transition-colors"
                            title="Delete passkey"
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
          </div>
        </>
      )}

      {pendingDelete && (
        <ConfirmDialog
          title="Delete passkey"
          message={`Delete the passkey for ${pendingDelete.rp_id || "this site"}? The Agent will no longer be able to sign in with it. This can't be undone.`}
          confirmLabel="Delete"
          onConfirm={() => {
            deleteMutation.mutate(pendingDelete);
            setPendingDelete(null);
          }}
          onCancel={() => setPendingDelete(null)}
        />
      )}
    </div>
  );
}
