import { useEffect, useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useQueries } from "@tanstack/react-query";
import { useSettings } from "@common/hooks/useSettings";
import { useProviders } from "@common/hooks/useProviders";
import { useAuth } from "@common/contexts/AuthContext";
import { useHealth } from "@common/hooks/useHealth";
import { fetchCatalogProviderDetail } from "@common/api/llm";
import type { CatalogProviderDetail } from "@common/api/llm";
import ProviderModelSelector from "@common/components/ProviderModelSelector";
import EnvVarsEditor from "@common/components/EnvVarsEditor";
import SimpleKVEditor from "@common/components/SimpleKVEditor";
import TolerationsEditor from "@common/components/TolerationsEditor";
import AffinityEditor from "@common/components/AffinityEditor";
import PortsEditor from "@common/components/PortsEditor";
import StickyActionBar from "@common/components/StickyActionBar";
import ConfirmDialog from "@common/components/ConfirmDialog";
import SlackChannelsEditor, { SlackDMPolicySelect } from "@common/components/SlackChannelsEditor";
import type { InstanceCreatePayload, PortSpec, Toleration } from "@common/types/instance";
import type { SlackChannel } from "@common/types/slack";
import type { UserTeamMembership } from "@common/types/auth";

interface AgentFormProps {
  onSubmit: (payload: InstanceCreatePayload) => void;
  onCancel: () => void;
  loading?: boolean;
  teams: UserTeamMembership[];
  teamId: number | null;
  onTeamIdChange: (id: number) => void;
}

export default function AgentForm({
  onSubmit,
  onCancel,
  loading,
  teams,
  teamId,
  onTeamIdChange,
}: AgentFormProps) {
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

  const [browserEnabled, setBrowserEnabled] = useState(true);
  const [browserImage, setBrowserImage] = useState("");
  const [vncResolution, setVncResolution] = useState("");
  const [userAgent, setUserAgent] = useState("");

  // Pod placement overrides (admin + Kubernetes only). Left undefined until
  // touched so the create payload omits them entirely and the backend falls
  // back to the configured global defaults (see resolvePlacementDefaults).
  const [podAnnotations, setPodAnnotations] = useState<Record<string, string>>({});
  const [nodeSelector, setNodeSelector] = useState<Record<string, string>>({});
  const [tolerations, setTolerations] = useState<Toleration[]>([]);
  const [affinity, setAffinity] = useState("");
  const [serviceAccountAnnotations, setServiceAccountAnnotations] = useState<Record<string, string>>({});
  const [ports, setPorts] = useState<PortSpec[]>([]);
  const [showAdvanced, setShowAdvanced] = useState(false);

  const { data: settings } = useSettings();
  const { data: allProviders = [] } = useProviders();
  const { isAdmin } = useAuth();
  const { data: health } = useHealth();
  const isKubernetes = health?.orchestrator_backend === "kubernetes";

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

  // Seed placement fields from global defaults once settings have loaded, same
  // "seed once, admin can override before submitting" contract as resources
  // above. Untouched fields round-trip the same default value the backend
  // would have applied anyway; an edit becomes an explicit per-instance
  // override. Gating the editors' render on `settings` below (not just
  // isAdmin/isKubernetes) avoids them mounting with stale {}/[]/"" before this
  // effect seeds the real defaults - SimpleKVEditor/TolerationsEditor/
  // AffinityEditor/PortsEditor only read their `values`/`value` prop once, at
  // mount.
  const [placementSeeded, setPlacementSeeded] = useState(false);
  useEffect(() => {
    if (placementSeeded || !settings) return;
    setPodAnnotations(settings.default_pod_annotations ?? {});
    setNodeSelector(settings.default_node_selector ?? {});
    setTolerations(settings.default_tolerations ?? []);
    setAffinity(settings.default_affinity ?? "");
    setServiceAccountAnnotations(settings.default_service_account_annotations ?? {});
    setPorts(settings.default_ports ?? []);
    setPlacementSeeded(true);
  }, [settings, placementSeeded]);

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

  // Slack connection (optional): configured at create time so the agent
  // connects to Slack on first boot. Tokens ride the encrypted env-var path
  // server-side (SLACK_BOT_TOKEN / SLACK_APP_TOKEN).
  const [slackEnabled, setSlackEnabled] = useState(false);
  const [slackBotToken, setSlackBotToken] = useState("");
  const [slackAppToken, setSlackAppToken] = useState("");
  const [slackChannels, setSlackChannels] = useState<SlackChannel[]>([]);
  const [slackDmPolicy, setSlackDmPolicy] = useState("");

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
    if (!browserEnabled) {
      payload.browser_enabled = false;
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
    if (slackEnabled) {
      payload.slack = {
        enabled: true,
        channels: slackChannels.filter((c) => c.id.trim() !== ""),
        dm_policy: slackDmPolicy || undefined,
        bot_token: slackBotToken.trim() || undefined,
        app_token: slackAppToken.trim() || undefined,
      };
    }

    // Placement overrides: only meaningful (and only accepted by the backend)
    // for admins on the Kubernetes backend. Omitting them entirely for
    // everyone else lets the backend's own resolvePlacementDefaults apply,
    // rather than risking a 403 on the whole create request.
    if (isAdmin && isKubernetes && placementSeeded) {
      payload.pod_annotations = podAnnotations;
      payload.node_selector = nodeSelector;
      payload.tolerations = tolerations;
      payload.affinity = affinity;
      payload.service_account_annotations = serviceAccountAnnotations;
      payload.ports = ports;
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
      {/* Agent */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-4">Agent</h3>
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
        description="Applied to both the agent container and the browser pod. Per-agent values override globals with the same name. Values are encrypted at rest."
        onChange={setEnvVars}
      />

      {/* Slack */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-1">Slack</h3>
        <p className="text-xs text-gray-500 mb-4">
          Optionally connect this agent to a Slack workspace via a Socket Mode app, so it's
          reachable in Slack from first boot. Tokens are stored encrypted. Can be changed later
          in the agent's settings.
        </p>
        <div className="space-y-4">
          <label className="flex items-start gap-2 cursor-pointer select-none">
            <input
              type="checkbox"
              checked={slackEnabled}
              onChange={(e) => setSlackEnabled(e.target.checked)}
              className="mt-0.5 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
            />
            <span>
              <span className="block text-sm text-gray-900">Enable Slack</span>
              <span className="block text-xs text-gray-500">
                The agent joins Slack at startup and responds in the allowed channels below.
              </span>
            </span>
          </label>
          {slackEnabled && (
            <>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-xs text-gray-500 mb-1">Bot token</label>
                  <input
                    type="password"
                    autoComplete="off"
                    value={slackBotToken}
                    onChange={(e) => setSlackBotToken(e.target.value)}
                    placeholder="xoxb-…"
                    className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                </div>
                <div>
                  <label className="block text-xs text-gray-500 mb-1">App token</label>
                  <input
                    type="password"
                    autoComplete="off"
                    value={slackAppToken}
                    onChange={(e) => setSlackAppToken(e.target.value)}
                    placeholder="xapp-…"
                    className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                </div>
              </div>
              {(!slackBotToken.trim() || !slackAppToken.trim()) && (
                <p className="text-xs text-amber-700">
                  Both tokens are required for the agent to connect — unless they're provided as
                  SLACK_BOT_TOKEN / SLACK_APP_TOKEN env vars (global or per-agent).
                </p>
              )}
              <SlackChannelsEditor channels={slackChannels} onChange={setSlackChannels} />
              <SlackDMPolicySelect value={slackDmPolicy} onChange={setSlackDmPolicy} />
            </>
          )}
        </div>
      </div>

      {/* Browser */}
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-1">Browser</h3>
        <p className="text-xs text-gray-500 mb-4">
          Overrides for the on-demand browser launched for this instance.
        </p>
        <div className="space-y-4">
          <label className="flex items-start gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={browserEnabled}
              onChange={(e) => setBrowserEnabled(e.target.checked)}
              className="mt-0.5 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
            />
            <span>
              <span className="block text-sm text-gray-900">Enable browser</span>
              <span className="block text-xs text-gray-500">
                Uncheck for agents that never need a browser — no browser pod
                will be created, saving cluster resources. Can be changed later.
              </span>
            </span>
          </label>
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              Browser Image Override
            </label>
            <input
              type="text"
              value={browserImage}
              onChange={(e) => setBrowserImage(e.target.value)}
              placeholder={settings?.default_browser_image ?? "claworc/chromium-browser:latest"}
              disabled={!browserEnabled}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:bg-gray-50 disabled:text-gray-400"
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

      {/* Advanced (admin + K8s only): ports, annotations (pod + service
          account), node placement. Collapsed by default - these are rarely
          touched at creation time and the agent/global settings pages keep
          them uncollapsed, this is just the create form. */}
      {isAdmin && isKubernetes && settings && (
        <div>
          <button
            type="button"
            onClick={() => setShowAdvanced((v) => !v)}
            className="flex items-center gap-1.5 text-sm font-medium text-gray-700 hover:text-gray-900"
          >
            {showAdvanced ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
            Advanced
          </button>

          {showAdvanced && (
            <div className="mt-4 space-y-8">
              <PortsEditor
                values={ports}
                title="Ports"
                description="Additional TCP ports exposed by the pod and published via a ClusterIP Service of the same name. Leave empty for the common SSH-only case (no Service)."
                inline
                onChange={setPorts}
              />

              <div className="bg-white rounded-lg border border-gray-200 p-6 space-y-6">
                <h3 className="text-sm font-medium text-gray-900">Annotations</h3>

                <SimpleKVEditor
                  values={podAnnotations}
                  title="Pod Annotations"
                  description="Metadata annotations applied to the pod template. Useful for tools like Karpenter, Datadog, or custom controllers."
                  inline
                  onChange={setPodAnnotations}
                  keyPlaceholder="karpenter.sh/do-not-disrupt"
                  valuePlaceholder="true"
                />

                <SimpleKVEditor
                  values={serviceAccountAnnotations}
                  title="Service Account Annotations"
                  description="Annotations on the dedicated ServiceAccount claworc creates for this instance (e.g. for external secret-store auth methods keyed off SA identity). Leave empty to run under the namespace's default ServiceAccount."
                  inline
                  onChange={setServiceAccountAnnotations}
                  keyPlaceholder="vault.hashicorp.com/role"
                  valuePlaceholder="my-app"
                />
              </div>

              <div className="bg-white rounded-lg border border-gray-200 p-6 space-y-6">
                <h3 className="text-sm font-medium text-gray-900">Node Placement</h3>

                <SimpleKVEditor
                  values={nodeSelector}
                  title="Node Selector"
                  description="Schedule this pod only on nodes matching all these labels."
                  inline
                  onChange={setNodeSelector}
                  keyPlaceholder="kubernetes.io/hostname"
                  valuePlaceholder="worker-1"
                />

                <TolerationsEditor
                  values={tolerations}
                  title="Tolerations"
                  description="Tolerations for this pod. Appended after any global default tolerations."
                  inline
                  onChange={setTolerations}
                />

                <AffinityEditor
                  value={affinity}
                  title="Affinity (JSON)"
                  description="Raw K8s affinity spec — nodeAffinity, podAffinity, podAntiAffinity."
                  inline
                  onChange={setAffinity}
                />
              </div>
            </div>
          )}
        </div>
      )}

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
