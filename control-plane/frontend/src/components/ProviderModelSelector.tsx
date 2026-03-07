import { useState, useMemo, useEffect } from "react";
import { ChevronDown, ChevronUp } from "lucide-react";
import ProviderIcon from "@/components/ProviderIcon";
import type { LLMProvider } from "@/types/instance";
import type { CatalogProviderDetail } from "@/api/llm";

interface Props {
  providers: LLMProvider[];
  catalogDetailMap: Record<string, CatalogProviderDetail>;
  enabledProviders: number[];
  providerModels: Record<number, string[]>;
  onUpdate: (enabledProviders: number[], providerModels: Record<number, string[]>) => void;
  defaultModel?: string;
  onDefaultModelChange?: (model: string) => void;
}

export default function ProviderModelSelector({
  providers,
  catalogDetailMap,
  enabledProviders,
  providerModels,
  onUpdate,
  defaultModel,
  onDefaultModelChange,
}: Props) {
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  // Flat list of all currently-selected models across all providers
  const allActiveModelOptions = useMemo(() => {
    const result: { value: string; label: string }[] = [];
    for (const p of providers) {
      const isEnabled = enabledProviders.includes(p.id);
      const isCustom = (p.models?.length ?? 0) > 0;
      if (isCustom) {
        if (isEnabled) {
          for (const m of p.models ?? []) {
            result.push({ value: `${p.key}/${m.id}`, label: `${p.name} / ${m.name}` });
          }
        }
      } else {
        const catalogDetail = p.provider ? catalogDetailMap[p.provider] : undefined;
        for (const mId of providerModels[p.id] ?? []) {
          const modelName = catalogDetail?.models.find((m) => m.model_id === mId)?.model_name ?? mId;
          result.push({ value: `${p.key}/${mId}`, label: `${p.name} / ${modelName}` });
        }
      }
    }
    return result;
  }, [providers, enabledProviders, providerModels, catalogDetailMap]);

  // Clear defaultModel if its model is no longer selected
  useEffect(() => {
    if (defaultModel && onDefaultModelChange && !allActiveModelOptions.some((m) => m.value === defaultModel)) {
      onDefaultModelChange("");
    }
  }, [allActiveModelOptions, defaultModel, onDefaultModelChange]);

  const toggleExpand = (id: number) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  };

  const toggleCustomProvider = (id: number) => {
    const isEnabled = enabledProviders.includes(id);
    const newEnabled = isEnabled ? enabledProviders.filter((x) => x !== id) : [...enabledProviders, id];
    const newModels = { ...providerModels };
    if (isEnabled) delete newModels[id];
    onUpdate(newEnabled, newModels);
  };

  const toggleModel = (providerId: number, modelId: string) => {
    const current = providerModels[providerId] ?? [];
    const next = current.includes(modelId) ? current.filter((x) => x !== modelId) : [...current, modelId];
    const newModels = { ...providerModels, [providerId]: next };
    let newEnabled = [...enabledProviders];
    if (next.length > 0 && !newEnabled.includes(providerId)) {
      newEnabled.push(providerId);
    } else if (next.length === 0) {
      newEnabled = newEnabled.filter((x) => x !== providerId);
    }
    onUpdate(newEnabled, newModels);
  };

  const selectAll = (providerId: number, allModelIds: string[]) => {
    const current = providerModels[providerId] ?? [];
    const next = current.length === allModelIds.length ? [] : allModelIds;
    const newModels = { ...providerModels, [providerId]: next };
    let newEnabled = [...enabledProviders];
    if (next.length > 0 && !newEnabled.includes(providerId)) {
      newEnabled.push(providerId);
    } else if (next.length === 0) {
      newEnabled = newEnabled.filter((x) => x !== providerId);
    }
    onUpdate(newEnabled, newModels);
  };

  return (
    <div className="space-y-2">
      {providers.map((p) => {
        const isExpanded = expanded.has(p.id);
        const isCustom = (p.models?.length ?? 0) > 0;
        const isEnabled = enabledProviders.includes(p.id);
        const selectedModels = providerModels[p.id] ?? [];
        const catalogDetail = p.provider ? catalogDetailMap[p.provider] : undefined;
        const modelCount = isCustom ? (p.models?.length ?? 0) : (catalogDetail?.models.length ?? null);
        const iconKey = catalogDetail?.icon_key ?? undefined;

        return (
          <div key={p.id} className="bg-white rounded-lg border border-gray-200 overflow-hidden">
            {/* Provider header */}
            <button
              type="button"
              onClick={() => toggleExpand(p.id)}
              className="w-full flex items-center gap-3 px-4 py-3 hover:bg-gray-50 transition-colors text-left"
            >
              {/* Icon box */}
              <div className="w-10 h-10 rounded-lg bg-gray-100 flex items-center justify-center shrink-0">
                {iconKey ? (
                  <ProviderIcon provider={iconKey} size={22} />
                ) : (
                  <span className="text-sm font-semibold text-gray-500">{p.name[0].toUpperCase()}</span>
                )}
              </div>

              {/* Name + subtitle */}
              <div className="flex-1 min-w-0">
                <div className="text-sm font-semibold text-gray-900">{p.name}</div>
                <div className="text-xs text-gray-500">
                  {isCustom
                    ? `${modelCount} model${modelCount !== 1 ? "s" : ""}`
                    : modelCount !== null
                    ? `${modelCount} models available`
                    : p.provider
                    ? "Loading..."
                    : "Dynamic models"}
                </div>
              </div>

              {/* Custom provider enable toggle */}
              {isCustom && (
                <input
                  type="checkbox"
                  checked={isEnabled}
                  onChange={() => toggleCustomProvider(p.id)}
                  onClick={(e) => e.stopPropagation()}
                  className="rounded border-gray-300 mr-1"
                />
              )}

              {/* Chevron */}
              {isExpanded ? (
                <ChevronUp size={16} className="text-gray-400 shrink-0" />
              ) : (
                <ChevronDown size={16} className="text-gray-400 shrink-0" />
              )}
            </button>

            {/* Expanded body */}
            {isExpanded && (
              <div className="border-t border-gray-100 bg-gray-50">
                {isCustom ? (
                  <div className="px-4 py-3 flex flex-wrap gap-1">
                    {(p.models ?? []).map((m) => (
                      <span key={m.id} className="px-2 py-0.5 bg-white border border-gray-200 text-gray-600 text-xs rounded font-mono">
                        {m.id}
                      </span>
                    ))}
                  </div>
                ) : catalogDetail ? (
                  <>
                    {/* Models sub-header */}
                    <div className="flex items-center justify-between px-4 py-2 border-b border-gray-100">
                      <span className="text-xs font-semibold text-gray-400 uppercase tracking-wide">Models</span>
                      <button
                        type="button"
                        onClick={() => selectAll(p.id, catalogDetail.models.map((m) => m.model_id))}
                        className="text-xs text-blue-600 hover:text-blue-800"
                      >
                        {selectedModels.length === catalogDetail.models.length ? "Deselect all" : "Select all"}
                      </button>
                    </div>

                    {/* Model rows */}
                    <div className="divide-y divide-gray-100">
                      {catalogDetail.models.map((m) => (
                        <label key={m.model_id} className="flex items-center gap-3 px-4 py-2.5 cursor-pointer hover:bg-white transition-colors">
                          <input
                            type="checkbox"
                            checked={selectedModels.includes(m.model_id)}
                            onChange={() => toggleModel(p.id, m.model_id)}
                            className="rounded border-gray-300 shrink-0"
                          />
                          <span className="flex-1 text-sm text-gray-900 min-w-0">{m.model_name}</span>
                          <span className="text-xs font-mono text-gray-400 shrink-0">{m.model_id}</span>
                        </label>
                      ))}
                    </div>
                  </>
                ) : p.provider ? (
                  <p className="px-4 py-3 text-xs text-gray-400 italic">Loading models...</p>
                ) : (
                  <p className="px-4 py-3 text-xs text-gray-400 italic">Models determined dynamically.</p>
                )}
              </div>
            )}
          </div>
        );
      })}

      {/* Primary Model dropdown — only in edit mode when models are selected */}
      {onDefaultModelChange && allActiveModelOptions.length > 0 && (
        <div className="pt-3 border-t border-gray-200">
          <label className="block text-xs text-gray-500 mb-1">Primary Model</label>
          <select
            value={defaultModel ?? ""}
            onChange={(e) => onDefaultModelChange(e.target.value)}
            className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white"
          >
            <option value="">None (auto)</option>
            {allActiveModelOptions.map((m) => (
              <option key={m.value} value={m.value}>{m.label}</option>
            ))}
          </select>
        </div>
      )}
    </div>
  );
}
