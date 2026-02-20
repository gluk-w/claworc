import { useState, useMemo, useRef, useEffect, useCallback } from "react";
import { useSearchParams } from "react-router-dom";
import { KeyRound, Check, Search, X } from "lucide-react";
import type { Settings } from "@/types/settings";
import { PROVIDERS } from "./providerData";
import type { Provider, ProviderCategory } from "./providerData";
import { CATEGORY_ICONS } from "./providerIcons";
import ProviderCard from "./ProviderCard";
import type { CardAnimationState } from "./ProviderCard";
import ProviderCardSkeleton from "./ProviderCardSkeleton";
import ProviderConfigModal from "./ProviderConfigModal";
import ConfirmDialog, { isSuppressed } from "../ConfirmDialog";
import BatchActionBar from "./BatchActionBar";

/** Category display order */
const CATEGORY_ORDER: ProviderCategory[] = [
  "Major Providers",
  "Open Source / Inference",
  "Specialized",
  "Aggregators",
  "Search & Tools",
];

export type ProviderFilterStatus = "all" | "configured" | "not-configured";

interface PendingChange {
  apiKey: string;
  baseUrl?: string;
}

export interface ProviderSavePayload {
  api_keys?: Record<string, string>;
  base_urls?: Record<string, string>;
  delete_api_keys?: string[];
}

interface ProviderGridProps {
  settings: Settings;
  onSaveChanges: (payload: ProviderSavePayload) => Promise<void>;
  isSaving: boolean;
  isLoading?: boolean;
}

export default function ProviderGrid({
  settings,
  onSaveChanges,
  isSaving,
  isLoading = false,
}: ProviderGridProps) {
  const [searchParams, setSearchParams] = useSearchParams();
  const [selectedProvider, setSelectedProvider] = useState<Provider | null>(null);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [pendingChanges, setPendingChanges] = useState<Record<string, PendingChange>>({});
  const [pendingDeletes, setPendingDeletes] = useState<string[]>([]);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [cardAnimations, setCardAnimations] = useState<Record<string, CardAnimationState>>({});
  const [providerToDelete, setProviderToDelete] = useState<Provider | null>(null);
  const [selectedProviderIds, setSelectedProviderIds] = useState<Set<string>>(new Set());
  const [batchDeleteTarget, setBatchDeleteTarget] = useState<Provider[] | null>(null);
  const animationTimers = useRef<Map<string, ReturnType<typeof setTimeout>>>(new Map());
  const saveSuccessTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Search and filter state from URL query params
  const searchQuery = searchParams.get("q") ?? "";
  const filterStatus = (searchParams.get("filter") as ProviderFilterStatus) || "all";

  const setSearchQuery = useCallback(
    (query: string) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (query) {
          next.set("q", query);
        } else {
          next.delete("q");
        }
        return next;
      }, { replace: true });
    },
    [setSearchParams],
  );

  const setFilterStatus = useCallback(
    (status: ProviderFilterStatus) => {
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev);
        if (status !== "all") {
          next.set("filter", status);
        } else {
          next.delete("filter");
        }
        return next;
      }, { replace: true });
    },
    [setSearchParams],
  );

  /** Schedule clearing an animation state after its duration */
  const scheduleAnimationClear = useCallback((envVar: string, delayMs: number) => {
    const existing = animationTimers.current.get(envVar);
    if (existing) clearTimeout(existing);
    const timer = setTimeout(() => {
      setCardAnimations((prev) => {
        const next = { ...prev };
        delete next[envVar];
        return next;
      });
      animationTimers.current.delete(envVar);
    }, delayMs);
    animationTimers.current.set(envVar, timer);
  }, []);

  // Cleanup timers on unmount
  useEffect(() => {
    return () => {
      animationTimers.current.forEach((t) => clearTimeout(t));
      if (saveSuccessTimer.current) clearTimeout(saveSuccessTimer.current);
    };
  }, []);

  /** Check whether a provider has a key configured (server or pending) */
  const isConfigured = (provider: Provider): boolean => {
    const envVar = provider.envVarName;
    if (pendingDeletes.includes(envVar)) return false;
    if (envVar in pendingChanges) return true;
    return !!settings.api_keys?.[envVar];
  };

  /** Get the masked key for display */
  const getMaskedKey = (provider: Provider): string | null => {
    const envVar = provider.envVarName;
    if (pendingDeletes.includes(envVar)) return null;
    const pending = pendingChanges[envVar];
    if (pending) {
      const key = pending.apiKey;
      return "****" + (key.length > 4 ? key.slice(-4) : key);
    }
    return settings.api_keys?.[envVar] || null;
  };

  const configuredCount = useMemo(
    () => PROVIDERS.filter((p) => isConfigured(p)).length,
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [settings, pendingChanges, pendingDeletes],
  );

  /** Filter providers based on search query and status filter */
  const filteredProviders = useMemo(() => {
    let result = PROVIDERS;

    // Search filter: match on name or description (case-insensitive)
    if (searchQuery) {
      const query = searchQuery.toLowerCase();
      result = result.filter(
        (p) =>
          p.name.toLowerCase().includes(query) ||
          p.description.toLowerCase().includes(query),
      );
    }

    // Status filter
    if (filterStatus === "configured") {
      result = result.filter((p) => isConfigured(p));
    } else if (filterStatus === "not-configured") {
      result = result.filter((p) => !isConfigured(p));
    }

    return result;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchQuery, filterStatus, settings, pendingChanges, pendingDeletes]);

  const hasActiveFilters = searchQuery !== "" || filterStatus !== "all";

  /** Selection helpers */
  const selectionMode = selectedProviderIds.size > 0;

  const selectedProviders = useMemo(
    () => PROVIDERS.filter((p) => selectedProviderIds.has(p.id)),
    [selectedProviderIds],
  );

  const allFilteredSelected = useMemo(
    () => filteredProviders.length > 0 && filteredProviders.every((p) => selectedProviderIds.has(p.id)),
    [filteredProviders, selectedProviderIds],
  );

  const handleToggleSelectAll = useCallback(() => {
    setSelectedProviderIds((prev) => {
      if (allFilteredSelected) {
        // Deselect all currently filtered providers
        const next = new Set(prev);
        for (const p of filteredProviders) {
          next.delete(p.id);
        }
        return next;
      } else {
        // Select all currently filtered providers
        const next = new Set(prev);
        for (const p of filteredProviders) {
          next.add(p.id);
        }
        return next;
      }
    });
  }, [allFilteredSelected, filteredProviders]);

  const handleToggleSelect = useCallback((provider: Provider, selected: boolean) => {
    setSelectedProviderIds((prev) => {
      const next = new Set(prev);
      if (selected) {
        next.add(provider.id);
      } else {
        next.delete(provider.id);
      }
      return next;
    });
  }, []);

  const handleClearSelection = useCallback(() => {
    setSelectedProviderIds(new Set());
  }, []);

  /** Build a map of configured keys for batch operations */
  const configuredKeysMap = useMemo(() => {
    const map: Record<string, string> = {};
    for (const p of PROVIDERS) {
      const masked = getMaskedKey(p);
      if (masked) map[p.envVarName] = masked;
    }
    return map;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settings, pendingChanges, pendingDeletes]);

  /** Group filtered providers by category */
  const grouped = useMemo(() => {
    const map = new Map<ProviderCategory, Provider[]>();
    for (const cat of CATEGORY_ORDER) {
      map.set(cat, []);
    }
    for (const p of filteredProviders) {
      map.get(p.category)?.push(p);
    }
    return map;
  }, [filteredProviders]);

  const handleConfigure = (provider: Provider) => {
    setSelectedProvider(provider);
    setIsModalOpen(true);
  };

  const handleSave = (apiKey: string, baseUrl?: string) => {
    if (!selectedProvider) return;
    const envVar = selectedProvider.envVarName;
    setPendingChanges((prev) => ({ ...prev, [envVar]: { apiKey, baseUrl } }));
    setPendingDeletes((prev) => prev.filter((k) => k !== envVar));
    setCardAnimations((prev) => ({ ...prev, [envVar]: "added" }));
    scheduleAnimationClear(envVar, 200);
    setIsModalOpen(false);
    setSelectedProvider(null);
  };

  const executeDelete = useCallback(
    (provider: Provider) => {
      const envVar = provider.envVarName;
      setCardAnimations((prev) => ({ ...prev, [envVar]: "deleted" }));
      scheduleAnimationClear(envVar, 400);
      setPendingDeletes((prev) =>
        prev.includes(envVar) ? prev : [...prev, envVar],
      );
      setPendingChanges((prev) => {
        const next = { ...prev };
        delete next[envVar];
        return next;
      });
    },
    [scheduleAnimationClear],
  );

  const handleBatchDelete = useCallback(() => {
    const configuredSelected = selectedProviders.filter((p) => isConfigured(p));
    if (configuredSelected.length === 0) return;

    if (isSuppressed("delete-provider")) {
      for (const provider of configuredSelected) {
        executeDelete(provider);
      }
      setSelectedProviderIds(new Set());
    } else {
      setBatchDeleteTarget(configuredSelected);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedProviders, executeDelete]);

  const handleConfirmBatchDelete = () => {
    if (batchDeleteTarget) {
      for (const provider of batchDeleteTarget) {
        executeDelete(provider);
      }
      setSelectedProviderIds(new Set());
      setBatchDeleteTarget(null);
    }
  };

  const handleCancelBatchDelete = () => {
    setBatchDeleteTarget(null);
  };

  const handleDelete = (provider: Provider) => {
    if (isSuppressed("delete-provider")) {
      executeDelete(provider);
    } else {
      setProviderToDelete(provider);
    }
  };

  const handleConfirmDelete = () => {
    if (providerToDelete) {
      executeDelete(providerToDelete);
      setProviderToDelete(null);
    }
  };

  const handleCancelDelete = () => {
    setProviderToDelete(null);
  };

  const handleCloseModal = () => {
    setIsModalOpen(false);
    setSelectedProvider(null);
  };

  const hasChanges =
    Object.keys(pendingChanges).length > 0 || pendingDeletes.length > 0;

  /** Get the current base URL for a provider (pending or server) */
  const getBaseUrl = (provider: Provider): string | undefined => {
    const envVar = provider.envVarName;
    const pending = pendingChanges[envVar];
    if (pending?.baseUrl) return pending.baseUrl;
    return settings.base_urls?.[envVar] || undefined;
  };

  const handleSaveChanges = () => {
    const payload: ProviderSavePayload = {};

    const apiKeys: Record<string, string> = {};
    const baseUrls: Record<string, string> = {};
    for (const [envVar, change] of Object.entries(pendingChanges)) {
      apiKeys[envVar] = change.apiKey;
      if (change.baseUrl) {
        baseUrls[envVar] = change.baseUrl;
      }
    }
    if (Object.keys(apiKeys).length > 0) {
      payload.api_keys = apiKeys;
    }
    if (Object.keys(baseUrls).length > 0) {
      payload.base_urls = baseUrls;
    }

    if (pendingDeletes.length > 0) {
      payload.delete_api_keys = pendingDeletes;
    }

    onSaveChanges(payload)
      .then(() => {
        setPendingChanges({});
        setPendingDeletes([]);
        setSaveSuccess(true);
        if (saveSuccessTimer.current) clearTimeout(saveSuccessTimer.current);
        saveSuccessTimer.current = setTimeout(() => {
          setSaveSuccess(false);
          saveSuccessTimer.current = null;
        }, 1500);
      })
      .catch(() => {
        // Error handling is managed by the parent (e.g. LLMProvidersTab)
      });
  };

  if (isLoading) {
    return (
      <div className="space-y-4" data-testid="provider-grid-loading">
        <div className="h-4 w-48 bg-gray-200 rounded animate-pulse" />
        {CATEGORY_ORDER.map((category) => {
          const providers = grouped.get(category);
          if (!providers || providers.length === 0) return null;
          return (
            <div key={category}>
              <div className="h-3 w-32 bg-gray-200 rounded animate-pulse mb-3" />
              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
                {providers.map((provider) => (
                  <ProviderCardSkeleton key={provider.id} />
                ))}
              </div>
            </div>
          );
        })}
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <label className="inline-flex items-center gap-2 cursor-pointer" data-testid="select-all-container">
          <input
            type="checkbox"
            checked={allFilteredSelected && filteredProviders.length > 0}
            onChange={handleToggleSelectAll}
            aria-label="Select all providers"
            data-testid="select-all-checkbox"
            className="w-4 h-4 rounded border-gray-300 text-blue-600 focus:ring-2 focus:ring-blue-500 cursor-pointer"
          />
          <span className="text-sm text-gray-600">Select all</span>
        </label>
        <p className="text-sm text-gray-600" data-testid="provider-count-summary">
          <span className="font-medium text-gray-900">{configuredCount}</span> of{" "}
          {PROVIDERS.length} providers configured
        </p>
      </div>

      {/* Search and filter controls */}
      <div className="flex flex-col sm:flex-row gap-3" data-testid="provider-search-filter">
        <div className="relative flex-1">
          <Search
            size={16}
            className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400 pointer-events-none"
            aria-hidden="true"
          />
          <input
            type="text"
            value={searchQuery}
            onChange={(e) => setSearchQuery(e.target.value)}
            placeholder="Search providers..."
            aria-label="Search providers"
            data-testid="provider-search-input"
            className="w-full pl-9 pr-8 py-2 text-sm border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
          />
          {searchQuery && (
            <button
              type="button"
              onClick={() => setSearchQuery("")}
              aria-label="Clear search"
              data-testid="provider-search-clear"
              className="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-gray-400 hover:text-gray-600 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              <X size={14} />
            </button>
          )}
        </div>
        <select
          value={filterStatus}
          onChange={(e) => setFilterStatus(e.target.value as ProviderFilterStatus)}
          aria-label="Filter providers by status"
          data-testid="provider-filter-select"
          className="px-3 py-2 text-sm border border-gray-300 rounded-md bg-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
        >
          <option value="all">Show: All</option>
          <option value="configured">Show: Configured</option>
          <option value="not-configured">Show: Not Configured</option>
        </select>
      </div>

      {/* Batch action bar */}
      {selectionMode && (
        <BatchActionBar
          selectedProviders={selectedProviders}
          configuredKeys={configuredKeysMap}
          onDeleteSelected={handleBatchDelete}
          onClearSelection={handleClearSelection}
        />
      )}

      {configuredCount === 0 && !hasActiveFilters && (
        <div
          className="flex flex-col items-center justify-center py-10 text-center"
          data-testid="provider-empty-state"
        >
          <div className="flex items-center justify-center w-12 h-12 rounded-full bg-gray-100 mb-3">
            <KeyRound size={24} className="text-gray-400" />
          </div>
          <p className="text-sm font-medium text-gray-900">
            No providers configured yet
          </p>
          <p className="text-xs text-gray-500 mt-1">
            Get started by adding your first API key
          </p>
        </div>
      )}

      {/* No search results empty state */}
      {hasActiveFilters && filteredProviders.length === 0 && (
        <div
          className="flex flex-col items-center justify-center py-10 text-center"
          data-testid="provider-no-results"
        >
          <div className="flex items-center justify-center w-12 h-12 rounded-full bg-gray-100 mb-3">
            <Search size={24} className="text-gray-400" />
          </div>
          <p className="text-sm font-medium text-gray-900">
            No providers match your search
          </p>
          <p className="text-xs text-gray-500 mt-1">
            Try adjusting your search or filter criteria
          </p>
        </div>
      )}

      {CATEGORY_ORDER.map((category) => {
        const providers = grouped.get(category);
        if (!providers || providers.length === 0) return null;
        const CategoryIcon = CATEGORY_ICONS[category];
        return (
          <div key={category}>
            <h3 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-3 flex items-center gap-1.5">
              {CategoryIcon && (
                <CategoryIcon
                  size={14}
                  aria-hidden="true"
                  data-testid={`category-icon-${category.toLowerCase().replace(/[^a-z]+/g, "-")}`}
                />
              )}
              {category}
            </h3>
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {providers.map((provider) => (
                <ProviderCard
                  key={provider.id}
                  provider={provider}
                  isConfigured={isConfigured(provider)}
                  maskedKey={getMaskedKey(provider)}
                  onConfigure={() => handleConfigure(provider)}
                  onDelete={() => handleDelete(provider)}
                  animationState={cardAnimations[provider.envVarName] ?? "idle"}
                  selectionMode={selectionMode}
                  isSelected={selectedProviderIds.has(provider.id)}
                  onSelect={(selected) => handleToggleSelect(provider, selected)}
                />
              ))}
            </div>
          </div>
        );
      })}

      {(hasChanges || saveSuccess) && (
        <div className="flex justify-end pt-4 border-t border-gray-200">
          <button
            onClick={handleSaveChanges}
            disabled={isSaving || saveSuccess}
            data-testid="save-changes-button"
            className={`px-4 py-2 text-sm font-medium text-white rounded-md transition-all duration-200 ease-in-out disabled:cursor-not-allowed focus:outline-none focus:ring-2 focus:ring-blue-500 ${
              saveSuccess
                ? "bg-green-600"
                : "bg-blue-600 hover:bg-blue-700 disabled:opacity-50"
            }`}
          >
            {isSaving ? (
              "Saving..."
            ) : saveSuccess ? (
              <span className="inline-flex items-center gap-1.5">
                <Check size={16} data-testid="save-success-icon" />
                Saved!
              </span>
            ) : (
              "Save Changes"
            )}
          </button>
        </div>
      )}

      {selectedProvider && (
        <ProviderConfigModal
          provider={selectedProvider}
          isOpen={isModalOpen}
          onClose={handleCloseModal}
          onSave={handleSave}
          currentMaskedKey={getMaskedKey(selectedProvider)}
          currentBaseUrl={getBaseUrl(selectedProvider)}
          isSaving={isSaving}
        />
      )}

      {providerToDelete && (
        <ConfirmDialog
          title="Delete API Key"
          message={`Are you sure you want to delete the ${providerToDelete.name} API key? This will affect all instances without overrides.`}
          confirmLabel="Delete"
          cancelLabel="Cancel"
          storageId="delete-provider"
          onConfirm={handleConfirmDelete}
          onCancel={handleCancelDelete}
        />
      )}

      {batchDeleteTarget && (
        <ConfirmDialog
          title="Delete Selected API Keys"
          message={`Are you sure you want to delete ${batchDeleteTarget.length} API key${batchDeleteTarget.length !== 1 ? "s" : ""}? This will affect all instances without overrides.`}
          confirmLabel="Delete All"
          cancelLabel="Cancel"
          storageId="delete-provider"
          onConfirm={handleConfirmBatchDelete}
          onCancel={handleCancelBatchDelete}
        />
      )}
    </div>
  );
}
