import { useState } from "react";
import type { Settings, SettingsUpdatePayload } from "@/types/settings";
import { useUpdateSettings } from "@/hooks/useSettings";

interface AgentImageTabProps {
  settings: Settings;
  onFieldChange: (field: string, value: string) => void;
}

export default function AgentImageTab({
  settings,
  onFieldChange,
}: AgentImageTabProps) {
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
      <h3 className="text-sm font-medium text-gray-900 mb-4">Agent Image</h3>
      <div className="space-y-4">
        <div>
          <label className="block text-xs text-gray-500 mb-1">
            Default Container Image
          </label>
          <input
            type="text"
            defaultValue={settings.default_container_image ?? ""}
            onChange={(e) =>
              handleChange("default_container_image", e.target.value)
            }
            className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>
        <div>
          <label className="block text-xs text-gray-500 mb-1">
            Default VNC Resolution
          </label>
          <input
            type="text"
            defaultValue={settings.default_vnc_resolution || "1920x1080"}
            onChange={(e) =>
              handleChange("default_vnc_resolution", e.target.value)
            }
            placeholder="e.g., 1920x1080"
            className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>
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
