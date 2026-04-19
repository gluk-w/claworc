import { useState } from "react";
import { useQueries } from "@tanstack/react-query";
import { useSettings } from "@/hooks/useSettings";
import { useProviders } from "@/hooks/useProviders";
import { fetchCatalogProviderDetail } from "@/api/llm";
import type { CatalogProviderDetail } from "@/api/llm";
import ProviderModelSelector from "@/components/ProviderModelSelector";
import KeyValueListEditor from "@/components/KeyValueListEditor";
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

  // Fetch catalog model lists for all catalog providers
  const catalogKeys = [...new Set(allProviders.filter((p) => p.provider).map((p) => p.provider))];
  const catalogDetailResults = useQueries({
    queries: catalogKeys.map((key) => ({
      queryKey: ["catalog-provider", key],
      queryFn: () => fetchCatalogProviderDetail(key),
      staleTime: 5 * 60 * 1000,
    })),
  });
  const catalogDetailMap: Record<string, CatalogProviderDetail> = {};
  catalogKeys.forEach((key, i) => {
    if (catalogDetailResults[i]?.data) catalogDetailMap[key] = catalogDetailResults[i].data!;
  });

  // Gateway providers + model selection
  const [enabledProviders, setEnabledProviders] = useState<number[]>([]);
  const [providerModels, setProviderModels] = useState<Record<number, string[]>>({});
  const [defaultModel, setDefaultModel] = useState<string>("");

  // Brave key
  const [braveKey, setBraveKey] = useState("");

  // Per-instance env var overrides (plaintext, encrypted server-side on save)
  const [envVars, setEnvVars] = useState<Record<string, string>>({});

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!displayName.trim()) return;

    // Build provider-prefixed extra models.
    // Skip providers with stored models (custom providers) — their models are
    // pushed to the container directly from the provider definition.
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
    if (defaultModel) {
      payload.default_model = defaultModel;
    }
    if (Object.keys(envVars).length > 0) {
      payload.env_vars_set = envVars;
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

      {/* Enabled Models */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-1">Enabled Models</h3>
        <p className="text-xs text-gray-500 mb-4">
          Pick among available model(s) for the agent.
        </p>

        {allProviders.length === 0 ? (
          <p className="text-sm text-gray-400 italic">
            No providers configured. Add providers in Settings → Model API Keys first.
          </p>
        ) : (
          <ProviderModelSelector
            providers={allProviders}
            catalogDetailMap={catalogDetailMap}
            enabledProviders={enabledProviders}
            providerModels={providerModels}
            defaultModel={defaultModel}
            onUpdate={(newEnabled, newModels, newDefault) => {
              setEnabledProviders(newEnabled);
              setProviderModels(newModels);
              setDefaultModel(newDefault);
            }}
          />
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

      {/* Environment Variables */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-1">Environment Variables</h3>
        <p className="text-xs text-gray-500 mb-4">
          Per-instance env vars override globals with the same name. Values are
          encrypted at rest. Reserved names (<span className="font-mono">OPENCLAW_GATEWAY_TOKEN</span>,
          <span className="font-mono"> CLAWORC_INSTANCE_ID</span>,
          <span className="font-mono"> OPENCLAW_INITIAL_MODELS</span>,
          <span className="font-mono"> OPENCLAW_INITIAL_PROVIDERS</span>) are not allowed.
        </p>
        <KeyValueListEditor
          values={{}}
          pendingSet={envVars}
          onSet={(name, value) => setEnvVars((m) => ({ ...m, [name]: value }))}
          onUnset={(name) =>
            setEnvVars((m) => {
              const { [name]: _omit, ...rest } = m;
              return rest;
            })
          }
          emptyMessage="No instance-specific env vars. Globals from Settings apply."
        />
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
