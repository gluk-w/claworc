import { useState, useMemo } from "react";
import { X, Search, Brain, Eye, Plus } from "lucide-react";
import type { LLMProvider } from "@/types/instance";
import type { CatalogProviderDetail } from "@/api/llm";

interface ModelCatalogPickerProps {
  selectedModels: string[];
  onSelect: (id: string) => void;
  onClose: () => void;
  providers: LLMProvider[];
  catalogDetailMap: Record<string, CatalogProviderDetail>;
  defaultProvider?: string;
}

interface DerivedModel {
  id: string;
  name: string;
  reasoning: boolean;
  vision: boolean;
}

interface DerivedProvider {
  key: string;
  label: string;
  apiFormat: string;
  dynamic: boolean;
  models: DerivedModel[];
}

export default function ModelCatalogPicker({
  selectedModels,
  onSelect,
  onClose,
  providers,
  catalogDetailMap,
  defaultProvider,
}: ModelCatalogPickerProps) {
  const [activeProvider, setActiveProvider] = useState<string | null>(defaultProvider ?? null);
  const [search, setSearch] = useState("");

  const derivedProviders = useMemo((): DerivedProvider[] => {
    return providers.map((p) => {
      const catalogDetail = p.provider ? catalogDetailMap[p.provider] : undefined;
      const isCustom = (p.models?.length ?? 0) > 0;
      const dynamic = !p.provider && !isCustom;
      const models: DerivedModel[] = catalogDetail
        ? catalogDetail.models.map((m) => ({
            id: m.model_id,
            name: m.model_name,
            reasoning: m.reasoning,
            vision: m.vision,
          }))
        : isCustom
        ? (p.models ?? []).map((m) => ({
            id: m.id,
            name: m.name,
            reasoning: !!m.reasoning,
            vision: false,
          }))
        : [];
      return {
        key: p.key,
        label: p.name,
        apiFormat: p.api_type,
        dynamic,
        models,
      };
    });
  }, [providers, catalogDetailMap]);

  const filteredProviders = useMemo(() => {
    if (!search.trim()) {
      return derivedProviders.map((p) => ({ ...p, filteredModels: p.models }));
    }
    const q = search.toLowerCase();
    return derivedProviders
      .map((p) => ({
        ...p,
        filteredModels: p.models.filter(
          (m) => m.id.toLowerCase().includes(q) || m.name.toLowerCase().includes(q),
        ),
      }))
      .filter((p) => p.filteredModels.length > 0 || p.label.toLowerCase().includes(q));
  }, [derivedProviders, search]);

  const visibleProviders = useMemo(() => {
    if (activeProvider && !search.trim()) {
      return filteredProviders.filter((p) => p.key === activeProvider);
    }
    return filteredProviders;
  }, [filteredProviders, activeProvider, search]);

  const totalModelCount = useMemo(
    () => derivedProviders.reduce((acc, p) => acc + p.models.length, 0),
    [derivedProviders],
  );

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="bg-white rounded-lg shadow-xl w-full max-w-4xl max-h-[85vh] flex flex-col">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200 shrink-0">
          <h3 className="text-sm font-medium text-gray-900">Browse Model Catalog</h3>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600">
            <X size={18} />
          </button>
        </div>

        {/* Search */}
        <div className="px-6 py-3 border-b border-gray-200 shrink-0">
          <div className="relative">
            <Search size={14} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search models or providers..."
              className="w-full pl-8 pr-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              autoFocus
            />
          </div>
        </div>

        {/* Body */}
        <div className="flex flex-1 min-h-0">
          {/* Left sidebar */}
          <div className="w-48 border-r border-gray-200 overflow-y-auto shrink-0">
            <button
              onClick={() => setActiveProvider(null)}
              className={`w-full text-left px-4 py-2.5 text-sm flex items-center justify-between hover:bg-gray-50 ${
                activeProvider === null && !search.trim()
                  ? "bg-blue-50 text-blue-700 font-medium"
                  : "text-gray-700"
              }`}
            >
              <span>All Providers</span>
              <span className="text-xs text-gray-400">{totalModelCount}</span>
            </button>
            {derivedProviders.map((provider) => (
              <button
                key={provider.key}
                onClick={() => {
                  setActiveProvider(provider.key);
                  setSearch("");
                }}
                className={`w-full text-left px-4 py-2.5 text-sm flex items-center justify-between hover:bg-gray-50 ${
                  activeProvider === provider.key && !search.trim()
                    ? "bg-blue-50 text-blue-700 font-medium"
                    : "text-gray-700"
                }`}
              >
                <span className="truncate mr-1">{provider.label}</span>
                <span className="text-xs text-gray-400 shrink-0">
                  {provider.dynamic ? "∞" : provider.models.length}
                </span>
              </button>
            ))}
          </div>

          {/* Right panel */}
          <div className="flex-1 overflow-y-auto">
            {visibleProviders.length === 0 ? (
              <div className="flex items-center justify-center h-32 text-sm text-gray-500">
                No models match your search.
              </div>
            ) : (
              visibleProviders.map((provider) => (
                <div key={provider.key}>
                  <div className="sticky top-0 bg-gray-50 border-b border-gray-200 px-4 py-2 flex items-center gap-2">
                    <span className="text-xs font-semibold text-gray-600 uppercase tracking-wide">
                      {provider.label}
                    </span>
                    <span className="text-xs text-gray-400">{provider.apiFormat}</span>
                  </div>

                  {provider.dynamic ? (
                    <div className="px-4 py-4 text-sm text-gray-500 italic">
                      Models determined dynamically.
                    </div>
                  ) : provider.filteredModels.length === 0 ? (
                    <div className="px-4 py-4 text-sm text-gray-400 italic">
                      No models match your search in this provider.
                    </div>
                  ) : (
                    provider.filteredModels.map((model) => {
                      const isSelected = selectedModels.includes(model.id);
                      return (
                        <div
                          key={model.id}
                          className="flex items-center gap-3 px-4 py-2.5 border-b border-gray-100 hover:bg-gray-50"
                        >
                          <div className="flex-1 min-w-0">
                            <div className="text-sm font-mono text-gray-900 truncate">
                              {model.id}
                            </div>
                            <div className="text-xs text-gray-500 mt-0.5 flex items-center gap-2">
                              <span>{model.name}</span>
                              {model.reasoning && (
                                <span className="inline-flex items-center gap-0.5 px-1.5 py-0.5 bg-purple-50 text-purple-700 rounded text-xs">
                                  <Brain size={10} />
                                  reasoning
                                </span>
                              )}
                              {model.vision && (
                                <span className="inline-flex items-center gap-0.5 px-1.5 py-0.5 bg-teal-50 text-teal-700 rounded text-xs">
                                  <Eye size={10} />
                                  vision
                                </span>
                              )}
                            </div>
                          </div>
                          <button
                            type="button"
                            onClick={() => onSelect(model.id)}
                            disabled={isSelected}
                            className={`shrink-0 inline-flex items-center gap-1 px-2.5 py-1 text-xs font-medium rounded-md border transition-colors ${
                              isSelected
                                ? "bg-green-50 text-green-700 border-green-200 cursor-default"
                                : "text-blue-600 border-blue-300 hover:bg-blue-50"
                            }`}
                          >
                            {isSelected ? (
                              "Added"
                            ) : (
                              <>
                                <Plus size={12} />
                                Add
                              </>
                            )}
                          </button>
                        </div>
                      );
                    })
                  )}
                </div>
              ))
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
