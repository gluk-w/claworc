import { useState } from "react";
import type { Settings, SettingsUpdatePayload } from "@/types/settings";
import { useUpdateSettings } from "@/hooks/useSettings";

interface ResourceLimitsTabProps {
  settings: Settings;
  onFieldChange: (field: string, value: string) => void;
}

const RESOURCE_FIELDS: { key: string; label: string }[] = [
  { key: "default_cpu_request", label: "Default CPU Request" },
  { key: "default_cpu_limit", label: "Default CPU Limit" },
  { key: "default_memory_request", label: "Default Memory Request" },
  { key: "default_memory_limit", label: "Default Memory Limit" },
  { key: "default_storage_homebrew", label: "Default Homebrew Storage" },
  { key: "default_storage_clawd", label: "Default Clawd Storage" },
  { key: "default_storage_chrome", label: "Default Browser Storage" },
];

export default function ResourceLimitsTab({
  settings,
  onFieldChange,
}: ResourceLimitsTabProps) {
  const updateMutation = useUpdateSettings();
  const [pendingFields, setPendingFields] = useState<Record<string, string>>(
    {},
  );

  const handleChange = (field: string, value: string) => {
    setPendingFields((prev) => ({ ...prev, [field]: value }));
    onFieldChange(field, value);
  };

  const hasChanges = Object.keys(pendingFields).length > 0;

  const handleSave = () => {
    const payload: SettingsUpdatePayload = { ...pendingFields };
    updateMutation.mutate(payload, {
      onSuccess: () => {
        setPendingFields({});
      },
    });
  };

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <h3 className="text-sm font-medium text-gray-900 mb-4">
        Default Resource Limits
      </h3>
      <div className="grid grid-cols-2 gap-4">
        {RESOURCE_FIELDS.map((field) => (
          <div key={field.key}>
            <label className="block text-xs text-gray-500 mb-1">
              {field.label}
            </label>
            <input
              type="text"
              defaultValue={
                (settings as Record<string, any>)[field.key] ?? ""
              }
              onChange={(e) => handleChange(field.key, e.target.value)}
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
        ))}
      </div>
      <div className="flex justify-end mt-6">
        <button
          onClick={handleSave}
          disabled={updateMutation.isPending || !hasChanges}
          className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 transition-colors duration-200 disabled:opacity-50 disabled:cursor-not-allowed focus:outline-none focus:ring-2 focus:ring-blue-500"
        >
          {updateMutation.isPending ? "Saving..." : "Save Settings"}
        </button>
      </div>
    </div>
  );
}
