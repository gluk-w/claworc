import { useState, useRef, useCallback } from "react";
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
  const tabRefs = useRef<(HTMLButtonElement | null)[]>([]);

  const handleTabKeyDown = useCallback(
    (e: React.KeyboardEvent, index: number) => {
      let nextIndex: number | null = null;

      if (e.key === "ArrowRight") {
        e.preventDefault();
        nextIndex = (index + 1) % TABS.length;
      } else if (e.key === "ArrowLeft") {
        e.preventDefault();
        nextIndex = (index - 1 + TABS.length) % TABS.length;
      } else if (e.key === "Home") {
        e.preventDefault();
        nextIndex = 0;
      } else if (e.key === "End") {
        e.preventDefault();
        nextIndex = TABS.length - 1;
      }

      if (nextIndex !== null) {
        const tab = TABS[nextIndex]!;
        setActiveTab(tab.id);
        tabRefs.current[nextIndex]?.focus();
      }
    },
    [],
  );

  if (isLoading || !settings) {
    return <div className="text-center py-12 text-gray-500">Loading...</div>;
  }

  return (
    <div>
      <h1 className="text-xl font-semibold text-gray-900 mb-6">Settings</h1>

      <div className="border-b border-gray-200 mb-6">
        <nav
          className="flex gap-0 -mb-px"
          role="tablist"
          aria-label="Settings tabs"
        >
          {TABS.map((tab, index) => (
            <button
              key={tab.id}
              ref={(el) => { tabRefs.current[index] = el; }}
              type="button"
              role="tab"
              id={`tab-${tab.id}`}
              aria-selected={activeTab === tab.id}
              aria-controls={`tabpanel-${tab.id}`}
              tabIndex={activeTab === tab.id ? 0 : -1}
              onClick={() => setActiveTab(tab.id)}
              onKeyDown={(e) => handleTabKeyDown(e, index)}
              className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500 focus:ring-inset ${
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
        {activeTab === "providers" && (
          <div role="tabpanel" id="tabpanel-providers" aria-labelledby="tab-providers">
            <LLMProvidersTab settings={settings} />
          </div>
        )}
        {activeTab === "resources" && (
          <div role="tabpanel" id="tabpanel-resources" aria-labelledby="tab-resources">
            <ResourceLimitsTab
              settings={settings}
              onFieldChange={() => {}}
            />
          </div>
        )}
        {activeTab === "image" && (
          <div role="tabpanel" id="tabpanel-image" aria-labelledby="tab-image">
            <AgentImageTab
              settings={settings}
              onFieldChange={() => {}}
            />
          </div>
        )}
      </div>
    </div>
  );
}
