import { useState } from "react";
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
}

interface ModelEntry {
  id: string;
  name: string;
  tag?: string | null;
  description?: string | null;
}

const TAG_STYLES: Record<string, string> = {
  flagship: "bg-purple-100 text-purple-700",
  balanced: "bg-blue-100 text-blue-700",
  speed: "bg-green-100 text-green-700",
};

export default function ProviderModelSelector({
  providers,
  catalogDetailMap,
  enabledProviders,
  providerModels,
  onUpdate,
}: Props) {
  const [expanded, setExpanded] = useState<Set<number>>(new Set());

  const toggleExpanded = (id: number) => {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const handleModelToggle = (p: LLMProvider, modelId: string, availableModels: ModelEntry[]) => {
    const current = providerModels[p.id] ?? [];
    const next = current.includes(modelId)
      ? current.filter((x) => x !== modelId)
      : [...current, modelId];

    const newProviderModels = { ...providerModels, [p.id]: next };
    let newEnabled = [...enabledProviders];
    if (next.length > 0 && !newEnabled.includes(p.id)) {
      newEnabled = [...newEnabled, p.id];
    } else if (next.length === 0 && newEnabled.includes(p.id)) {
      newEnabled = newEnabled.filter((id) => id !== p.id);
    }
    onUpdate(newEnabled, newProviderModels);
  };

  const handleSelectAll = (p: LLMProvider, availableModels: ModelEntry[]) => {
    const allIds = availableModels.map((m) => m.id);
    const newProviderModels = { ...providerModels, [p.id]: allIds };
    const newEnabled = enabledProviders.includes(p.id)
      ? enabledProviders
      : [...enabledProviders, p.id];
    onUpdate(newEnabled, newProviderModels);
  };

  const handleDeselectAll = (p: LLMProvider) => {
    const newProviderModels = { ...providerModels, [p.id]: [] };
    const newEnabled = enabledProviders.filter((id) => id !== p.id);
    onUpdate(newEnabled, newProviderModels);
  };

  const handleDynamicToggle = (p: LLMProvider) => {
    const newEnabled = enabledProviders.includes(p.id)
      ? enabledProviders.filter((id) => id !== p.id)
      : [...enabledProviders, p.id];
    onUpdate(newEnabled, providerModels);
  };

  return (
    <div className="space-y-2">
      {providers.map((p) => {
        const catalogDetail = p.provider ? catalogDetailMap[p.provider] : undefined;
        const iconKey = catalogDetail?.icon_key ?? undefined;
        const isCustom = (p.models?.length ?? 0) > 0;
        const isDynamic = !p.provider && !isCustom;

        const availableModels: ModelEntry[] = isCustom
          ? (p.models ?? []).map((m) => ({ id: m.id, name: m.name }))
          : (catalogDetail?.models ?? []).map((m) => ({
              id: m.model_id,
              name: m.model_name,
              tag: m.tag,
              description: m.description,
            }));

        const selectedModels = providerModels[p.id] ?? [];
        const isExpanded = expanded.has(p.id);
        const isEnabled = enabledProviders.includes(p.id);

        return (
          <div key={p.id} className="rounded-lg border border-gray-200 overflow-hidden">
            {/* Header */}
            <button
              type="button"
              onClick={() => !isDynamic && toggleExpanded(p.id)}
              className={`w-full flex items-center gap-3 px-4 py-3 bg-white text-left ${!isDynamic ? "cursor-pointer hover:bg-gray-50" : "cursor-default"}`}
            >
              {/* Icon */}
              <div className="w-8 h-8 rounded-lg bg-gray-100 flex items-center justify-center shrink-0">
                {iconKey ? (
                  <ProviderIcon provider={iconKey} size={18} />
                ) : (
                  <span className="text-xs font-semibold text-gray-500">{p.name[0].toUpperCase()}</span>
                )}
              </div>

              {/* Name + subtitle */}
              <div className="flex-1 min-w-0">
                <div className="text-sm font-medium text-gray-900">{p.name}</div>
                {!isDynamic && (
                  <div className="text-xs text-gray-500">
                    {availableModels.length > 0
                      ? `${availableModels.length} model${availableModels.length === 1 ? "" : "s"} available`
                      : catalogDetail
                        ? "Loading models..."
                        : "No models"}
                  </div>
                )}
              </div>

              {/* Right side: toggle for dynamic, chevron for accordion */}
              {isDynamic ? (
                <div
                  onClick={(e) => { e.stopPropagation(); handleDynamicToggle(p); }}
                  className="shrink-0"
                >
                  <button
                    type="button"
                    role="switch"
                    aria-checked={isEnabled}
                    className={`relative inline-flex h-5 w-9 items-center rounded-full transition-colors ${isEnabled ? "bg-blue-600" : "bg-gray-300"}`}
                  >
                    <span
                      className={`inline-block h-3.5 w-3.5 transform rounded-full bg-white shadow transition-transform ${isEnabled ? "translate-x-4.5" : "translate-x-0.5"}`}
                    />
                  </button>
                </div>
              ) : (
                <div className="shrink-0 text-gray-400">
                  {isExpanded ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
                </div>
              )}
            </button>

            {/* Expanded body */}
            {!isDynamic && isExpanded && (
              <div className="border-t border-gray-100">
                {/* Header row */}
                <div className="flex items-center justify-between px-4 py-2 bg-gray-50">
                  <span className="text-xs font-semibold text-gray-500 uppercase tracking-wide">Models</span>
                  <div className="flex gap-3">
                    <button
                      type="button"
                      onClick={() => handleSelectAll(p, availableModels)}
                      className="text-xs text-orange-600 hover:text-orange-800 font-medium"
                    >
                      Select all
                    </button>
                    <span className="text-xs text-gray-300">|</span>
                    <button
                      type="button"
                      onClick={() => handleDeselectAll(p)}
                      className="text-xs text-orange-600 hover:text-orange-800 font-medium"
                    >
                      Deselect all
                    </button>
                  </div>
                </div>

                {/* Model rows */}
                {availableModels.length === 0 ? (
                  <div className="px-4 py-3 text-sm text-gray-400 italic">
                    {p.provider && !catalogDetail ? "Loading models..." : "No models available."}
                  </div>
                ) : (
                  <div className="divide-y divide-gray-100">
                    {availableModels.map((m) => {
                      const checked = selectedModels.includes(m.id);
                      return (
                        <label
                          key={m.id}
                          className="flex items-start gap-3 px-4 py-2.5 cursor-pointer hover:bg-gray-50"
                        >
                          <input
                            type="checkbox"
                            checked={checked}
                            onChange={() => handleModelToggle(p, m.id, availableModels)}
                            className="mt-0.5 rounded border-gray-300 shrink-0"
                          />
                          <div className="flex-1 min-w-0">
                            <div className="flex items-center gap-2 flex-wrap">
                              <span className="text-sm text-gray-900">{m.name}</span>
                              {m.tag && (
                                <span
                                  className={`px-1.5 py-0.5 text-xs font-medium rounded capitalize ${TAG_STYLES[m.tag] ?? "bg-gray-100 text-gray-600"}`}
                                >
                                  {m.tag}
                                </span>
                              )}
                            </div>
                            {m.description && (
                              <div className="text-xs text-gray-500 mt-0.5">{m.description}</div>
                            )}
                          </div>
                          <span className="text-xs font-mono text-gray-400 shrink-0 mt-0.5">{m.id}</span>
                        </label>
                      );
                    })}
                  </div>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}
