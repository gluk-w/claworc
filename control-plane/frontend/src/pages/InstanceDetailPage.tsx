import { useState, useEffect, useRef } from "react";
import { useParams, useNavigate, useLocation } from "react-router-dom";
import { AlertTriangle, ChevronDown, ChevronRight, Wrench } from "lucide-react";
import StatusBadge from "@/components/StatusBadge";
import ActionButtons from "@/components/ActionButtons";
import ProviderTable from "@/components/ProviderTable";
import MonacoConfigEditor from "@/components/MonacoConfigEditor";
import LogViewer from "@/components/LogViewer";
import TerminalPanel from "@/components/TerminalPanel";
import VncPanel from "@/components/VncPanel";
import ChatPanel from "@/components/ChatPanel";
import FileBrowser from "@/components/FileBrowser";
import SSHStatus from "@/components/SSHStatus";
import SSHTunnelList from "@/components/SSHTunnelList";
import SSHEventLog from "@/components/SSHEventLog";
import SSHTroubleshoot from "@/components/SSHTroubleshoot";
import SSHIPRestrict from "@/components/SSHIPRestrict";
import {
  useInstance,
  useStartInstance,
  useStopInstance,
  useRestartInstance,
  useCloneInstance,
  useDeleteInstance,
  useUpdateInstance,
  useInstanceConfig,
  useUpdateInstanceConfig,
  useRestartedToast,
} from "@/hooks/useInstances";
import { useSettings } from "@/hooks/useSettings";
import { useSSHStatus } from "@/hooks/useSSH";
import { useInstanceLogs } from "@/hooks/useInstanceLogs";
import { useTerminal } from "@/hooks/useTerminal";
import { useDesktop } from "@/hooks/useDesktop";
import { useChat } from "@/hooks/useChat";
import type { InstanceUpdatePayload } from "@/types/instance";
import { useAuth } from "@/contexts/AuthContext";

type Tab = "overview" | "chrome" | "terminal" | "files" | "config" | "logs";

export default function InstanceDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const instanceId = Number(id);

  const { isAdmin } = useAuth();
  const { data: instance, isLoading } = useInstance(instanceId);
  const { data: settings } = useSettings();
  useRestartedToast(instance ? [instance] : undefined);
  const { data: configData } = useInstanceConfig(instanceId, instance?.status === "running");
  const startMutation = useStartInstance();
  const stopMutation = useStopInstance();
  const restartMutation = useRestartInstance();
  const cloneMutation = useCloneInstance();
  const deleteMutation = useDeleteInstance();
  const updateMutation = useUpdateInstance();
  const updateConfigMutation = useUpdateInstanceConfig();

  // Get initial tab from URL hash (supports #files:///path pattern)
  const getTabFromHash = (): Tab => {
    const hash = location.hash.slice(1); // Remove '#'
    if (hash === "chrome" || hash === "terminal" || hash === "config" || hash === "logs") {
      return hash;
    }
    if (hash === "files" || hash.startsWith("files://")) {
      return "files";
    }
    return "overview";
  };

  const getFilesPathFromHash = (): string => {
    const hash = location.hash.slice(1);
    if (hash.startsWith("files://")) {
      return hash.slice("files://".length) || "/";
    }
    return "/";
  };

  const [activeTab, setActiveTab] = useState<Tab>(getTabFromHash());
  const [editedConfig, setEditedConfig] = useState<string | null>(null);
  // Terminal/Chrome are mounted once the user first visits the tab, then stay mounted
  const [terminalActivated, setTerminalActivated] = useState(getTabFromHash() === "terminal");
  const [chromeActivated, setChromeActivated] = useState(getTabFromHash() === "chrome");

  // SSH tunnel detail toggle
  const [sshTunnelsExpanded, setSSHTunnelsExpanded] = useState(false);
  const [sshEventsExpanded, setSSHEventsExpanded] = useState(false);
  const [sshTroubleshootOpen, setSSHTroubleshootOpen] = useState(false);
  const { data: sshStatus } = useSSHStatus(instanceId, instance?.status === "running");

  // API key editing state
  const [editingKeys, setEditingKeys] = useState(false);
  const [pendingKeyUpdates, setPendingKeyUpdates] = useState<Record<string, string | null>>({});

  // Update tab when hash changes
  useEffect(() => {
    const tab = getTabFromHash();
    setActiveTab(tab);
    if (tab === "terminal") setTerminalActivated(true);
    if (tab === "chrome") setChromeActivated(true);
  }, [location.hash]);

  // Provider enable/disable state
  const [pendingDisabled, setPendingDisabled] = useState<string[] | null>(null);

  // Default model state
  const [pendingDefaultModel, setPendingDefaultModel] = useState<string | null>(null);

  // Reset provider state when instance data changes
  useEffect(() => {
    setPendingDisabled(null);
  }, [instance?.models?.disabled_defaults?.join(",")]);

  // Reset default model state when instance data changes
  useEffect(() => {
    setPendingDefaultModel(null);
  }, [instance?.default_model]);

  const handleFilesPathChange = (path: string) => {
    const hash = path === "/" ? "files" : `files://${path}`;
    navigate(`#${hash}`, { replace: true });
  };

  // Update hash when tab changes
  const handleTabChange = (tab: Tab) => {
    setActiveTab(tab);
    if (tab === "terminal") setTerminalActivated(true);
    if (tab === "chrome") setChromeActivated(true);
    navigate(`#${tab}`, { replace: true });
  };

  const [chatOpen, _setChatOpen] = useState(false);
  const chatInitSentRef = useRef(false);

  const logsHook = useInstanceLogs(instanceId, activeTab === "logs");
  const termHook = useTerminal(instanceId, terminalActivated && instance?.status === "running");
  const desktopHook = useDesktop(instanceId, chromeActivated && instance?.status === "running");
  const chatHook = useChat(instanceId, chatOpen && chromeActivated && instance?.status === "running");

  // Auto-send initial messages when chat connects (delayed to survive StrictMode double-mount)
  useEffect(() => {
    if (chatHook.connectionState !== "connected" || !chatOpen || chatInitSentRef.current) return;
    const timer = setTimeout(() => {
      chatInitSentRef.current = true;
      chatHook.clearMessages();
      chatHook.sendMessage("/new");
      chatHook.sendMessage("You need to interact with the current tab in the Browser");
    }, 300);
    return () => clearTimeout(timer);
  }, [chatHook.connectionState, chatOpen, chatHook.sendMessage, chatHook.clearMessages]);

  // Reset init flag when chat is closed so re-opening starts fresh
  useEffect(() => {
    if (!chatOpen) {
      chatInitSentRef.current = false;
    }
  }, [chatOpen]);

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
  const currentDefaultModel = pendingDefaultModel ?? instance?.default_model ?? "";
  const currentDisabled = pendingDisabled ?? instance?.models?.disabled_defaults ?? [];

  const handleSaveConfig = () => {
    updateConfigMutation.mutate(
      { id: instanceId, config: currentConfig },
      { onSuccess: () => setEditedConfig(null) },
    );
  };

  const handleResetConfig = () => {
    setEditedConfig(null);
  };

  const handleSaveKeys = () => {
    const hasKeyChanges = Object.keys(pendingKeyUpdates).length > 0;
    const hasProviderChanges = pendingDisabled !== null;
    const hasDefaultModelChange = pendingDefaultModel !== null;
    if (!hasKeyChanges && !hasProviderChanges && !hasDefaultModelChange) return;
    const payload: InstanceUpdatePayload = {};
    if (hasKeyChanges) {
      payload.api_keys = pendingKeyUpdates;
    }
    if (hasProviderChanges) {
      payload.models = { disabled: pendingDisabled!, extra: instance!.models.extra ?? [] };
    }
    if (hasDefaultModelChange) {
      payload.default_model = pendingDefaultModel!;
    }
    updateMutation.mutate(
      { id: instanceId, payload },
      {
        onSuccess: () => {
          setEditingKeys(false);
          setPendingKeyUpdates({});
          setPendingDisabled(null);
          setPendingDefaultModel(null);
        },
      },
    );
  };

  // Compute pending new keys (keys being added that aren't existing overrides)
  const pendingNewKeys: Record<string, string> = {};
  for (const [k, v] of Object.entries(pendingKeyUpdates)) {
    if (v !== null && !instance.api_key_overrides.includes(k)) {
      pendingNewKeys[k] = v;
    }
  }

  // Compute pending removals
  const pendingRemovals: Record<string, true> = {};
  for (const [k, v] of Object.entries(pendingKeyUpdates)) {
    if (v === null) {
      pendingRemovals[k] = true;
    }
  }

  const handleToggleEnabled = (key: string) => {
    setPendingDisabled((prev) => {
      const list = prev ?? instance.models.disabled_defaults ?? [];
      return list.includes(key)
        ? list.filter((p) => p !== key)
        : [...list, key];
    });
    // If disabling the current default, clear it
    if (!currentDisabled.includes(key) && currentDefaultModel === key) {
      setPendingDefaultModel("");
    }
  };

  const handleDefaultModelChange = (key: string) => {
    setPendingDefaultModel(key);
  };

  const handleAddKey = (key: string, value: string) => {
    setPendingKeyUpdates((prev) => ({ ...prev, [key]: value }));
  };

  const handleRemoveKey = (key: string) => {
    setPendingKeyUpdates((prev) => ({ ...prev, [key]: null }));
  };

  const handleUndoRemove = (key: string) => {
    setPendingKeyUpdates((prev) => {
      const next = { ...prev };
      delete next[key];
      return next;
    });
  };

  const handleUndoAdd = (key: string) => {
    setPendingKeyUpdates((prev) => {
      const next = { ...prev };
      delete next[key];
      return next;
    });
  };

  const tabs: { key: Tab; label: string }[] = [
    { key: "overview", label: "Overview" },
    { key: "chrome", label: "Browser" },
    { key: "terminal", label: "Terminal" },
    { key: "config", label: "Config" },
    { key: "logs", label: "Logs" },
  ];

  return (
    <div>
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
          onRestart={(id) =>
            restartMutation.mutate({ id, displayName: instance.display_name })
          }
          onClone={(id) =>
            cloneMutation.mutate({ id, displayName: instance.display_name })
          }
          onDelete={(id) =>
            deleteMutation.mutate(id, {
              onSuccess: () => navigate("/"),
            })
          }
        />
      </div>

      <div className="border-b border-gray-200 mb-4">
        <nav className="flex gap-6">
          {tabs.map((tab) => (
            <button
              key={tab.key}
              onClick={() => handleTabChange(tab.key)}
              className={`pb-3 text-sm font-medium border-b-2 ${activeTab === tab.key
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
        <div className="space-y-6">
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <div className="grid grid-cols-2 gap-y-4 gap-x-8">
              {[
                { label: "Instance Name", value: instance.name },
                { label: "Display Name", value: instance.display_name },
                { label: "Status", value: instance.status },
                {
                  label: "CPU",
                  value: `${instance.cpu_request} / ${instance.cpu_limit}`,
                },
                {
                  label: "Memory",
                  value: `${instance.memory_request} / ${instance.memory_limit}`,
                },
                {
                  label: "Storage (Homebrew)",
                  value: instance.storage_homebrew,
                },
                { label: "Storage (Clawd)", value: instance.storage_clawd },
                { label: "Storage (Browser)", value: instance.storage_chrome },
                {
                  label: "Agent Image",
                  value: instance.has_image_override
                    ? instance.container_image ?? ""
                    : "Default",
                },
                {
                  label: "VNC Resolution",
                  value: instance.has_resolution_override
                    ? instance.vnc_resolution ?? ""
                    : "Default",
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

          {/* SSH Connection Status Section */}
          {instance.status === "running" && (
            <>
              <SSHStatus instanceId={instanceId} enabled={instance.status === "running"} />
              {sshStatus && sshStatus.tunnels.length > 0 && (
                <div className="bg-white rounded-lg border border-gray-200 p-6">
                  <button
                    onClick={() => setSSHTunnelsExpanded(!sshTunnelsExpanded)}
                    className="flex items-center gap-2 text-sm font-medium text-gray-900 w-full"
                  >
                    {sshTunnelsExpanded ? (
                      <ChevronDown size={16} className="text-gray-500" />
                    ) : (
                      <ChevronRight size={16} className="text-gray-500" />
                    )}
                    Tunnel Details
                    <span className="text-xs font-normal text-gray-500">
                      ({sshStatus.tunnels.length} active)
                    </span>
                  </button>
                  {sshTunnelsExpanded && (
                    <div className="mt-4">
                      <SSHTunnelList tunnels={sshStatus.tunnels} />
                    </div>
                  )}
                </div>
              )}
              <div className="bg-white rounded-lg border border-gray-200 p-6">
                <button
                  onClick={() => setSSHEventsExpanded(!sshEventsExpanded)}
                  className="flex items-center gap-2 text-sm font-medium text-gray-900 w-full"
                >
                  {sshEventsExpanded ? (
                    <ChevronDown size={16} className="text-gray-500" />
                  ) : (
                    <ChevronRight size={16} className="text-gray-500" />
                  )}
                  Connection Events
                </button>
                {sshEventsExpanded && (
                  <div className="mt-4">
                    <SSHEventLog instanceId={instanceId} enabled={instance.status === "running"} />
                  </div>
                )}
              </div>
              <div className="flex justify-end">
                <button
                  onClick={() => setSSHTroubleshootOpen(true)}
                  className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
                >
                  <Wrench size={12} />
                  Troubleshoot SSH
                </button>
              </div>
              {sshTroubleshootOpen && (
                <SSHTroubleshoot
                  instanceId={instanceId}
                  onClose={() => setSSHTroubleshootOpen(false)}
                />
              )}
            </>
          )}

          {/* Source IP Restrictions (admin only) */}
          {isAdmin && <SSHIPRestrict instanceId={instanceId} />}

          {/* API Key Overrides Section */}
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <div className="flex items-center justify-between mb-4">
              <h3 className="text-sm font-medium text-gray-900">
                API Key Overrides
              </h3>
              <button
                type="button"
                onClick={() => {
                  setEditingKeys(!editingKeys);
                  if (editingKeys) {
                    setPendingKeyUpdates({});
                    setPendingDisabled(null);
                    setPendingDefaultModel(null);
                  }
                }}
                className="text-xs text-blue-600 hover:text-blue-800"
              >
                {editingKeys ? "Cancel" : "Edit"}
              </button>
            </div>

            <ProviderTable
              globalApiKeys={settings?.api_keys ?? {}}
              instanceOverrides={instance.api_key_overrides}
              disabledProviders={currentDisabled}
              defaultModel={currentDefaultModel}
              pendingNewKeys={pendingNewKeys}
              pendingRemovals={pendingRemovals}
              onToggleEnabled={handleToggleEnabled}
              onDefaultModelChange={handleDefaultModelChange}
              onAddKey={handleAddKey}
              onRemoveKey={handleRemoveKey}
              onUndoRemove={handleUndoRemove}
              onUndoAdd={handleUndoAdd}
              editable={editingKeys}
            />

            {editingKeys && (
              <div className="flex justify-end pt-4">
                <button
                  onClick={handleSaveKeys}
                  disabled={
                    updateMutation.isPending ||
                    (Object.keys(pendingKeyUpdates).length === 0 &&
                      pendingDisabled === null &&
                      pendingDefaultModel === null)
                  }
                  className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
                >
                  {updateMutation.isPending ? "Saving..." : "Save"}
                </button>
              </div>
            )}
          </div>
        </div>
      )}

      {chromeActivated && (
        <div
          className="bg-white rounded-lg border border-gray-200 overflow-hidden h-[calc(100vh-142px)] min-h-[400px] flex"
          style={activeTab !== "chrome" ? { display: "none" } : undefined}
        >
          {instance.status === "running" ? (
            <>
              {chatOpen && (
                <div className="w-96 flex-shrink-0 border-r border-gray-700">
                  <ChatPanel
                    messages={chatHook.messages}
                    connectionState={chatHook.connectionState}
                    onSend={chatHook.sendMessage}
                    onClear={chatHook.clearMessages}
                    onReconnect={chatHook.reconnect}
                  />
                </div>
              )}
              <div className="flex-1 min-w-0">
                <VncPanel
                  instanceId={instanceId}
                  connectionState={desktopHook.connectionState}
                  desktopUrl={desktopHook.desktopUrl}
                  setIframe={desktopHook.setIframe}
                  onLoad={desktopHook.onLoad}
                  onError={desktopHook.onError}
                  reconnect={desktopHook.reconnect}
                  chatOpen={false}
                  showNewWindow={false}
                />
              </div>
            </>
          ) : (
            <div className="flex items-center justify-center h-full w-full text-gray-500 text-sm">
              Instance must be running to view Browser.
            </div>
          )}
        </div>
      )}

      {terminalActivated && (
        <div
          className="bg-white rounded-lg border border-gray-200 overflow-hidden h-[calc(100vh-142px)] min-h-[400px]"
          style={activeTab !== "terminal" ? { display: "none" } : undefined}
        >
          {instance.status === "running" ? (
            <TerminalPanel
              connectionState={termHook.connectionState}
              onData={termHook.onData}
              onResize={termHook.onResize}
              setTerminal={termHook.setTerminal}
              reconnect={termHook.reconnect}
              visible={activeTab === "terminal"}
            />
          ) : (
            <div className="flex items-center justify-center h-full text-gray-500 text-sm">
              Instance must be running to use terminal.
            </div>
          )}
        </div>
      )}

      {activeTab === "files" && (
        <div className="h-[calc(100vh-142px)] min-h-[400px]">
          {instance.status === "running" ? (
            <FileBrowser instanceId={instanceId} initialPath={getFilesPathFromHash()} onPathChange={handleFilesPathChange} />
          ) : (
            <div className="flex items-center justify-center h-full text-gray-500 text-sm">
              Instance must be running to browse files.
            </div>
          )}
        </div>
      )}

      {activeTab === "config" && (
        <div className="flex flex-col gap-4 h-[calc(100vh-142px)] min-h-[400px]">
          {instance.status !== "running" ? (
            <div className="flex items-center justify-center flex-1 text-gray-500 text-sm bg-white rounded-lg border border-gray-200">
              Instance must be running to edit config.
            </div>
          ) : (
            <>
              <div className="bg-white rounded-lg border border-gray-200 overflow-hidden flex-1 min-h-0">
                <MonacoConfigEditor
                  value={currentConfig}
                  onChange={(v) => setEditedConfig(v ?? "{}")}
                  height="100%"
                />
              </div>
              <div className="flex items-center shrink-0">
                <div className="flex items-center gap-2 text-sm text-amber-700">
                  <AlertTriangle size={16} className="shrink-0" />
                  Saving will restart the openclaw-gateway service.
                </div>
                <div className="ml-auto flex gap-3">
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
            </>
          )}
        </div>
      )}

      {activeTab === "logs" && (
        <div className="bg-white rounded-lg border border-gray-200 overflow-hidden h-[calc(100vh-142px)] min-h-[400px]">
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
