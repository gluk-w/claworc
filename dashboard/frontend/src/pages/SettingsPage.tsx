import { useState } from "react";
import { AlertTriangle } from "lucide-react";
import ApiKeySettings from "@/components/ApiKeySettings";
import { useSettings, useUpdateSettings } from "@/hooks/useSettings";
import type { SettingsUpdate } from "@/types/settings";

export default function SettingsPage() {
  const { data: settings, isLoading } = useSettings();
  const updateMutation = useUpdateSettings();

  const [changes, setChanges] = useState<SettingsUpdate>({});

  // Resource field local state
  const [resources, setResources] = useState<SettingsUpdate>({});

  if (isLoading || !settings) {
    return <div className="text-center py-12 text-gray-500">Loading...</div>;
  }

  const handleSave = () => {
    const payload: SettingsUpdate = { ...resources };
    if (changes.anthropic_api_key)
      payload.anthropic_api_key = changes.anthropic_api_key;
    if (changes.openai_api_key)
      payload.openai_api_key = changes.openai_api_key;
    if (changes.brave_api_key)
      payload.brave_api_key = changes.brave_api_key;

    updateMutation.mutate(payload, {
      onSuccess: () => {
        setChanges({});
        setResources({});
      },
    });
  };

  const resourceFields: {
    key: keyof SettingsUpdate;
    label: string;
  }[] = [
    { key: "default_cpu_request", label: "Default CPU Request" },
    { key: "default_cpu_limit", label: "Default CPU Limit" },
    { key: "default_memory_request", label: "Default Memory Request" },
    { key: "default_memory_limit", label: "Default Memory Limit" },
    { key: "default_storage_clawdbot", label: "Default Clawdbot Storage" },
    { key: "default_storage_homebrew", label: "Default Homebrew Storage" },
    { key: "default_storage_clawd", label: "Default Clawd Storage" },
    { key: "default_storage_chrome", label: "Default Chrome Storage" },
  ];

  const hasChanges =
    Object.keys(changes).length > 0 || Object.keys(resources).length > 0;

  return (
    <div>
      <h1 className="text-xl font-semibold text-gray-900 mb-6">Settings</h1>

      <div className="flex items-center gap-2 px-3 py-2 mb-6 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800">
        <AlertTriangle size={16} className="shrink-0" />
        Changing global API keys will update all instances that don't have
        overrides.
      </div>

      <div className="space-y-8 max-w-2xl">
        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <ApiKeySettings
            anthropicKey={settings.anthropic_api_key}
            openaiKey={settings.openai_api_key}
            braveKey={settings.brave_api_key}
            onAnthropicChange={(v) =>
              setChanges((c) => ({
                ...c,
                ...(v ? { anthropic_api_key: v } : {}),
              }))
            }
            onOpenaiChange={(v) =>
              setChanges((c) => ({
                ...c,
                ...(v ? { openai_api_key: v } : {}),
              }))
            }
            onBraveChange={(v) =>
              setChanges((c) => ({
                ...c,
                ...(v ? { brave_api_key: v } : {}),
              }))
            }
          />
        </div>

        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <h3 className="text-sm font-medium text-gray-900 mb-4">
            Default Resource Limits
          </h3>
          <div className="grid grid-cols-2 gap-4">
            {resourceFields.map((field) => (
              <div key={field.key}>
                <label className="block text-xs text-gray-500 mb-1">
                  {field.label}
                </label>
                <input
                  type="text"
                  defaultValue={
                    settings[field.key as keyof typeof settings] ?? ""
                  }
                  onChange={(e) =>
                    setResources((r) => ({ ...r, [field.key]: e.target.value }))
                  }
                  className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>
            ))}
          </div>
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
    </div>
  );
}
