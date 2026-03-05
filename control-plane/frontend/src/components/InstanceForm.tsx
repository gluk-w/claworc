import { useState } from "react";
import { useSettings } from "@/hooks/useSettings";
import { useProviders } from "@/hooks/useProviders";
import ProviderIcon from "@/components/ProviderIcon";
import { MODEL_CATALOG } from "@/data/model-catalog";
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
  const [storageHome, setStorageHome] = useState("10Gi");

  const [containerImage, setContainerImage] = useState("");
  const [vncResolution, setVncResolution] = useState("");
  const [timezone, setTimezone] = useState("");
  const [userAgent, setUserAgent] = useState("");

  const { data: settings } = useSettings();
  const { data: allProviders = [] } = useProviders();

  // Gateway providers + model selection
  const [enabledProviders, setEnabledProviders] = useState<number[]>([]);
  const [providerModels, setProviderModels] = useState<Record<number, string[]>>({});

  // Brave key
  const [braveKey, setBraveKey] = useState("");

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!displayName.trim()) return;

    // Build provider-prefixed extra models
    const extraModels: string[] = [];
    for (const p of allProviders) {
      for (const m of providerModels[p.id] ?? []) {
        extraModels.push(`${p.key}/${m}`);
      }
    }

    const payload: InstanceCreatePayload = {
      display_name: displayName.trim(),
      cpu_request: cpuRequest,
      cpu_limit: cpuLimit,
      memory_request: memoryRequest,
      memory_limit: memoryLimit,
      storage_homebrew: storageHomebrew,
      storage_home: storageHome,
      brave_api_key: braveKey || null,
      container_image: containerImage || null,
      vnc_resolution: vncResolution || null,
      timezone: timezone || null,
      user_agent: userAgent || null,
    };

    if (enabledProviders.length > 0) {
      payload.enabled_providers = enabledProviders;
    }
    if (extraModels.length > 0) {
      payload.models = { disabled: [], extra: extraModels };
    }

    onSubmit(payload);
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
              placeholder={settings?.default_container_image ?? "glukw/openclaw-vnc-chromium:latest"}
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
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Timezone Override
            </label>
            <input
              type="text"
              value={timezone}
              onChange={(e) => setTimezone(e.target.value)}
              placeholder={settings?.default_timezone ?? "America/New_York"}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              User-Agent Override
            </label>
            <input
              type="text"
              value={userAgent}
              onChange={(e) => setUserAgent(e.target.value)}
              placeholder={settings?.default_user_agent || "Browser default"}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
        </div>
      </div>

      {/* Model Providers */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-1">Model Providers</h3>
        <p className="text-xs text-gray-500 mb-4">
          Select providers and models to configure via the LLM gateway. Providers are set up in{" "}
          <span className="font-medium">Settings → Model API Keys</span>.
        </p>

        {allProviders.length === 0 ? (
          <p className="text-sm text-gray-400 italic">
            No providers configured. Add providers in Settings → Model API Keys first.
          </p>
        ) : (
          <div className="divide-y divide-gray-100">
            {allProviders.map((p) => {
              const enabled = enabledProviders.includes(p.id);
              const selectedModels = providerModels[p.id] ?? [];
              const catalog = MODEL_CATALOG.find((c) => c.key === p.provider);
              const iconKey = catalog?.lobeIconKey;
              return (
                <div key={p.id} className="py-3 first:pt-0 last:pb-0">
                  <label className="flex items-center gap-3 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={enabled}
                      onChange={() => {
                        setEnabledProviders((prev) =>
                          enabled ? prev.filter((id) => id !== p.id) : [...prev, p.id],
                        );
                        if (enabled) {
                          setProviderModels((prev) => {
                            const next = { ...prev };
                            delete next[p.id];
                            return next;
                          });
                        }
                      }}
                      className="rounded border-gray-300"
                    />
                    {iconKey ? (
                      <ProviderIcon provider={iconKey} size={18} />
                    ) : (
                      <span className="w-4 h-4 rounded-full bg-gray-200 flex items-center justify-center text-xs font-medium text-gray-500 shrink-0">
                        {p.name[0].toUpperCase()}
                      </span>
                    )}
                    <span className="text-sm text-gray-900">{p.name}</span>
                  </label>
                  {enabled && catalog && !catalog.dynamic && catalog.models.length > 0 && (
                    <div className="ml-7 mt-2 grid grid-cols-2 gap-x-6 gap-y-1">
                      {catalog.models.map((m) => (
                        <label key={m.id} className="flex items-center gap-2 cursor-pointer">
                          <input
                            type="checkbox"
                            checked={selectedModels.includes(m.id)}
                            onChange={() => {
                              setProviderModels((prev) => {
                                const current = prev[p.id] ?? [];
                                const next = current.includes(m.id)
                                  ? current.filter((x) => x !== m.id)
                                  : [...current, m.id];
                                return { ...prev, [p.id]: next };
                              });
                            }}
                            className="rounded border-gray-300"
                          />
                          <span className="text-xs font-mono text-gray-700 truncate">{m.id}</span>
                        </label>
                      ))}
                    </div>
                  )}
                  {enabled && (!catalog || catalog.dynamic) && (
                    <p className="ml-7 mt-1 text-xs text-gray-400 italic">Models determined dynamically.</p>
                  )}
                </div>
              );
            })}
          </div>
        )}

        {/* Brave key */}
        <div className="pt-4 mt-4 border-t border-gray-200">
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
              { label: "Home Storage", value: storageHome, set: setStorageHome },
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
