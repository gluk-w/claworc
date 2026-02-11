import { useState } from "react";
import ProviderTable from "@/components/ProviderTable";
import { LLM_API_KEY_OPTIONS } from "@/components/DynamicApiKeyEditor";
import { useSettings } from "@/hooks/useSettings";
import type { InstanceCreatePayload } from "@/types/instance";

interface InstanceFormProps {
  onSubmit: (payload: InstanceCreatePayload) => void;
  onCancel: () => void;
  loading?: boolean;
}

export default function InstanceForm({
  onSubmit,
  onCancel,
  loading,
}: InstanceFormProps) {
  const [displayName, setDisplayName] = useState("");
  const [cpuRequest, setCpuRequest] = useState("500m");
  const [cpuLimit, setCpuLimit] = useState("2000m");
  const [memoryRequest, setMemoryRequest] = useState("1Gi");
  const [memoryLimit, setMemoryLimit] = useState("4Gi");
  const [storageHomebrew, setStorageHomebrew] = useState("10Gi");
  const [storageClawd, setStorageClawd] = useState("5Gi");
  const [storageChrome, setStorageChrome] = useState("5Gi");

  const [containerImage, setContainerImage] = useState("");
  const [vncResolution, setVncResolution] = useState("");

  const { data: settings } = useSettings();

  // API key overrides
  const [disabledProviders, setDisabledProviders] = useState<string[]>([]);
  const [apiKeys, setApiKeys] = useState<Record<string, string>>({});
  const [defaultModel, setDefaultModel] = useState(LLM_API_KEY_OPTIONS[0].value);

  // Brave key
  const [braveKey, setBraveKey] = useState("");

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!displayName.trim()) return;

    const payload: InstanceCreatePayload = {
      display_name: displayName.trim(),
      cpu_request: cpuRequest,
      cpu_limit: cpuLimit,
      memory_request: memoryRequest,
      memory_limit: memoryLimit,
      storage_homebrew: storageHomebrew,
      storage_clawd: storageClawd,
      storage_chrome: storageChrome,
      brave_api_key: braveKey || null,
      container_image: containerImage || null,
      vnc_resolution: vncResolution || null,
    };

    // Add dynamic API keys
    if (Object.keys(apiKeys).length > 0) {
      payload.api_keys = apiKeys;
    }

    // Add model config if any providers were disabled
    if (disabledProviders.length > 0) {
      payload.models = { disabled: disabledProviders, extra: [] };
    }

    // Add default model
    if (defaultModel) {
      payload.default_model = defaultModel;
    }

    onSubmit(payload);
  };

  const handleToggleEnabled = (key: string) => {
    setDisabledProviders((prev) =>
      prev.includes(key)
        ? prev.filter((p) => p !== key)
        : [...prev, key],
    );
    // If disabling the current default, clear it
    if (!disabledProviders.includes(key) && defaultModel === key) {
      setDefaultModel("");
    }
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-8">
      {/* General */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-4">General</h3>
        <div className="space-y-4">
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Display Name *
            </label>
            <input
              data-testid="display-name-input"
              type="text"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder="e.g., Bot Alpha"
              required
              autoFocus
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Agent Image Override
            </label>
            <input
              type="text"
              value={containerImage}
              onChange={(e) => setContainerImage(e.target.value)}
              placeholder={settings?.default_container_image ?? "glukw/openclaw-vnc-chrome:latest"}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              VNC Resolution Override
            </label>
            <input
              type="text"
              value={vncResolution}
              onChange={(e) => setVncResolution(e.target.value)}
              placeholder={settings?.default_vnc_resolution ?? "1920x1080"}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
        </div>
      </div>

      {/* API Key Overrides */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-4">API Key Overrides</h3>
        <p className="text-xs text-gray-500 mb-3">
          Leave empty to use global keys from Settings.
        </p>

        <ProviderTable
          globalApiKeys={settings?.api_keys ?? {}}
          instanceOverrides={[]}
          disabledProviders={disabledProviders}
          defaultModel={defaultModel}
          pendingNewKeys={apiKeys}
          pendingRemovals={{}}
          onToggleEnabled={handleToggleEnabled}
          onDefaultModelChange={setDefaultModel}
          onAddKey={(key, value) =>
            setApiKeys((prev) => ({ ...prev, [key]: value }))
          }
          onRemoveKey={() => {}}
          onUndoRemove={() => {}}
          onUndoAdd={(key) =>
            setApiKeys((prev) => {
              const next = { ...prev };
              delete next[key];
              return next;
            })
          }
          editable={true}
        />

        {/* Brave key (fixed field) */}
        <div className="pt-3 mt-3 border-t border-gray-200">
          <label className="block text-xs text-gray-500 mb-1">
            Brave API Key (web search)
          </label>
          <input
            type="password"
            value={braveKey}
            onChange={(e) => setBraveKey(e.target.value)}
            placeholder="Leave empty to use global key"
            className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>
      </div>

      {/* Container Resources */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-4">Container Resources</h3>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            {[
              { label: "CPU Request", value: cpuRequest, set: setCpuRequest },
              { label: "CPU Limit", value: cpuLimit, set: setCpuLimit },
              { label: "Memory Request", value: memoryRequest, set: setMemoryRequest },
              { label: "Memory Limit", value: memoryLimit, set: setMemoryLimit },
            ].map((field) => (
              <div key={field.label}>
                <label className="block text-xs text-gray-500 mb-1">
                  {field.label}
                </label>
                <input
                  type="text"
                  value={field.value}
                  onChange={(e) => field.set(e.target.value)}
                  className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>
            ))}
          </div>
          <div className="grid grid-cols-2 gap-4">
            {[
              { label: "Homebrew Storage", value: storageHomebrew, set: setStorageHomebrew },
              { label: "Clawd Storage", value: storageClawd, set: setStorageClawd },
              { label: "Chrome Storage", value: storageChrome, set: setStorageChrome },
            ].map((field) => (
              <div key={field.label}>
                <label className="block text-xs text-gray-500 mb-1">
                  {field.label}
                </label>
                <input
                  type="text"
                  value={field.value}
                  onChange={(e) => field.set(e.target.value)}
                  className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>
            ))}
          </div>
        </div>
      </div>

      <div className="flex justify-end gap-3">
        <button
          type="button"
          onClick={onCancel}
          className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
        >
          Cancel
        </button>
        <button
          data-testid="create-instance-button"
          type="submit"
          disabled={loading || !displayName.trim()}
          className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {loading ? "Creating..." : "Create"}
        </button>
      </div>
    </form>
  );
}
