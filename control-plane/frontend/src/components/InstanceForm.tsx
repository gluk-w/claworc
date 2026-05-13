import { useEffect, useState } from "react";
import { useQueries } from "@tanstack/react-query";
import { useSettings } from "@/hooks/useSettings";
import { useProviders } from "@/hooks/useProviders";
import { fetchCatalogProviderDetail } from "@/api/llm";
import type { CatalogProviderDetail } from "@/api/llm";
import ProviderModelSelector from "@/components/ProviderModelSelector";
import EnvVarsEditor from "@/components/EnvVarsEditor";
import StickyActionBar from "@/components/StickyActionBar";
import ConfirmDialog from "@/components/ConfirmDialog";
import type { InstanceCreatePayload } from "@/types/instance";
import type { UserTeamMembership } from "@/types/auth";

interface InstanceFormProps {
  onSubmit: (payload: InstanceCreatePayload) => void;
  onCancel: () => void;
  loading?: boolean;
  teams: UserTeamMembership[];
  teamId: number | null;
  onTeamIdChange: (id: number) => void;
}

export default function InstanceForm({
  onSubmit,
  onCancel,
  loading,
  teams,
  teamId,
  onTeamIdChange,
}: InstanceFormProps) {
  const [displayName, setDisplayName] = useState("");
  const [cpuRequest, setCpuRequest] = useState("");
  const [cpuLimit, setCpuLimit] = useState("");
  const [memoryRequest, setMemoryRequest] = useState("");
  const [memoryLimit, setMemoryLimit] = useState("");
  const [storageHomebrew, setStorageHomebrew] = useState("");
  const [storageHome, setStorageHome] = useState("");
  const [resourcesSeeded, setResourcesSeeded] = useState(false);

  const [containerImage, setContainerImage] = useState("");
  const [timezone, setTimezone] = useState("");

  const [browserImage, setBrowserImage] = useState("");
  const [vncResolution, setVncResolution] = useState("");
  const [userAgent, setUserAgent] = useState("");

  const { data: settings } = useSettings();
  const { data: allProviders = [] } = useProviders();

  // Seed resource fields from global defaults once settings have loaded.
  // The user can still override anything before submitting.
  useEffect(() => {
    if (resourcesSeeded || !settings) return;
    setCpuRequest(settings.default_cpu_request ?? "");
    setCpuLimit(settings.default_cpu_limit ?? "");
    setMemoryRequest(settings.default_memory_request ?? "");
    setMemoryLimit(settings.default_memory_limit ?? "");
    setStorageHomebrew(settings.default_storage_homebrew ?? "");
    setStorageHome(settings.default_storage_home ?? "");
    setResourcesSeeded(true);
  }, [settings, resourcesSeeded]);

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

  const [showNoModelsWarning, setShowNoModelsWarning] = useState(false);

  const buildPayload = (): InstanceCreatePayload | null => {
    if (!displayName.trim()) return null;

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
      team_id: teamId ?? undefined,
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

    if (browserImage) {
      payload.browser_image = browserImage;
    }

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

    return payload;
  };

  const hasModelsSelected = Object.values(providerModels).some((m) => m.length > 0);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!displayName.trim()) return;
    if (!hasModelsSelected) {
      setShowNoModelsWarning(true);
      return;
    }
    const payload = buildPayload();
    if (payload) onSubmit(payload);
  };

  const handleConfirmNoModels = () => {
    setShowNoModelsWarning(false);
    const payload = buildPayload();
    if (payload) onSubmit(payload);
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-8 pb-24">
      {/* Instance */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-4">Instance</h3>
        <div className="space-y-4">
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Team *
            </label>
            {teams.length <= 1 ? (
              <div className="w-full px-3 py-1.5 border border-gray-200 bg-gray-50 rounded-md text-sm text-gray-700">
                {teams[0]?.name ?? "—"}
              </div>
            ) : (
              <select
                value={teamId ?? ""}
                onChange={(e) => onTeamIdChange(Number(e.target.value))}
                required
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm bg-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                {teams.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
              </select>
            )}
          </div>
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

      {/* Container */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-4">Container</h3>
        <div className="space-y-4">
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Agent Image Override
            </label>
            <input
              type="text"
              value={containerImage}
              onChange={(e) => setContainerImage(e.target.value)}
              placeholder={settings?.default_agent_image ?? "claworc/openclaw:latest"}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
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
      <EnvVarsEditor
        inline
        values={{}}
        title="Environment Variables"
        description="Applied to both the instance container and the browser pod. Per-instance values override globals with the same name. Values are encrypted at rest."
        onChange={setEnvVars}
      />

      {/* Browser */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-1">Browser</h3>
        <p className="text-xs text-gray-500 mb-4">
          Overrides for the on-demand browser launched for this instance.
        </p>
        <div className="space-y-4">
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Browser Image Override
            </label>
            <input
              type="text"
              value={browserImage}
              onChange={(e) => setBrowserImage(e.target.value)}
              placeholder={settings?.default_browser_image ?? "claworc/chromium-browser:latest"}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Resolution Override
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

      <StickyActionBar visible={!!displayName.trim()}>
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
      </StickyActionBar>

      {showNoModelsWarning && (
        <ConfirmDialog
          title="No models selected"
          message="You haven't selected any models for this instance. The agent won't be able to run until models are configured. Continue anyway?"
          confirmLabel="Continue"
          onConfirm={handleConfirmNoModels}
          onCancel={() => setShowNoModelsWarning(false)}
        />
      )}
    </form>
  );
}
