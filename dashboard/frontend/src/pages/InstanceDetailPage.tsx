import { useState } from "react";
import { useParams, useNavigate } from "react-router-dom";
import { ArrowLeft, AlertTriangle } from "lucide-react";
import StatusBadge from "@/components/StatusBadge";
import ActionButtons from "@/components/ActionButtons";
import MonacoConfigEditor from "@/components/MonacoConfigEditor";
import LogViewer from "@/components/LogViewer";
import {
  useInstance,
  useStartInstance,
  useStopInstance,
  useRestartInstance,
  useDeleteInstance,
  useInstanceConfig,
  useUpdateInstanceConfig,
} from "@/hooks/useInstances";
import { useInstanceLogs } from "@/hooks/useInstanceLogs";

type Tab = "overview" | "config" | "logs";

export default function InstanceDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const instanceId = Number(id);

  const { data: instance, isLoading } = useInstance(instanceId);
  const { data: configData } = useInstanceConfig(instanceId);
  const startMutation = useStartInstance();
  const stopMutation = useStopInstance();
  const restartMutation = useRestartInstance();
  const deleteMutation = useDeleteInstance();
  const updateConfigMutation = useUpdateInstanceConfig();

  const [activeTab, setActiveTab] = useState<Tab>("overview");
  const [editedConfig, setEditedConfig] = useState<string | null>(null);

  const logsHook = useInstanceLogs(instanceId, activeTab === "logs");

  if (isLoading) {
    return <div className="text-center py-12 text-gray-500">Loading...</div>;
  }

  if (!instance) {
    return (
      <div className="text-center py-12 text-gray-500">
        Instance not found.
      </div>
    );
  }

  const currentConfig = editedConfig ?? configData?.config ?? "{}";

  const handleSaveConfig = () => {
    updateConfigMutation.mutate(
      { id: instanceId, config: currentConfig },
      { onSuccess: () => setEditedConfig(null) },
    );
  };

  const handleResetConfig = () => {
    setEditedConfig(null);
  };

  const tabs: { key: Tab; label: string }[] = [
    { key: "overview", label: "Overview" },
    { key: "config", label: "Config" },
    { key: "logs", label: "Logs" },
  ];

  return (
    <div>
      <button
        onClick={() => navigate("/")}
        className="inline-flex items-center gap-1 text-sm text-gray-600 hover:text-gray-900 mb-4"
      >
        <ArrowLeft size={16} />
        Back to Dashboard
      </button>

      <div className="flex items-center justify-between mb-6">
        <div className="flex items-center gap-3">
          <h1 className="text-xl font-semibold text-gray-900">
            {instance.display_name}
          </h1>
          <StatusBadge status={instance.status} />
        </div>
        <ActionButtons
          instance={instance}
          onStart={(id) => startMutation.mutate(id)}
          onStop={(id) => stopMutation.mutate(id)}
          onRestart={(id) => restartMutation.mutate(id)}
          onDelete={(id) =>
            deleteMutation.mutate(id, {
              onSuccess: () => navigate("/"),
            })
          }
        />
      </div>

      <div className="border-b border-gray-200 mb-6">
        <nav className="flex gap-6">
          {tabs.map((tab) => (
            <button
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              className={`pb-3 text-sm font-medium border-b-2 ${
                activeTab === tab.key
                  ? "border-blue-600 text-blue-600"
                  : "border-transparent text-gray-500 hover:text-gray-700"
              }`}
            >
              {tab.label}
            </button>
          ))}
        </nav>
      </div>

      {activeTab === "overview" && (
        <div className="bg-white rounded-lg border border-gray-200 p-6">
          <div className="grid grid-cols-2 gap-y-4 gap-x-8">
            {[
              { label: "K8s Name", value: instance.name },
              { label: "Display Name", value: instance.display_name },
              { label: "Status", value: instance.status },
              {
                label: "Chrome NodePort",
                value: String(instance.nodeport_chrome),
              },
              {
                label: "Terminal NodePort",
                value: String(instance.nodeport_terminal),
              },
              { label: "Chrome VNC URL", value: instance.vnc_chrome_url },
              { label: "Terminal VNC URL", value: instance.vnc_terminal_url },
              {
                label: "CPU",
                value: `${instance.cpu_request} / ${instance.cpu_limit}`,
              },
              {
                label: "Memory",
                value: `${instance.memory_request} / ${instance.memory_limit}`,
              },
              {
                label: "Storage (Clawdbot)",
                value: instance.storage_clawdbot,
              },
              {
                label: "Storage (Homebrew)",
                value: instance.storage_homebrew,
              },
              { label: "Storage (Clawd)", value: instance.storage_clawd },
              { label: "Storage (Chrome)", value: instance.storage_chrome },
              {
                label: "API Key Overrides",
                value: [
                  instance.has_anthropic_override && "Anthropic",
                  instance.has_openai_override && "OpenAI",
                  instance.has_brave_override && "Brave",
                ]
                  .filter(Boolean)
                  .join(", ") || "None",
              },
              { label: "Created", value: instance.created_at },
              { label: "Updated", value: instance.updated_at },
            ].map((field) => (
              <div key={field.label}>
                <dt className="text-xs text-gray-500">{field.label}</dt>
                <dd className="text-sm text-gray-900 mt-0.5 break-all">
                  {field.value}
                </dd>
              </div>
            ))}
          </div>
        </div>
      )}

      {activeTab === "config" && (
        <div className="space-y-4">
          <div className="flex items-center gap-2 px-3 py-2 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800">
            <AlertTriangle size={16} className="shrink-0" />
            Saving configuration will restart the pod.
          </div>
          <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
            <MonacoConfigEditor
              value={currentConfig}
              onChange={(v) => setEditedConfig(v ?? "{}")}
              height="400px"
            />
          </div>
          <div className="flex justify-end gap-3">
            <button
              onClick={handleResetConfig}
              disabled={editedConfig === null}
              className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50"
            >
              Reset
            </button>
            <button
              onClick={handleSaveConfig}
              disabled={updateConfigMutation.isPending}
              className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {updateConfigMutation.isPending ? "Saving..." : "Save"}
            </button>
          </div>
        </div>
      )}

      {activeTab === "logs" && (
        <div className="bg-white rounded-lg border border-gray-200 overflow-hidden h-[500px]">
          <LogViewer
            logs={logsHook.logs}
            isPaused={logsHook.isPaused}
            isConnected={logsHook.isConnected}
            onTogglePause={logsHook.togglePause}
            onClear={logsHook.clearLogs}
          />
        </div>
      )}
    </div>
  );
}
