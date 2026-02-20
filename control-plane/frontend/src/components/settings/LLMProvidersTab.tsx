import { AlertTriangle } from "lucide-react";
import type { Settings, SettingsUpdatePayload } from "@/types/settings";
import { useUpdateSettings } from "@/hooks/useSettings";
import { ProviderGrid } from "@/components/providers";
import type { ProviderSavePayload } from "@/components/providers";

interface LLMProvidersTabProps {
  settings: Settings;
  onSave?: (payload: SettingsUpdatePayload) => void;
}

/**
 * Normalize settings so that brave_api_key appears in api_keys
 * under the BRAVE_API_KEY env var. This lets ProviderGrid treat
 * every provider uniformly without knowing about backend field
 * differences.
 */
function normalizeSettings(settings: Settings): Settings {
  if (!settings.brave_api_key) return settings;
  return {
    ...settings,
    api_keys: {
      ...settings.api_keys,
      BRAVE_API_KEY: settings.brave_api_key,
    },
  };
}

export default function LLMProvidersTab({
  settings,
  onSave,
}: LLMProvidersTabProps) {
  const updateMutation = useUpdateSettings();
  const normalized = normalizeSettings(settings);

  const handleSaveChanges = async (payload: ProviderSavePayload) => {
    // Map BRAVE_API_KEY back to the brave_api_key backend field
    const mapped: SettingsUpdatePayload = {};

    if (payload.api_keys) {
      const { BRAVE_API_KEY, ...rest } = payload.api_keys;
      if (BRAVE_API_KEY) {
        mapped.brave_api_key = BRAVE_API_KEY;
      }
      if (Object.keys(rest).length > 0) {
        mapped.api_keys = rest;
      }
    }

    if (payload.delete_api_keys) {
      mapped.delete_api_keys = payload.delete_api_keys;
    }

    await updateMutation.mutateAsync(mapped);
    onSave?.(mapped);
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-2 px-3 py-2 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800">
        <AlertTriangle size={16} className="shrink-0" />
        Changing global API keys will update all instances that don't have
        overrides.
      </div>

      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-4">
          Provider Configuration
        </h3>
        <ProviderGrid
          settings={normalized}
          onSaveChanges={handleSaveChanges}
          isSaving={updateMutation.isPending}
        />
      </div>
    </div>
  );
}
