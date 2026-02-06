import { useState } from "react";
import { AlertTriangle, Eye, EyeOff } from "lucide-react";
import DynamicApiKeyEditor from "@/components/DynamicApiKeyEditor";
import { useSettings, useUpdateSettings } from "@/hooks/useSettings";
import type { SettingsUpdatePayload } from "@/types/settings";

export default function SettingsPage() {
  const { data: settings, isLoading } = useSettings();
  const updateMutation = useUpdateSettings();

  // Pending changes to send on save
  const [pendingApiKeys, setPendingApiKeys] = useState<Record<string, string>>({});
  const [pendingDeleteKeys, setPendingDeleteKeys] = useState<string[]>([]);
  const [pendingBraveKey, setPendingBraveKey] = useState<string | null>(null);
  const [resources, setResources] = useState<Record<string, string>>({});

  // Brave key editing state
  const [editingBrave, setEditingBrave] = useState(false);
  const [braveValue, setBraveValue] = useState("");
  const [showBrave, setShowBrave] = useState(false);

  if (isLoading || !settings) {
    return <div className="text-center py-12 text-gray-500">Loading...</div>;
  }

  // Compute the displayed api_keys (merge server state with pending)
  const displayedApiKeys = { ...settings.api_keys };
  for (const k of pendingDeleteKeys) {
    delete displayedApiKeys[k];
  }
  for (const [k, v] of Object.entries(pendingApiKeys)) {
    displayedApiKeys[k] = "****" + (v.length > 4 ? v.slice(-4) : "");
  }

  const handleSave = () => {
    const payload: SettingsUpdatePayload = { ...resources };

    if (Object.keys(pendingApiKeys).length > 0) {
      payload.api_keys = pendingApiKeys;
    }
    if (pendingDeleteKeys.length > 0) {
      payload.delete_api_keys = pendingDeleteKeys;
    }
    if (pendingBraveKey !== null) {
      payload.brave_api_key = pendingBraveKey;
    }

    updateMutation.mutate(payload, {
      onSuccess: () => {
        setPendingApiKeys({});
        setPendingDeleteKeys([]);
        setPendingBraveKey(null);
        setResources({});
        setEditingBrave(false);
        setBraveValue("");
      },
    });
  };

  const resourceFields: { key: string; label: string }[] = [
    { key: "default_cpu_request", label: "Default CPU Request" },
    { key: "default_cpu_limit", label: "Default CPU Limit" },
    { key: "default_memory_request", label: "Default Memory Request" },
    { key: "default_memory_limit", label: "Default Memory Limit" },
    { key: "default_storage_homebrew", label: "Default Homebrew Storage" },
    { key: "default_storage_clawd", label: "Default Clawd Storage" },
    { key: "default_storage_chrome", label: "Default Chrome Storage" },
  ];

  const hasChanges =
    Object.keys(pendingApiKeys).length > 0 ||
    pendingDeleteKeys.length > 0 ||
    pendingBraveKey !== null ||
    Object.keys(resources).length > 0;

  return (
    <div>
      <h1 className="text-xl font-semibold text-gray-900 mb-6">Settings</h1>

      <div className="flex items-center gap-2 px-3 py-2 mb-6 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800">
        <AlertTriangle size={16} className="shrink-0" />
        Changing global API keys will update all instances that don't have
        overrides.
      </div>

      <div className="space-y-8 max-w-2xl">
        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h3 className="text-sm font-medium text-gray-900 mb-4">
            Model API Keys
          </h3>
          <DynamicApiKeyEditor
            keys={displayedApiKeys}
            onUpdate={(keyName, value) => {
              setPendingApiKeys((prev) => ({ ...prev, [keyName]: value }));
              setPendingDeleteKeys((prev) => prev.filter((k) => k !== keyName));
            }}
            onDelete={(keyName) => {
              setPendingDeleteKeys((prev) =>
                prev.includes(keyName) ? prev : [...prev, keyName],
              );
              setPendingApiKeys((prev) => {
                const next = { ...prev };
                delete next[keyName];
                return next;
              });
            }}
          />
        </div>

        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h3 className="text-sm font-medium text-gray-900 mb-4">
            Brave API Key
          </h3>
          <p className="text-xs text-gray-500 mb-3">
            Used for web search (not an LLM provider key).
          </p>
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
                onClick={() => {
                  setEditingBrave(false);
                  setBraveValue("");
                  setPendingBraveKey(null);
                }}
                className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
              >
                Cancel
              </button>
            </div>
          ) : (
            <div className="flex items-center gap-2">
              <span className="text-sm text-gray-500 font-mono">
                {pendingBraveKey !== null
                  ? pendingBraveKey
                    ? "****" + pendingBraveKey.slice(-4)
                    : "(not set)"
                  : settings.brave_api_key || "(not set)"}
              </span>
              <button
                type="button"
                onClick={() => setEditingBrave(true)}
                className="text-xs text-blue-600 hover:text-blue-800"
              >
                Change
              </button>
            </div>
          )}
        </div>

        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h3 className="text-sm font-medium text-gray-900 mb-4">
            Agent Image
          </h3>
          <div className="space-y-4">
            <div>
              <label className="block text-xs text-gray-500 mb-1">
                Default Container Image
              </label>
              <input
                type="text"
                defaultValue={settings.default_container_image ?? ""}
                onChange={(e) =>
                  setResources((r) => ({
                    ...r,
                    default_container_image: e.target.value,
                  }))
                }
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-xs text-gray-500 mb-1">
                Default VNC Resolution
              </label>
              <input
                type="text"
                defaultValue={settings.default_vnc_resolution ?? "1920x1080"}
                onChange={(e) =>
                  setResources((r) => ({
                    ...r,
                    default_vnc_resolution: e.target.value,
                  }))
                }
                placeholder="e.g., 1920x1080"
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
          </div>
        </div>

        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h3 className="text-sm font-medium text-gray-900 mb-4">
            Default Resource Limits
          </h3>
          <div className="grid grid-cols-2 gap-4">
            {resourceFields.map((field) => (
              <div key={field.key}>
                <label className="block text-xs text-gray-500 mb-1">
                  {field.label}
                </label>
                <input
                  type="text"
                  defaultValue={
                    (settings as Record<string, any>)[field.key] ?? ""
                  }
                  onChange={(e) =>
                    setResources((r) => ({ ...r, [field.key]: e.target.value }))
                  }
                  className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>
            ))}
          </div>
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
    </div>
  );
}
