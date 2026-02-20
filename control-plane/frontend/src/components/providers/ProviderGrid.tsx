import { useState, useMemo } from "react";
import { useSettings } from "@/hooks/useSettings";
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

export default function ProviderGrid() {
  const { data: settings } = useSettings();

  const [selectedProvider, setSelectedProvider] = useState<Provider | null>(null);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [pendingChanges, setPendingChanges] = useState<Record<string, PendingChange>>({});
  const [pendingDeletes, setPendingDeletes] = useState<string[]>([]);

  /** Check whether a provider has a key configured (server or pending) */
  const isConfigured = (provider: Provider): boolean => {
    const envVar = provider.envVarName;
    // If pending delete, not configured
    if (pendingDeletes.includes(envVar)) return false;
    // If pending change, configured
    if (envVar in pendingChanges) return true;
    // Check server state
    if (!settings) return false;
    if (provider.id === "brave") {
      return !!settings.brave_api_key;
    }
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
    if (!settings) return null;
    if (provider.id === "brave") {
      return settings.brave_api_key || null;
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
