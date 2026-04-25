import { createElement, useState, useEffect } from "react";
import { Eye, EyeOff } from "lucide-react";
import ProviderIcon from "@/components/ProviderIcon";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useCreateProvider, useUpdateProvider, useDeleteProvider, useCatalogProviders, useCatalogProviderDetail } from "@/hooks/useProviders";
import { testProviderKey } from "@/api/llm";
import { successToast, errorToast } from "@/utils/toast";
import toast from "react-hot-toast";
import AppToast from "@/components/AppToast";
import type { LLMProvider, ProviderModel } from "@/types/instance";

const CUSTOM_PROVIDER = "__custom__";

const slugify = (s: string) =>
  s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");

const deriveUniqueKey = (base: string, existing: string[]): string => {
  if (!existing.includes(base)) return base;
  let i = 2;
  while (existing.includes(`${base}-${i}`)) i++;
  return `${base}-${i}`;
};

interface ProviderModalProps {
  open: boolean;
  mode: "create" | "edit";
  provider?: LLMProvider;
  instanceId?: number;
  existingKeys: string[];
  onClose: () => void;
  onSaved: () => void;
  onDeleted?: () => void;
}

export default function ProviderModal({
  open,
  mode,
  provider,
  instanceId,
  existingKeys,
  onClose,
  onSaved,
  onDeleted,
}: ProviderModalProps) {
  const queryClient = useQueryClient();
  const createProviderMutation = useCreateProvider();
  const updateProviderMutation = useUpdateProvider();
  const deleteProviderMutation = useDeleteProvider();

  const [mCatalogSource, setMCatalogSource] = useState("builtin");
  const [mCatalogUrl, setMCatalogUrl] = useState("");
  const { data: catalogProviders = [], isLoading: catalogLoading } = useCatalogProviders(mCatalogSource, mCatalogUrl);
  const customUrlMissing = mCatalogSource === "custom" && !mCatalogUrl.trim();

  const catalogIconMap = Object.fromEntries(
    catalogProviders.map((c) => [c.name, c.icon_key]).filter(([, v]) => v)
  );

  const [mCatalogKey, setMCatalogKey] = useState("");
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
    open && mode === "create" && mCatalogKey && mCatalogKey !== CUSTOM_PROVIDER ? mCatalogKey : null,
    mCatalogSource,
    mCatalogUrl,
  );

  const testMutation = useMutation({
    mutationFn: testProviderKey,
    onSuccess: (result) => {
      if (result.ok) {
        successToast("API key is valid");
      } else {
        errorToast("API key test failed", result.error || "Unknown error");
      }
    },
    onError: (err) => errorToast("Test request failed", err),
  });

  // Reset provider selection when catalog source changes
  useEffect(() => {
    setMCatalogKey("");
    setMProvider("");
    setMName("");
    setMBaseURL("");
  }, [mCatalogSource, mCatalogUrl]);

  useEffect(() => {
    if (!open) return;
    createProviderMutation.reset();
    updateProviderMutation.reset();
    deleteProviderMutation.reset();
    setMCatalogSource("builtin");
    setMCatalogUrl("");
    if (mode === "create") {
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
    } else if (provider) {
      setMCatalogKey("");
      setMName(provider.name);
      setMBaseURL(provider.base_url);
      setMApiKey("");
      setMShowApiKey(false);
      setMApiType(provider.api_type || "openai-completions");
      setMModels(provider.models || []);
      setMModelDraft({ id: "", name: "", reasoning: false, contextWindow: "", maxTokens: "", costInput: "", costOutput: "" });
      setMShowOptionalFields(false);
    }
  }, [open, mode, provider]);

  useEffect(() => {
    if (!catalogDetail || mCatalogKey === CUSTOM_PROVIDER || !mCatalogKey) return;
    const baseUrl = catalogDetail.models.find((m) => m.base_url)?.base_url;
    if (baseUrl) setMBaseURL(baseUrl);
  }, [catalogDetail, mCatalogKey]);

  const effectiveKey =
    mode === "edit"
      ? provider!.key
      : deriveUniqueKey(slugify(mName), existingKeys);

  const isCustomProvider =
    mCatalogKey === CUSTOM_PROVIDER ||
    (mode === "edit" && !provider?.provider);

  const handleCatalogKeyChange = (val: string) => {
    setMCatalogKey(val);
    if (val === CUSTOM_PROVIDER) {
      setMProvider("");
      setMName("");
      setMBaseURL("");
    } else if (val) {
      const cat = catalogProviders.find((c) => c.name === val);
      if (cat) {
        setMProvider(cat.name);
        setMName(cat.label);
        setMBaseURL(cat.base_url);
      }
    }
  };

  const resolveApiType = (): string => {
    if (isCustomProvider) return mApiType;
    if (mode === "edit") return provider!.api_type || "openai-completions";
    const catalogEntry = catalogProviders.find((c) => c.name === mCatalogKey);
    return catalogEntry?.api_format ?? "openai-completions";
  };

  const refreshQueries = () => Promise.all([
    queryClient.invalidateQueries({ queryKey: ["llm-providers"] }),
    queryClient.invalidateQueries({ queryKey: ["settings"] }),
    ...(instanceId ? [
      queryClient.invalidateQueries({ queryKey: ["instance-providers", instanceId] }),
      queryClient.invalidateQueries({ queryKey: ["instance", instanceId] }),
    ] : []),
  ]);

  const showLoadingToast = (toastId: string, title: string) => {
    toast.custom(
      createElement(AppToast, { title, status: "loading", toastId }),
      { id: toastId, duration: Infinity },
    );
  };

  const showResultToast = (toastId: string, title: string, err?: unknown) => {
    if (!err) {
      toast.custom(
        createElement(AppToast, { title, status: "success", toastId }),
        { id: toastId, duration: 3000 },
      );
    } else {
      errorToast(title, err);
      toast.dismiss(toastId);
    }
  };

  const handleSave = async () => {
    const key = effectiveKey;
    const toastId = mode === "create" ? "provider-create" : `provider-update-${provider!.id}`;
    showLoadingToast(toastId, mode === "create" ? "Adding provider..." : "Updating provider...");
    try {
      if (mode === "create") {
        const apiType = resolveApiType();
        const models = isCustomProvider ? mModels : (() => {
          const cat = catalogProviders.find((c) => c.name === mCatalogKey);
          if (!cat) return [];
          return cat.models.map((m) => ({
            id: m.model_id,
            name: m.model_name,
            reasoning: m.reasoning,
            contextWindow: m.context_window ?? undefined,
            maxTokens: m.max_tokens ?? undefined,
            cost: (m.input_cost || m.output_cost || m.cached_read_cost || m.cached_write_cost)
              ? { input: m.input_cost, output: m.output_cost, cacheRead: m.cached_read_cost, cacheWrite: m.cached_write_cost }
              : undefined,
          }));
        })();
        await createProviderMutation.mutateAsync({
          key,
          provider: mProvider,
          name: mName,
          base_url: mBaseURL,
          api_type: apiType,
          models,
          api_key: mApiKey.trim() || undefined,
          instance_id: instanceId,
        });
      } else {
        const payload: { name: string; base_url: string; api_type?: string; models?: ProviderModel[]; api_key?: string } = {
          name: mName,
          base_url: mBaseURL,
        };
        if (isCustomProvider) {
          payload.api_type = mApiType;
          payload.models = mModels;
        }
        if (mApiKey.trim()) {
          payload.api_key = mApiKey.trim();
        }
        await updateProviderMutation.mutateAsync({ id: provider!.id, payload });
      }
      onClose();
      await refreshQueries();
      onSaved();
      showResultToast(toastId, mode === "create" ? "Provider created" : "Provider updated");
    } catch (err) {
      onClose();
      showResultToast(toastId, mode === "create" ? "Failed to create provider" : "Failed to update provider", err);
    }
  };

  const handleDelete = async () => {
    if (!provider) return;
    const toastId = `provider-delete-${provider.id}`;
    showLoadingToast(toastId, "Deleting provider...");
    try {
      await deleteProviderMutation.mutateAsync(provider.id);
      onClose();
      await refreshQueries();
      onDeleted?.();
      showResultToast(toastId, "Provider deleted");
    } catch (err) {
      onClose();
      showResultToast(toastId, "Failed to delete provider", err);
    }
  };

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

  const showForm = mode === "edit" || (mCatalogKey !== "");
  const canSave =
    showForm &&
    !!effectiveKey &&
    !!mName &&
    !!mBaseURL &&
    (!isCustomProvider || mModels.length > 0) &&
    (mode === "edit" || isCustomProvider || !!mApiKey.trim()) &&
    !createProviderMutation.isPending &&
    !updateProviderMutation.isPending;

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      onClose();
    } else if (e.key === "Enter" && canSave && !e.shiftKey) {
      handleSave();
    }
  };

  if (!open) return null;

  return (
    <div className="fixed inset-0 bg-black/40 z-50 flex items-center justify-center" onKeyDown={handleKeyDown}>
      <div className={`bg-white rounded-lg shadow-xl p-6 w-full mx-4 ${isCustomProvider ? "max-w-xl" : "max-w-md"}`}>
        <h2 className="text-base font-semibold text-gray-900 mb-4 flex items-center gap-2">
          {mode === "edit" && provider!.provider && (
            <ProviderIcon provider={catalogIconMap[provider!.provider] ?? provider!.provider} size={22} />
          )}
          {mode === "create" && mCatalogKey && mCatalogKey !== CUSTOM_PROVIDER && (
            <ProviderIcon provider={catalogIconMap[mCatalogKey] ?? mCatalogKey} size={22} />
          )}
          {mode === "create" ? "Add Provider" : "Edit Provider"}
          {instanceId && (
            <span className="text-xs font-normal text-amber-600 bg-amber-50 px-2 py-0.5 rounded-full">Instance</span>
          )}
        </h2>

        <div className="space-y-4">
          {/* Provider picker — create mode only */}
          {mode === "create" && (
            <>
              <div className="flex gap-2">
                <div className="flex-1">
                  <label className="block text-xs text-gray-500 mb-1">Catalog Source</label>
                  <select
                    value={mCatalogSource}
                    onChange={(e) => { setMCatalogSource(e.target.value); if (e.target.value !== "custom") setMCatalogUrl(""); }}
                    className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white"
                  >
                    <option value="builtin">Built-in</option>
                    <option value="claworc">Claworc (official)</option>
                    <option value="custom">Custom URL</option>
                  </select>
                </div>
                {mCatalogSource === "custom" && (
                  <div className="flex-1">
                    <label className="block text-xs text-gray-500 mb-1">Catalog URL</label>
                    <input
                      type="text"
                      value={mCatalogUrl}
                      onChange={(e) => setMCatalogUrl(e.target.value)}
                      placeholder="https://example.com/providers"
                      className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                    />
                  </div>
                )}
              </div>
              <div>
                <label className="block text-xs text-gray-500 mb-1">Provider</label>
              {customUrlMissing ? (
                <p className="text-sm text-gray-400 italic py-1.5">Enter a catalog URL above to load providers</p>
              ) : (
              <select
                value={mCatalogKey}
                onChange={(e) => handleCatalogKeyChange(e.target.value)}
                disabled={catalogLoading}
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white disabled:opacity-50"
              >
                <option value="" disabled hidden>
                  {catalogLoading ? "Loading providers..." : ""}
                </option>
                {catalogProviders.map((cat) => (
                  <option key={cat.name} value={cat.name}>
                    {cat.label}
                  </option>
                ))}
                <option value={CUSTOM_PROVIDER}>Custom (self-hosted / unlisted)</option>
              </select>
              )}
            </div>
            </>
          )}

          {/* Name */}
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
                          x
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
                    {mShowOptionalFields ? "Hide optional fields" : "Optional fields (reasoning, context window, cost...)"}
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
                {mode === "edit" && (
                  <span className="text-gray-400">(leave blank to keep current)</span>
                )}
              </label>
              <div className="relative">
                <input
                  type={mShowApiKey ? "text" : "password"}
                  value={mApiKey}
                  onChange={(e) => setMApiKey(e.target.value)}
                  placeholder={mode === "edit" ? "Enter new key to update" : "Enter API key"}
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
              onClick={onClose}
              className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
            {mode === "edit" && (
              <button
                type="button"
                onClick={handleDelete}
                disabled={deleteProviderMutation.isPending}
                className="px-3 py-1.5 text-xs font-medium text-red-600 border border-red-200 rounded-md hover:bg-red-50 disabled:opacity-50"
              >
                {deleteProviderMutation.isPending ? "Deleting..." : "Delete"}
              </button>
            )}
          </div>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={() => testMutation.mutate({ base_url: mBaseURL, api_key: mApiKey, api_type: resolveApiType() })}
              disabled={!mBaseURL || !mApiKey.trim() || testMutation.isPending}
              className="px-3 py-1.5 text-xs font-medium text-gray-700 border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {testMutation.isPending ? "Testing..." : "Test"}
            </button>
            <button
              type="button"
              onClick={handleSave}
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
    </div>
  );
}
