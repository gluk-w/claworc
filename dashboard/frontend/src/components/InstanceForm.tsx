import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import MonacoConfigEditor from "./MonacoConfigEditor";
import type { InstanceCreatePayload } from "@/types/instance";

interface InstanceFormProps {
  onSubmit: (payload: InstanceCreatePayload) => void;
  onCancel: () => void;
  loading?: boolean;
}

export default function InstanceForm({
  onSubmit,
  onCancel,
  loading,
}: InstanceFormProps) {
  const [displayName, setDisplayName] = useState("");
  const [cpuRequest, setCpuRequest] = useState("500m");
  const [cpuLimit, setCpuLimit] = useState("2000m");
  const [memoryRequest, setMemoryRequest] = useState("1Gi");
  const [memoryLimit, setMemoryLimit] = useState("4Gi");
  const [storageClawdbot, setStorageClawdbot] = useState("5Gi");
  const [storageHomebrew, setStorageHomebrew] = useState("10Gi");
  const [storageClawd, setStorageClawd] = useState("5Gi");
  const [storageChrome, setStorageChrome] = useState("5Gi");

  const [showKeys, setShowKeys] = useState(false);
  const [anthropicKey, setAnthropicKey] = useState("");
  const [openaiKey, setOpenaiKey] = useState("");
  const [braveKey, setBraveKey] = useState("");

  const [showConfig, setShowConfig] = useState(false);
  const [config, setConfig] = useState("{}");

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!displayName.trim()) return;

    onSubmit({
      display_name: displayName.trim(),
      cpu_request: cpuRequest,
      cpu_limit: cpuLimit,
      memory_request: memoryRequest,
      memory_limit: memoryLimit,
      storage_clawdbot: storageClawdbot,
      storage_homebrew: storageHomebrew,
      storage_clawd: storageClawd,
      storage_chrome: storageChrome,
      anthropic_api_key: anthropicKey || null,
      openai_api_key: openaiKey || null,
      brave_api_key: braveKey || null,
      clawdbot_config: config || null,
    });
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-6">
      <div>
        <label className="block text-sm font-medium text-gray-700 mb-1">
          Display Name *
        </label>
        <input
          type="text"
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          placeholder="e.g., Bot Alpha"
          required
          className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
        />
      </div>

      <div>
        <h3 className="text-sm font-medium text-gray-700 mb-3">Resources</h3>
        <div className="grid grid-cols-2 gap-4">
          {[
            { label: "CPU Request", value: cpuRequest, set: setCpuRequest },
            { label: "CPU Limit", value: cpuLimit, set: setCpuLimit },
            { label: "Memory Request", value: memoryRequest, set: setMemoryRequest },
            { label: "Memory Limit", value: memoryLimit, set: setMemoryLimit },
          ].map((field) => (
            <div key={field.label}>
              <label className="block text-xs text-gray-500 mb-1">
                {field.label}
              </label>
              <input
                type="text"
                value={field.value}
                onChange={(e) => field.set(e.target.value)}
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
          ))}
        </div>
      </div>

      <div>
        <h3 className="text-sm font-medium text-gray-700 mb-3">Storage</h3>
        <div className="grid grid-cols-2 gap-4">
          {[
            { label: "Clawdbot Storage", value: storageClawdbot, set: setStorageClawdbot },
            { label: "Homebrew Storage", value: storageHomebrew, set: setStorageHomebrew },
            { label: "Clawd Storage", value: storageClawd, set: setStorageClawd },
            { label: "Chrome Storage", value: storageChrome, set: setStorageChrome },
          ].map((field) => (
            <div key={field.label}>
              <label className="block text-xs text-gray-500 mb-1">
                {field.label}
              </label>
              <input
                type="text"
                value={field.value}
                onChange={(e) => field.set(e.target.value)}
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
          ))}
        </div>
      </div>

      <div className="border border-gray-200 rounded-md">
        <button
          type="button"
          onClick={() => setShowKeys(!showKeys)}
          className="w-full flex items-center gap-2 px-4 py-3 text-sm font-medium text-gray-700 hover:bg-gray-50"
        >
          {showKeys ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
          API Key Overrides
        </button>
        {showKeys && (
          <div className="px-4 pb-4 space-y-3 border-t border-gray-200">
            <p className="text-xs text-gray-500 mt-3">
              Leave empty to use global keys from Settings.
            </p>
            {[
              { label: "Anthropic API Key", value: anthropicKey, set: setAnthropicKey },
              { label: "OpenAI API Key", value: openaiKey, set: setOpenaiKey },
              { label: "Brave API Key", value: braveKey, set: setBraveKey },
            ].map((field) => (
              <div key={field.label}>
                <label className="block text-xs text-gray-500 mb-1">
                  {field.label}
                </label>
                <input
                  type="password"
                  value={field.value}
                  onChange={(e) => field.set(e.target.value)}
                  placeholder="Leave empty to use global key"
                  className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="border border-gray-200 rounded-md">
        <button
          type="button"
          onClick={() => setShowConfig(!showConfig)}
          className="w-full flex items-center gap-2 px-4 py-3 text-sm font-medium text-gray-700 hover:bg-gray-50"
        >
          {showConfig ? (
            <ChevronDown size={16} />
          ) : (
            <ChevronRight size={16} />
          )}
          Initial Config (clawdbot.json)
        </button>
        {showConfig && (
          <div className="border-t border-gray-200">
            <MonacoConfigEditor
              value={config}
              onChange={(v) => setConfig(v ?? "{}")}
              height="200px"
            />
          </div>
        )}
      </div>

      <div className="flex justify-end gap-3 pt-4">
        <button
          type="button"
          onClick={onCancel}
          className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
        >
          Cancel
        </button>
        <button
          type="submit"
          disabled={loading || !displayName.trim()}
          className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {loading ? "Creating..." : "Create"}
        </button>
      </div>
    </form>
  );
}
