import { useState, useEffect } from "react";
import { AlertTriangle, Eye, EyeOff, Key, Pencil, Plus, RefreshCw } from "lucide-react";
import ProviderIcon from "@/components/ProviderIcon";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSettings, useUpdateSettings } from "@/hooks/useSettings";
import { useProviders, useCreateProvider, useUpdateProvider, useDeleteProvider, useCatalogProviders, useCatalogProviderDetail } from "@/hooks/useProviders";
import { fetchSSHFingerprint, rotateSSHKey } from "@/api/ssh";
import { fetchCatalogProviderDetail, syncAllProviders } from "@/api/llm";
import { successToast, errorToast } from "@/utils/toast";
import type { LLMProvider, ProviderModel } from "@/types/instance";
import type { CatalogProviderDetail } from "@/api/llm";
import type { SettingsUpdatePayload } from "@/types/settings";

const slugify = (s: string) =>
  s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");

const deriveUniqueKey = (base: string, existing: string[]): string => {
  if (!existing.includes(base)) return base;
  let i = 2;
  while (existing.includes(`${base}-${i}`)) i++;
  return `${base}-${i}`;
};

export default function SettingsPage() {
  const queryClient = useQueryClient();
  const { data: settings, isLoading } = useSettings();
  const updateMutation = useUpdateSettings();
  const { data: providers = [] } = useProviders();
  const createProviderMutation = useCreateProvider();
  const updateProviderMutation = useUpdateProvider();
  const deleteProviderMutation = useDeleteProvider();
  const { data: catalogProviders = [], isLoading: catalogLoading } = useCatalogProviders();

  // Provider modal state
  const [modalOpen, setModalOpen] = useState(false);
  const [modalMode, setModalMode] = useState<"create" | "edit">("create");
  const [modalProvider, setModalProvider] = useState<LLMProvider | null>(null);
  // mCatalogKey: catalog provider key, "__custom__", or "" (unselected — create only)
  const [mCatalogKey, setMCatalogKey] = useState("");
  // mProvider: the catalog provider key stored in DB (e.g. "anthropic"), empty for custom
  const [mProvider, setMProvider] = useState("");
  const [mName, setMName] = useState("");
  const [mBaseURL, setMBaseURL] = useState("");
  const [mApiKey, setMApiKey] = useState("");
  const [mShowApiKey, setMShowApiKey] = useState(false);
  const [mApiType, setMApiType] = useState("openai-completions");
  const [mModels, setMModels] = useState<ProviderModel[]>([]);
  const [mModelDraft, setMModelDraft] = useState({
    id: "",
    name: "",
    reasoning: false,
    contextWindow: "",
    maxTokens: "",
    costInput: "",
    costOutput: "",
  });
  const [mShowOptionalFields, setMShowOptionalFields] = useState(false);

  const { data: catalogDetail } = useCatalogProviderDetail(
    modalOpen && modalMode === "create" && mCatalogKey && mCatalogKey !== "__custom__" ? mCatalogKey : null
  );

  // Sync all catalog providers
  const syncMutation = useMutation({
    mutationFn: syncAllProviders,
    onSuccess: () => {
      successToast("Models synced");
      queryClient.invalidateQueries({ queryKey: ["llm-providers"] });
    },
    onError: (err) => errorToast("Sync failed", err),
  });
  const hasCatalogProviders = providers.some((p) => p.provider !== "");

  // Fetch catalog detail for each catalog provider (for model display in rows)
  const catalogKeys = [...new Set(providers.filter((p) => p.provider).map((p) => p.provider))];
  const catalogDetailResults = useQueries({
    queries: catalogKeys.map((key) => ({
      queryKey: ["catalog-provider", key],
      queryFn: () => fetchCatalogProviderDetail(key),
      staleTime: 5 * 60 * 1000,
    })),
  });
  const catalogDetailMap: Record<string, CatalogProviderDetail> = {};
  catalogKeys.forEach((key, i) => {
    const result = catalogDetailResults[i];
    if (result?.data) catalogDetailMap[key] = result.data;
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

  // When catalog detail loads for the selected provider, fill in the base_url
  useEffect(() => {
    if (!catalogDetail || mCatalogKey === "__custom__" || !mCatalogKey) return;
    const baseUrl = catalogDetail.models.find((m) => m.base_url)?.base_url;
    if (baseUrl) setMBaseURL(baseUrl);
  }, [catalogDetail, mCatalogKey]);

  if (isLoading || !settings) {
    return <div className="text-center py-12 text-gray-500">Loading...</div>;
  }

  const settingsKeyName = (key: string) =>
    key.toUpperCase().replace(/-/g, "_") + "_API_KEY";

  // The effective provider key: always derived from name in create mode
  const effectiveKey =
    modalMode === "edit"
      ? modalProvider!.key
      : deriveUniqueKey(slugify(mName), providers.map((p) => p.key));

  // True when the provider is custom (not in catalog) — controls Base URL visibility
  const isCustomProvider =
    mCatalogKey === "__custom__" ||
    (modalMode === "edit" && !modalProvider?.provider);

  const handleCatalogKeyChange = (val: string) => {
    setMCatalogKey(val);
    if (val === "__custom__") {
      setMProvider("");
      setMName("");
      setMBaseURL("");
    } else if (val) {
      const cat = catalogProviders.find((c) => c.key === val);
      if (cat) {
        setMProvider(cat.key);
        setMName(cat.label);
        setMBaseURL("");
      }
    }
  };

  const openCreateModal = () => {
    setModalMode("create");
    setModalProvider(null);
    setMCatalogKey("");
    setMProvider("");
    setMName("");
    setMBaseURL("");
    setMApiKey("");
    setMShowApiKey(false);
    setMApiType("openai-completions");
    setMModels([]);
    setMModelDraft({ id: "", name: "", reasoning: false, contextWindow: "", maxTokens: "", costInput: "", costOutput: "" });
    setMShowOptionalFields(false);
    setModalOpen(true);
  };

  const openEditModal = (p: LLMProvider) => {
    setModalMode("edit");
    setModalProvider(p);
    setMCatalogKey("");
    setMName(p.name);
    setMBaseURL(p.base_url);
    setMApiKey("");
    setMShowApiKey(false);
    setMApiType(p.api_type || "openai-completions");
    setMModels(p.models || []);
    setMModelDraft({ id: "", name: "", reasoning: false, contextWindow: "", maxTokens: "", costInput: "", costOutput: "" });
    setMShowOptionalFields(false);
    setModalOpen(true);
  };

  const handleModalSave = async () => {
    const key = effectiveKey;
    try {
      if (modalMode === "create") {
        const catalogEntry = catalogProviders.find((c) => c.key === mCatalogKey);
        const apiType = isCustomProvider ? mApiType : (catalogEntry?.api_format ?? "openai-completions");
        const models = isCustomProvider ? mModels : [];
        await createProviderMutation.mutateAsync({ key, provider: mProvider, name: mName, base_url: mBaseURL, api_type: apiType, models });
      } else {
        const payload: { name: string; base_url: string; api_type?: string; models?: ProviderModel[] } = { name: mName, base_url: mBaseURL };
        if (isCustomProvider) {
          payload.api_type = mApiType;
          payload.models = mModels;
        }
        await updateProviderMutation.mutateAsync({ id: modalProvider!.id, payload });
      }
      if (mApiKey.trim()) {
        updateMutation.mutate({ api_keys: { [settingsKeyName(key)]: mApiKey.trim() } });
      }
      setModalOpen(false);
    } catch {
      // errors handled by mutation hooks
    }
  };

  const handleModalDelete = () => {
    if (!modalProvider) return;
    const keyToDelete = settingsKeyName(modalProvider.key);
    deleteProviderMutation.mutate(modalProvider.id, {
      onSuccess: () => {
        updateMutation.mutate({ delete_api_keys: [keyToDelete] });
        setModalOpen(false);
      },
    });
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

  const resourceFields: { key: string; label: string }[] = [
    { key: "default_cpu_request", label: "Default CPU Request" },
    { key: "default_cpu_limit", label: "Default CPU Limit" },
    { key: "default_memory_request", label: "Default Memory Request" },
    { key: "default_memory_limit", label: "Default Memory Limit" },
    { key: "default_storage_homebrew", label: "Default Homebrew Storage" },
    { key: "default_storage_home", label: "Default Home Storage" },
  ];

  const addModelFromDraft = () => {
    if (!mModelDraft.id.trim() || !mModelDraft.name.trim()) return;
    const model: ProviderModel = { id: mModelDraft.id.trim(), name: mModelDraft.name.trim() };
    if (mModelDraft.reasoning) model.reasoning = true;
    if (mModelDraft.contextWindow) model.contextWindow = parseInt(mModelDraft.contextWindow, 10);
    if (mModelDraft.maxTokens) model.maxTokens = parseInt(mModelDraft.maxTokens, 10);
    const hasInputCost = !!mModelDraft.costInput;
    const hasOutputCost = !!mModelDraft.costOutput;
    if (hasInputCost || hasOutputCost) {
      model.cost = {
        input: hasInputCost ? parseFloat(mModelDraft.costInput) : 0,
        output: hasOutputCost ? parseFloat(mModelDraft.costOutput) : 0,
        cacheRead: 0,
        cacheWrite: 0,
      };
    }
    setMModels((prev) => [...prev, model]);
    setMModelDraft({ id: "", name: "", reasoning: false, contextWindow: "", maxTokens: "", costInput: "", costOutput: "" });
    setMShowOptionalFields(false);
  };

  const hasChanges =
    pendingBraveKey !== null ||
    Object.keys(resources).length > 0;

  // Determine if "Save" is enabled in modal
  const showForm = modalMode === "edit" || (mCatalogKey !== "");
  const canSave =
    showForm &&
    !!effectiveKey &&
    !!mName &&
    !!mBaseURL &&
    (!isCustomProvider || mModels.length > 0) &&
    !createProviderMutation.isPending &&
    !updateProviderMutation.isPending;

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
                const skn = settingsKeyName(p.key);
                const apiKeyValue = settings.api_keys?.[skn];
                const apiKeyDisplay = apiKeyValue ? `****${apiKeyValue.slice(-4)}` : "not set";
                const catalogModels = p.provider ? catalogDetailMap[p.provider]?.models : undefined;
                const displayModels = catalogModels
                  ? catalogModels.map((m) => m.model_id)
                  : (p.models || []).map((m) => m.id);
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

      {/* Provider Modal */}
      {modalOpen && (
        <div className="fixed inset-0 bg-black/40 z-50 flex items-center justify-center">
          <div className={`bg-white rounded-lg shadow-xl p-6 w-full mx-4 ${isCustomProvider ? "max-w-xl" : "max-w-md"}`}>
            <h2 className="text-base font-semibold text-gray-900 mb-4 flex items-center gap-2">
              {modalMode === "edit" && modalProvider!.provider && (
                <ProviderIcon provider={modalProvider!.provider} size={22} />
              )}
              {modalMode === "create" && mCatalogKey && mCatalogKey !== "__custom__" && (
                <ProviderIcon provider={mCatalogKey} size={22} />
              )}
              {modalMode === "create" ? "Add Provider" : "Edit Provider"}
            </h2>

            <div className="space-y-4">
              {/* Provider picker — create mode only */}
              {modalMode === "create" && (
                <div>
                  <label className="block text-xs text-gray-500 mb-1">Provider</label>
                  <select
                    value={mCatalogKey}
                    onChange={(e) => handleCatalogKeyChange(e.target.value)}
                    disabled={catalogLoading}
                    className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white disabled:opacity-50"
                  >
                    <option value="" disabled hidden>
                      {catalogLoading ? "Loading providers..." : ""}
                    </option>
                    {catalogProviders.map((cat) => {
                      const count = providers.filter((p) => p.key === cat.key || p.key.startsWith(`${cat.key}-`)).length;
                      return (
                        <option key={cat.key} value={cat.key}>
                          {cat.label}{count > 0 ? ` (${count} added)` : ""}
                        </option>
                      );
                    })}
                    <option value="__custom__">Custom (self-hosted / unlisted)</option>
                  </select>
                </div>
              )}

              {/* Name — always shown when form is visible */}
              {showForm && (
                <div>
                  <label className="block text-xs text-gray-500 mb-1">Name</label>
                  <input
                    type="text"
                    value={mName}
                    onChange={(e) => setMName(e.target.value)}
                    placeholder="e.g., Anthropic"
                    className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                  {effectiveKey && (
                    <p className="text-xs text-gray-400 mt-1">
                      Key: <span className="font-mono">{effectiveKey}</span>
                    </p>
                  )}
                </div>
              )}

              {isCustomProvider && (
                <>
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">Base URL</label>
                    <input
                      type="text"
                      value={mBaseURL}
                      onChange={(e) => setMBaseURL(e.target.value)}
                      placeholder="https://api.example.com/v1"
                      className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                    />
                  </div>

                  <div>
                    <label className="block text-xs text-gray-500 mb-1">API Type</label>
                    <select
                      value={mApiType}
                      onChange={(e) => setMApiType(e.target.value)}
                      className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white"
                    >
                      <option value="openai-completions">openai-completions</option>
                      <option value="anthropic-messages">anthropic-messages</option>
                      <option value="openai-responses">openai-responses</option>
                      <option value="ollama">ollama</option>
                      <option value="bedrock-converse-stream">bedrock-converse-stream</option>
                    </select>
                  </div>

                  <div>
                    <label className="block text-xs text-gray-500 mb-1">
                      Models <span className="text-red-500">*</span>
                    </label>
                    {mModels.length > 0 && (
                      <div className="mb-2 space-y-1">
                        {mModels.map((m, i) => (
                          <div key={i} className="flex items-center justify-between px-2 py-1.5 bg-gray-50 border border-gray-200 rounded text-xs">
                            <span className="font-mono text-gray-700">{m.id}</span>
                            <span className="text-gray-500 mx-2 truncate">{m.name}</span>
                            <button
                              type="button"
                              onClick={() => setMModels((prev) => prev.filter((_, idx) => idx !== i))}
                              className="text-red-400 hover:text-red-600 shrink-0"
                            >
                              ×
                            </button>
                          </div>
                        ))}
                      </div>
                    )}
                    <div className="border border-gray-200 rounded-md p-3 space-y-2 bg-gray-50">
                      <div className="grid grid-cols-2 gap-2">
                        <div>
                          <label className="block text-xs text-gray-400 mb-0.5">Model ID</label>
                          <input
                            type="text"
                            value={mModelDraft.id}
                            onChange={(e) => setMModelDraft((d) => ({ ...d, id: e.target.value }))}
                            placeholder="claude-3-5-sonnet-20241022"
                            className="w-full px-2 py-1 border border-gray-300 rounded text-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
                          />
                        </div>
                        <div>
                          <label className="block text-xs text-gray-400 mb-0.5">Model Name</label>
                          <input
                            type="text"
                            value={mModelDraft.name}
                            onChange={(e) => setMModelDraft((d) => ({ ...d, name: e.target.value }))}
                            placeholder="Claude 3.5 Sonnet"
                            className="w-full px-2 py-1 border border-gray-300 rounded text-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
                          />
                        </div>
                      </div>
                      <button
                        type="button"
                        onClick={() => setMShowOptionalFields((v) => !v)}
                        className="text-xs text-gray-400 hover:text-gray-600"
                      >
                        {mShowOptionalFields ? "▾ Hide optional fields" : "▸ Optional fields (reasoning, context window, cost...)"}
                      </button>
                      {mShowOptionalFields && (
                        <div className="space-y-2 pt-1">
                          <label className="flex items-center gap-2 text-xs text-gray-600 cursor-pointer">
                            <input
                              type="checkbox"
                              checked={mModelDraft.reasoning}
                              onChange={(e) => setMModelDraft((d) => ({ ...d, reasoning: e.target.checked }))}
                            />
                            Reasoning model
                          </label>
                          <div className="grid grid-cols-2 gap-2">
                            <div>
                              <label className="block text-xs text-gray-400 mb-0.5">Context Window</label>
                              <input
                                type="number"
                                value={mModelDraft.contextWindow}
                                onChange={(e) => setMModelDraft((d) => ({ ...d, contextWindow: e.target.value }))}
                                placeholder="200000"
                                className="w-full px-2 py-1 border border-gray-300 rounded text-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
                              />
                            </div>
                            <div>
                              <label className="block text-xs text-gray-400 mb-0.5">Max Tokens</label>
                              <input
                                type="number"
                                value={mModelDraft.maxTokens}
                                onChange={(e) => setMModelDraft((d) => ({ ...d, maxTokens: e.target.value }))}
                                placeholder="8096"
                                className="w-full px-2 py-1 border border-gray-300 rounded text-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
                              />
                            </div>
                            <div>
                              <label className="block text-xs text-gray-400 mb-0.5">Input cost ($/M tokens)</label>
                              <input
                                type="number"
                                value={mModelDraft.costInput}
                                onChange={(e) => setMModelDraft((d) => ({ ...d, costInput: e.target.value }))}
                                placeholder="3.0"
                                step="0.01"
                                className="w-full px-2 py-1 border border-gray-300 rounded text-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
                              />
                            </div>
                            <div>
                              <label className="block text-xs text-gray-400 mb-0.5">Output cost ($/M tokens)</label>
                              <input
                                type="number"
                                value={mModelDraft.costOutput}
                                onChange={(e) => setMModelDraft((d) => ({ ...d, costOutput: e.target.value }))}
                                placeholder="15.0"
                                step="0.01"
                                className="w-full px-2 py-1 border border-gray-300 rounded text-xs focus:outline-none focus:ring-1 focus:ring-blue-500"
                              />
                            </div>
                          </div>
                        </div>
                      )}
                      <button
                        type="button"
                        onClick={addModelFromDraft}
                        disabled={!mModelDraft.id.trim() || !mModelDraft.name.trim()}
                        className="w-full py-1 text-xs font-medium text-blue-600 border border-blue-200 rounded hover:bg-blue-50 disabled:opacity-40 disabled:cursor-not-allowed"
                      >
                        + Add Model
                      </button>
                    </div>
                  </div>
                </>
              )}

              {/* API Key */}
              {showForm && (
                <div>
                  <label className="block text-xs text-gray-500 mb-1">
                    API Key{" "}
                    {modalMode === "edit" && (
                      <span className="text-gray-400">(leave blank to keep current)</span>
                    )}
                  </label>
                  <div className="relative">
                    <input
                      type={mShowApiKey ? "text" : "password"}
                      value={mApiKey}
                      onChange={(e) => setMApiKey(e.target.value)}
                      placeholder={modalMode === "edit" ? "Enter new key to update" : "Enter API key"}
                      className="w-full px-3 py-1.5 pr-10 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                    />
                    <button
                      type="button"
                      onClick={() => setMShowApiKey(!mShowApiKey)}
                      className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
                    >
                      {mShowApiKey ? <EyeOff size={14} /> : <Eye size={14} />}
                    </button>
                  </div>
                </div>
              )}
            </div>

            <div className="flex items-center justify-between mt-6">
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => setModalOpen(false)}
                  className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
                >
                  Cancel
                </button>
                {modalMode === "edit" && (
                  <button
                    type="button"
                    onClick={handleModalDelete}
                    disabled={deleteProviderMutation.isPending}
                    className="px-3 py-1.5 text-xs font-medium text-red-600 border border-red-200 rounded-md hover:bg-red-50 disabled:opacity-50"
                  >
                    {deleteProviderMutation.isPending ? "Deleting..." : "Delete"}
                  </button>
                )}
              </div>
              <button
                type="button"
                onClick={handleModalSave}
                disabled={!canSave}
                className="px-4 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
              >
                {createProviderMutation.isPending || updateProviderMutation.isPending
                  ? "Saving..."
                  : "Save"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
