import { useState } from "react";
import LLMProvidersTab from "@/components/settings/LLMProvidersTab";
import ResourceLimitsTab from "@/components/settings/ResourceLimitsTab";
import AgentImageTab from "@/components/settings/AgentImageTab";
import { useSettings } from "@/hooks/useSettings";

type TabId = "providers" | "resources" | "image";

const TABS: { id: TabId; label: string }[] = [
  { id: "providers", label: "LLM Providers" },
  { id: "resources", label: "Resource Limits" },
  { id: "image", label: "Agent Image" },
];

export default function SettingsPage() {
  const { data: settings, isLoading } = useSettings();
  const [activeTab, setActiveTab] = useState<TabId>("providers");

  if (isLoading || !settings) {
    return <div className="text-center py-12 text-gray-500">Loading...</div>;
  }

  return (
    <div>
      <h1 className="text-xl font-semibold text-gray-900 mb-6">Settings</h1>

      <div className="border-b border-gray-200 mb-6">
        <nav className="flex gap-0 -mb-px" aria-label="Settings tabs">
          {TABS.map((tab) => (
            <button
              key={tab.id}
              type="button"
              onClick={() => setActiveTab(tab.id)}
              className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
                activeTab === tab.id
                  ? "border-blue-600 text-blue-600"
                  : "border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300"
              }`}
            >
              {tab.label}
            </button>
          ))}
        </nav>
      </div>

      <div className="max-w-4xl">
        {activeTab === "providers" && <LLMProvidersTab settings={settings} />}
        {activeTab === "resources" && (
          <ResourceLimitsTab
            settings={settings}
            onFieldChange={() => {}}
          />
        )}
        {activeTab === "image" && (
          <AgentImageTab
            settings={settings}
            onFieldChange={() => {}}
          />
        )}
      </div>
    </div>
  );
}
