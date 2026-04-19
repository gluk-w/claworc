import { useState } from "react";
import { AlertTriangle, Eye, EyeOff, Key, Pencil, Plus, RefreshCw } from "lucide-react";
import ProviderIcon from "@/components/ProviderIcon";
import ProviderModal from "@/components/ProviderModal";
import EnvVarsEditor from "@/components/EnvVarsEditor";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSettings, useUpdateSettings } from "@/hooks/useSettings";
import { useProviders } from "@/hooks/useProviders";
import { fetchSSHFingerprint, rotateSSHKey } from "@/api/ssh";
import { syncAllProviders } from "@/api/llm";
import { successToast, errorToast, envVarRestartToast } from "@/utils/toast";
import type { LLMProvider } from "@/types/instance";

import type { SettingsUpdatePayload } from "@/types/settings";

export default function SettingsPage() {
  const queryClient = useQueryClient();
  const { data: settings, isLoading } = useSettings();
  const updateMutation = useUpdateSettings();
  const { data: providers = [] } = useProviders();

  // Provider modal state
  const [modalOpen, setModalOpen] = useState(false);
  const [modalMode, setModalMode] = useState<"create" | "edit">("create");
  const [modalProvider, setModalProvider] = useState<LLMProvider | null>(null);

  // Sync all catalog providers
  const syncMutation = useMutation({
    mutationFn: syncAllProviders,
    onSuccess: () => {
      successToast("Catalog synced");
      queryClient.invalidateQueries({ queryKey: ["llm-providers"] });
    },
    onError: (err) => errorToast("Sync failed", err),
  });

  const fingerprint = useQuery({
    queryKey: ["ssh-fingerprint"],
    queryFn: fetchSSHFingerprint,
    staleTime: 60_000,
  });
  const rotateMutation = useMutation({
    mutationFn: rotateSSHKey,
    onSuccess: () => {
      successToast("SSH key rotated successfully");
      queryClient.invalidateQueries({ queryKey: ["ssh-fingerprint"] });
    },
    onError: (err) => {
      errorToast("Failed to rotate SSH key", err);
    },
  });

  const [pendingBraveKey, setPendingBraveKey] = useState<string | null>(null);
  const [resources, setResources] = useState<Record<string, string>>({});
  const [editingBrave, setEditingBrave] = useState(false);
  const [braveValue, setBraveValue] = useState("");
  const [showBrave, setShowBrave] = useState(false);

  if (isLoading || !settings) {
    return <div className="text-center py-12 text-gray-500">Loading...</div>;
  }

  const openCreateModal = () => {
    setModalMode("create");
    setModalProvider(null);
    setModalOpen(true);
  };

  const openEditModal = (p: LLMProvider) => {
    setModalMode("edit");
    setModalProvider(p);
    setModalOpen(true);
  };

  const handleSave = () => {
    const payload: SettingsUpdatePayload = { ...resources };
    if (pendingBraveKey !== null) payload.brave_api_key = pendingBraveKey;

    updateMutation.mutate(payload, {
      onSuccess: () => {
        setPendingBraveKey(null);
        setResources({});
        setEditingBrave(false);
        setBraveValue("");
      },
    });
  };

  // Env var changes save independently of the rest of the page so the editor
  // has its own Save button inside the card (see EnvVarsEditor).
  const handleSaveEnvVars = async (delta: { set: Record<string, string>; unset: string[] }) => {
    const payload: SettingsUpdatePayload = {};
    if (Object.keys(delta.set).length > 0) payload.env_vars_set = delta.set;
    if (delta.unset.length > 0) payload.env_vars_unset = delta.unset;
    const data = await updateMutation.mutateAsync(payload);
    for (const inst of data?.restarting_instances ?? []) {
      envVarRestartToast(inst.id, inst.display_name);
    }
  };

  const resourceFields: { key: string; label: string }[] = [
    { key: "default_cpu_request", label: "Default CPU Request" },
    { key: "default_cpu_limit", label: "Default CPU Limit" },
    { key: "default_memory_request", label: "Default Memory Request" },
    { key: "default_memory_limit", label: "Default Memory Limit" },
    { key: "default_storage_homebrew", label: "Default Homebrew Storage" },
    { key: "default_storage_home", label: "Default Home Storage" },
  ];

  const hasChanges =
    pendingBraveKey !== null ||
    Object.keys(resources).length > 0;

  return (
    <div>
      <h1 className="text-xl font-semibold text-gray-900 mb-6">Settings</h1>

      <div className="flex items-center gap-2 px-3 py-2 mb-6 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800">
        <AlertTriangle size={16} className="shrink-0" />
        Changing global API keys will update all instances that don't have overrides.
      </div>

      <div className="space-y-8 max-w-2xl">
        {/* Model API Keys — provider list */}
        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <div className="flex items-center justify-between mb-4">
            <h3 className="text-sm font-medium text-gray-900">Model API Keys</h3>
            <div className="flex items-center gap-2">
              <button
                type="button"
                onClick={() => syncMutation.mutate()}
                disabled={syncMutation.isPending}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
              >
                <RefreshCw size={12} className={syncMutation.isPending ? "animate-spin" : ""} />
                {syncMutation.isPending ? "Syncing..." : "Sync Models"}
              </button>
              <button
                type="button"
                onClick={openCreateModal}
                className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
              >
                <Plus size={12} />
                Add Provider
              </button>
            </div>
          </div>

          {providers.length === 0 ? (
            <p className="text-sm text-gray-400 italic">No providers configured.</p>
          ) : (
            <div className="divide-y divide-gray-100">
              {providers.map((p) => {
                const apiKeyDisplay = p.masked_api_key || "not set";
                const displayModels = (p.models || []).map((m) => m.id);
                return (
                  <div key={p.id}>
                    <div className="flex items-center py-3 -mx-2 px-2 rounded transition-colors">
                      <div className="min-w-0 flex-1 flex items-center gap-3">
                        <div className="shrink-0 w-6 h-6 flex items-center justify-center">
                          {p.provider ? (
                            <ProviderIcon provider={p.provider} size={22} />
                          ) : (
                            <span className="w-6 h-6 rounded-full bg-gray-100 flex items-center justify-center text-xs font-medium text-gray-500">
                              {p.name[0].toUpperCase()}
                            </span>
                          )}
                        </div>
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-2">
                            <span className="text-sm font-medium text-gray-900">{p.name}</span>
                            <span className="text-xs font-mono text-gray-400 bg-gray-100 px-1.5 py-0.5 rounded">{p.key}</span>
                          </div>
                          <p className="text-xs font-mono text-gray-500 mt-0.5 truncate">{p.base_url}</p>
                          <p className="text-xs text-gray-400 mt-0.5">
                            API key: <span className="font-mono">{apiKeyDisplay}</span>
                          </p>
                        </div>
                      </div>
                      <button
                        type="button"
                        onClick={() => openEditModal(p)}
                        className="shrink-0 ml-2 p-1 text-gray-400 hover:text-gray-600 rounded"
                        title="Edit provider"
                      >
                        <Pencil size={14} />
                      </button>
                    </div>
                    <div className="pb-3 px-2">
                      {displayModels.length === 0 ? (
                        <p className="text-xs text-gray-400 italic">No models available.</p>
                      ) : (
                        <div className="flex flex-wrap gap-1">
                          {displayModels.map((id) => (
                            <span key={id} className="font-mono text-xs bg-gray-100 text-gray-600 px-1.5 py-0.5 rounded">
                              {id}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </div>

        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h3 className="text-sm font-medium text-gray-900 mb-4">Brave API Key</h3>
          <p className="text-xs text-gray-500 mb-3">Used for web search (not an LLM provider key).</p>
          {editingBrave ? (
            <div className="flex gap-2">
              <div className="relative flex-1">
                <input
                  type={showBrave ? "text" : "password"}
                  value={braveValue}
                  onChange={(e) => {
                    setBraveValue(e.target.value);
                    setPendingBraveKey(e.target.value);
                  }}
                  className="w-full px-3 py-1.5 pr-10 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="Enter Brave API key"
                />
                <button
                  type="button"
                  onClick={() => setShowBrave(!showBrave)}
                  className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
                >
                  {showBrave ? <EyeOff size={14} /> : <Eye size={14} />}
                </button>
              </div>
              <button
                type="button"
                onClick={() => { setEditingBrave(false); setBraveValue(""); setPendingBraveKey(null); }}
                className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
              >
                Cancel
              </button>
            </div>
          ) : (
            <div className="flex items-center gap-2">
              <span className="text-sm text-gray-500 font-mono">
                {pendingBraveKey !== null
                  ? pendingBraveKey ? "****" + pendingBraveKey.slice(-4) : "(not set)"
                  : settings.brave_api_key || "(not set)"}
              </span>
              <button type="button" onClick={() => setEditingBrave(true)} className="text-xs text-blue-600 hover:text-blue-800">
                Change
              </button>
            </div>
          )}
        </div>

        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h3 className="text-sm font-medium text-gray-900 mb-4">Agent Image</h3>
          <div className="space-y-4">
            <div>
              <label className="block text-xs text-gray-500 mb-1">Default Container Image</label>
              <input
                type="text"
                defaultValue={settings.default_container_image ?? ""}
                onChange={(e) => setResources((r) => ({ ...r, default_container_image: e.target.value }))}
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-xs text-gray-500 mb-1">Default VNC Resolution</label>
              <input
                type="text"
                defaultValue={settings.default_vnc_resolution ?? "1920x1080"}
                onChange={(e) => setResources((r) => ({ ...r, default_vnc_resolution: e.target.value }))}
                placeholder="e.g., 1920x1080"
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-xs text-gray-500 mb-1">Default Timezone</label>
              <input
                type="text"
                defaultValue={settings.default_timezone ?? ""}
                onChange={(e) => setResources((r) => ({ ...r, default_timezone: e.target.value }))}
                placeholder="e.g., America/New_York"
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-xs text-gray-500 mb-1">Default User-Agent</label>
              <input
                type="text"
                defaultValue={settings.default_user_agent ?? ""}
                onChange={(e) => setResources((r) => ({ ...r, default_user_agent: e.target.value }))}
                placeholder="Leave empty to use browser default"
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
          </div>
        </div>

        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h3 className="text-sm font-medium text-gray-900 mb-4">Default Resource Limits</h3>
          <div className="grid grid-cols-2 gap-4">
            {resourceFields.map((field) => (
              <div key={field.key}>
                <label className="block text-xs text-gray-500 mb-1">{field.label}</label>
                <input
                  type="text"
                  defaultValue={(settings as Record<string, any>)[field.key] ?? ""}
                  onChange={(e) => setResources((r) => ({ ...r, [field.key]: e.target.value }))}
                  className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>
            ))}
          </div>
        </div>

        <EnvVarsEditor
          values={settings.default_env_vars ?? {}}
          title="Environment Variables"
          description="Passed to every OpenClaw instance at container start. Per-instance values override these when the name matches. Values are encrypted at rest. Saving restarts every running instance so the change takes effect immediately."
          onSave={handleSaveEnvVars}
          isSaving={updateMutation.isPending}
          emptyMessage="No global environment variables set."
        />

        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <div className="flex items-center justify-between mb-2">
            <h3 className="text-sm font-medium text-gray-900 flex items-center gap-1.5">
              <Key size={14} />
              SSH Tunnel
            </h3>
            <button
              onClick={() => rotateMutation.mutate()}
              disabled={rotateMutation.isPending}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <RefreshCw size={12} className={rotateMutation.isPending ? "animate-spin" : ""} />
              {rotateMutation.isPending ? "Rotating..." : "Rotate Key"}
            </button>
          </div>
          <p className="text-xs text-gray-500 mb-3">
            Global control plane SSH key used to connect to all instances.
          </p>
          {fingerprint.isLoading && <p className="text-xs text-gray-400">Loading...</p>}
          {fingerprint.isError && <p className="text-xs text-red-600">Failed to load fingerprint.</p>}
          {fingerprint.data && (
            <div className="bg-gray-50 border border-gray-200 rounded-md p-3">
              <div className="mb-2">
                <dt className="text-xs text-gray-500 mb-0.5">Fingerprint</dt>
                <dd className="text-xs font-mono text-gray-900 break-all">{fingerprint.data.fingerprint}</dd>
              </div>
              <div>
                <dt className="text-xs text-gray-500 mb-0.5">Public Key</dt>
                <dd className="text-xs font-mono text-gray-700 break-all whitespace-pre-wrap leading-relaxed">
                  {fingerprint.data.public_key.trim()}
                </dd>
              </div>
            </div>
          )}
        </div>

        <div className="flex justify-end">
          <button
            onClick={handleSave}
            disabled={updateMutation.isPending || !hasChanges}
            className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {updateMutation.isPending ? "Saving..." : "Save Settings"}
          </button>
        </div>
      </div>

      <ProviderModal
        open={modalOpen}
        mode={modalMode}
        provider={modalProvider ?? undefined}
        existingKeys={providers.map((p) => p.key)}
        onClose={() => setModalOpen(false)}
        onSaved={() => {}}
      />
    </div>
  );
}
