import { useState, useMemo } from "react";
import type { Settings } from "@/types/settings";
import { PROVIDERS } from "./providerData";
import type { Provider, ProviderCategory } from "./providerData";
import ProviderCard from "./ProviderCard";
import ProviderConfigModal from "./ProviderConfigModal";

/** Category display order */
const CATEGORY_ORDER: ProviderCategory[] = [
  "Major Providers",
  "Open Source / Inference",
  "Specialized",
  "Aggregators",
  "Search & Tools",
];

interface PendingChange {
  apiKey: string;
  baseUrl?: string;
}

export interface ProviderSavePayload {
  api_keys?: Record<string, string>;
  delete_api_keys?: string[];
}

interface ProviderGridProps {
  settings: Settings;
  onSaveChanges: (payload: ProviderSavePayload) => Promise<void>;
  isSaving: boolean;
}

export default function ProviderGrid({
  settings,
  onSaveChanges,
  isSaving,
}: ProviderGridProps) {
  const [selectedProvider, setSelectedProvider] = useState<Provider | null>(null);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [pendingChanges, setPendingChanges] = useState<Record<string, PendingChange>>({});
  const [pendingDeletes, setPendingDeletes] = useState<string[]>([]);

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

  /** Group providers by category */
  const grouped = useMemo(() => {
    const map = new Map<ProviderCategory, Provider[]>();
    for (const cat of CATEGORY_ORDER) {
      map.set(cat, []);
    }
    for (const p of PROVIDERS) {
      map.get(p.category)?.push(p);
    }
    return map;
  }, []);

  const handleConfigure = (provider: Provider) => {
    setSelectedProvider(provider);
    setIsModalOpen(true);
  };

  const handleSave = (apiKey: string, baseUrl?: string) => {
    if (!selectedProvider) return;
    const envVar = selectedProvider.envVarName;
    setPendingChanges((prev) => ({ ...prev, [envVar]: { apiKey, baseUrl } }));
    setPendingDeletes((prev) => prev.filter((k) => k !== envVar));
    setIsModalOpen(false);
    setSelectedProvider(null);
  };

  const handleDelete = (provider: Provider) => {
    const envVar = provider.envVarName;
    setPendingDeletes((prev) =>
      prev.includes(envVar) ? prev : [...prev, envVar],
    );
    setPendingChanges((prev) => {
      const next = { ...prev };
      delete next[envVar];
      return next;
    });
  };

  const handleCloseModal = () => {
    setIsModalOpen(false);
    setSelectedProvider(null);
  };

  const hasChanges =
    Object.keys(pendingChanges).length > 0 || pendingDeletes.length > 0;

  const handleSaveChanges = () => {
    const payload: ProviderSavePayload = {};

    const apiKeys: Record<string, string> = {};
    for (const [envVar, change] of Object.entries(pendingChanges)) {
      apiKeys[envVar] = change.apiKey;
    }
    if (Object.keys(apiKeys).length > 0) {
      payload.api_keys = apiKeys;
    }

    if (pendingDeletes.length > 0) {
      payload.delete_api_keys = pendingDeletes;
    }

    onSaveChanges(payload)
      .then(() => {
        setPendingChanges({});
        setPendingDeletes([]);
      })
      .catch(() => {
        // Error handling is managed by the parent (e.g. LLMProvidersTab)
      });
  };

  return (
    <div className="space-y-6">
      <p className="text-sm text-gray-600">
        <span className="font-medium text-gray-900">{configuredCount}</span> of{" "}
        {PROVIDERS.length} providers configured
      </p>

      {CATEGORY_ORDER.map((category) => {
        const providers = grouped.get(category);
        if (!providers || providers.length === 0) return null;
        return (
          <div key={category}>
            <h3 className="text-xs font-semibold text-gray-500 uppercase tracking-wider mb-3">
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
                />
              ))}
            </div>
          </div>
        );
      })}

      {hasChanges && (
        <div className="flex justify-end pt-4 border-t border-gray-200">
          <button
            onClick={handleSaveChanges}
            disabled={isSaving}
            className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {isSaving ? "Saving..." : "Save Changes"}
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
        />
      )}
    </div>
  );
}
