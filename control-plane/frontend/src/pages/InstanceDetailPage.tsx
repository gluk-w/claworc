import { useState, useEffect, useRef, createElement } from "react";
import { useParams, useNavigate, useLocation } from "react-router-dom";
import { AlertTriangle } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import StatusBadge from "@/components/StatusBadge";
import ModelListEditor from "@/components/ModelListEditor";
import ActionButtons from "@/components/ActionButtons";
import MonacoConfigEditor from "@/components/MonacoConfigEditor";
import LogViewer from "@/components/LogViewer";
import TerminalPanel from "@/components/TerminalPanel";
import VncPanel from "@/components/VncPanel";
import ChatPanel from "@/components/ChatPanel";
import FileBrowser from "@/components/FileBrowser";
import SSHStatus from "@/components/SSHStatus";
import SSHEventLog from "@/components/SSHEventLog";
import SSHTroubleshoot from "@/components/SSHTroubleshoot";
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
import { useProviders } from "@/hooks/useProviders";
import ProviderIcon from "@/components/ProviderIcon";
import AppToast from "@/components/AppToast";
import toast from "react-hot-toast";
import { MODEL_CATALOG } from "@/data/model-catalog";
import { useSSHStatus, useSSHEvents } from "@/hooks/useSSHStatus";
import { useInstanceLogs } from "@/hooks/useInstanceLogs";
import { useTerminal } from "@/hooks/useTerminal";
import { useDesktop } from "@/hooks/useDesktop";
import { useChat } from "@/hooks/useChat";
import type { InstanceUpdatePayload } from "@/types/instance";
import { buildSSHTooltip } from "@/utils/sshTooltip";

type Tab = "overview" | "chrome" | "terminal" | "files" | "config" | "logs";

export default function InstanceDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const instanceId = Number(id);

  const { isAdmin } = useAuth();
  const { data: instance, isLoading } = useInstance(instanceId);
  const { data: allProviders = [] } = useProviders();
  useRestartedToast(instance ? [instance] : undefined);
  const { data: configData } = useInstanceConfig(instanceId, instance?.status === "running");
  const sshStatus = useSSHStatus(instanceId, instance?.status === "running");
  const sshEvents = useSSHEvents(instanceId, instance?.status === "running");
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

  // SSH troubleshoot dialog
  const [troubleshootOpen, setTroubleshootOpen] = useState(false);
  // SSH events modal
  const [eventsOpen, setEventsOpen] = useState(false);

  // Extra models editing state
  const [editingExtraModels, setEditingExtraModels] = useState(false);
  const [pendingExtraModels, setPendingExtraModels] = useState<string[] | null>(null);

  // Timezone override editing state
  const [editingTimezone, setEditingTimezone] = useState(false);
  const [pendingTimezone, setPendingTimezone] = useState<string | null>(null);

  // User-Agent override editing state
  const [editingUserAgent, setEditingUserAgent] = useState(false);
  const [pendingUserAgent, setPendingUserAgent] = useState<string | null>(null);

  // Gateway providers editing state
  const [editingGatewayProviders, setEditingGatewayProviders] = useState(false);
  const [pendingProviders, setPendingProviders] = useState<number[] | null>(null);
  const [pendingProviderModels, setPendingProviderModels] = useState<Record<number, string[]> | null>(null);

  // Update tab when hash changes
  useEffect(() => {
    const tab = getTabFromHash();
    setActiveTab(tab);
    if (tab === "terminal") setTerminalActivated(true);
    if (tab === "chrome") setChromeActivated(true);
  }, [location.hash]);

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

  const handleSaveConfig = () => {
    const toastId = "config-save";
    toast.custom(
      createElement(AppToast, { title: "Saving...", status: "loading", toastId }),
      { id: toastId, duration: Infinity },
    );
    updateConfigMutation.mutate(
      { id: instanceId, config: currentConfig },
      {
        onSuccess: () => {
          setEditedConfig(null);
          toast.custom(
            createElement(AppToast, { title: "OpenClaw settings saved", status: "success", toastId }),
            { id: toastId, duration: 3000 },
          );
        },
        onError: (err: unknown) => {
          const message = err instanceof Error ? err.message : "Unknown error";
          toast.custom(
            createElement(AppToast, { title: "Failed to save settings", description: message, status: "error", toastId }),
            { id: toastId, duration: 5000 },
          );
        },
      },
    );
  };

  const handleResetConfig = () => {
    setEditedConfig(null);
  };

  const handleSaveExtraModels = () => {
    if (pendingExtraModels === null) return;
    const payload: InstanceUpdatePayload = {
      models: {
        disabled: instance!.models.disabled_defaults ?? [],
        extra: pendingExtraModels,
      },
    };
    updateMutation.mutate(
      { id: instanceId, payload },
      {
        onSuccess: () => {
          setEditingExtraModels(false);
          setPendingExtraModels(null);
        },
      },
    );
  };

  const handleSaveTimezone = () => {
    if (pendingTimezone === null) return;
    updateMutation.mutate(
      { id: instanceId, payload: { timezone: pendingTimezone } },
      {
        onSuccess: () => {
          setEditingTimezone(false);
          setPendingTimezone(null);
        },
      },
    );
  };

  const handleSaveUserAgent = () => {
    if (pendingUserAgent === null) return;
    updateMutation.mutate(
      { id: instanceId, payload: { user_agent: pendingUserAgent } },
      {
        onSuccess: () => {
          setEditingUserAgent(false);
          setPendingUserAgent(null);
        },
      },
    );
  };

  const handleSaveGatewayProviders = () => {
    if (pendingProviders === null) return;

    // Collect models from pendingProviderModels with provider prefix
    const providerModels: string[] = [];
    for (const p of allProviders) {
      const bareModels = pendingProviderModels?.[p.id] ?? [];
      for (const m of bareModels) {
        providerModels.push(`${p.key}/${m}`);
      }
    }

    // Keep existing extra_models that don't start with any known provider prefix
    const providerPrefixes = allProviders.map((p) => `${p.key}/`);
    const nonProviderExtras = (instance!.models.extra ?? []).filter(
      (m) => !providerPrefixes.some((prefix) => m.startsWith(prefix)),
    );

    const mergedModels = [...nonProviderExtras, ...providerModels];

    const toastId = "gw-providers-save";
    toast.custom(
      createElement(AppToast, { title: "Saving...", status: "loading", toastId }),
      { id: toastId, duration: Infinity },
    );

    updateMutation.mutate(
      {
        id: instanceId,
        payload: {
          enabled_providers: pendingProviders,
          models: {
            disabled: instance!.models.disabled_defaults ?? [],
            extra: mergedModels,
          },
        },
      },
      {
        onSuccess: () => {
          setEditingGatewayProviders(false);
          setPendingProviders(null);
          setPendingProviderModels(null);
          toast.custom(
            createElement(AppToast, {
              title: "Gateway providers saved",
              description: "Instance is being configured in the background.",
              status: "success",
              toastId,
            }),
            { id: toastId, duration: 4000 },
          );
        },
        onError: (err: unknown) => {
          const message =
            err instanceof Error ? err.message : "Unknown error";
          toast.custom(
            createElement(AppToast, {
              title: "Failed to save providers",
              description: message,
              status: "error",
              toastId,
            }),
            { id: toastId, duration: 5000 },
          );
        },
      },
    );
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
          <StatusBadge status={instance.status} tooltip={buildSSHTooltip(sshStatus.data)} />
        </div>
        <ActionButtons
          instance={instance}
          onStart={(id) => startMutation.mutate(id)}
          onStop={(id) => stopMutation.mutate({ id, displayName: instance.display_name })}
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
                { label: "Display Name", value: instance.display_name },
                {
                  label: "Agent Image",
                  value: instance.live_image_info
                    ? instance.live_image_info
                    : instance.has_image_override
                      ? instance.container_image ?? ""
                      : "Default",
                },
                { label: "Instance Name", value: instance.name },
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
                { label: "Storage (Home)", value: instance.storage_home },
                {
                  label: "VNC Resolution",
                  value: instance.has_resolution_override
                    ? instance.vnc_resolution ?? ""
                    : "Default",
                },
                {
                  label: "Timezone",
                  value: instance.has_timezone_override
                    ? instance.timezone ?? ""
                    : "Default",
                },
                {
                  label: "User-Agent",
                  value: instance.has_user_agent_override
                    ? instance.user_agent ?? ""
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

          {/* LLM Gateway Providers (admin only) */}
          {isAdmin && (
            <div className="bg-white rounded-lg border border-gray-200 p-6">
              <div className="flex items-center justify-between mb-4">
                <div>
                  <h3 className="text-sm font-medium text-gray-900">Model Providers</h3>
                  <p className="text-xs text-gray-500 mt-0.5">
                    Providers routed through the internal LLM gateway. Configured automatically in the container via SSH.
                  </p>
                </div>
                <button
                  type="button"
                  onClick={() => {
                    if (editingGatewayProviders) {
                      setPendingProviders(null);
                      setPendingProviderModels(null);
                    } else {
                      setPendingProviders(instance.enabled_providers ?? []);
                      const initialModels: Record<number, string[]> = {};
                      for (const p of allProviders) {
                        const prefix = `${p.key}/`;
                        initialModels[p.id] = (instance.models.extra ?? [])
                          .filter((m) => m.startsWith(prefix))
                          .map((m) => m.slice(prefix.length));
                      }
                      setPendingProviderModels(initialModels);
                    }
                    setEditingGatewayProviders(!editingGatewayProviders);
                  }}
                  className="text-xs text-blue-600 hover:text-blue-800"
                >
                  {editingGatewayProviders ? "Cancel" : "Edit"}
                </button>
              </div>

              {editingGatewayProviders ? (
                <div className="space-y-4">
                  {allProviders.length === 0 ? (
                    <p className="text-sm text-gray-400 italic">No providers defined. Add providers in Settings first.</p>
                  ) : (
                    <div className="divide-y divide-gray-100">
                      {allProviders.map((p) => {
                        const enabled = (pendingProviders ?? []).includes(p.id);
                        const selectedModels = pendingProviderModels?.[p.id] ?? [];
                        const catalog = MODEL_CATALOG.find((c) => c.key === p.provider);
                        const iconKey = catalog?.lobeIconKey;
                        return (
                          <div key={p.id} className="py-3 first:pt-0 last:pb-0">
                            <label className="flex items-center gap-3 cursor-pointer">
                              <input
                                type="checkbox"
                                checked={enabled}
                                onChange={() => {
                                  setPendingProviders((prev) => {
                                    const current = prev ?? [];
                                    return enabled ? current.filter((id) => id !== p.id) : [...current, p.id];
                                  });
                                  if (enabled) {
                                    setPendingProviderModels((prev) => {
                                      const next = { ...(prev ?? {}) };
                                      delete next[p.id];
                                      return next;
                                    });
                                  }
                                }}
                                className="rounded border-gray-300"
                              />
                              {iconKey ? (
                                <ProviderIcon provider={iconKey} size={18} />
                              ) : (
                                <span className="w-4 h-4 rounded-full bg-gray-200 flex items-center justify-center text-xs font-medium text-gray-500 shrink-0">
                                  {p.name[0].toUpperCase()}
                                </span>
                              )}
                              <span className="text-sm text-gray-900">{p.name}</span>
                            </label>
                            {enabled && catalog && !catalog.dynamic && catalog.models.length > 0 && (
                              <div className="ml-7 mt-2 grid grid-cols-2 gap-x-6 gap-y-1">
                                {catalog.models.map((m) => (
                                  <label key={m.id} className="flex items-center gap-2 cursor-pointer">
                                    <input
                                      type="checkbox"
                                      checked={selectedModels.includes(m.id)}
                                      onChange={() => {
                                        setPendingProviderModels((prev) => {
                                          const current = prev?.[p.id] ?? [];
                                          const next = current.includes(m.id)
                                            ? current.filter((x) => x !== m.id)
                                            : [...current, m.id];
                                          return { ...(prev ?? {}), [p.id]: next };
                                        });
                                      }}
                                      className="rounded border-gray-300"
                                    />
                                    <span className="text-xs font-mono text-gray-700 truncate">{m.id}</span>
                                  </label>
                                ))}
                              </div>
                            )}
                            {enabled && (!catalog || catalog.dynamic) && (
                              <p className="ml-7 mt-1 text-xs text-gray-400 italic">Models determined dynamically.</p>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  )}
                  <div className="flex justify-end pt-2">
                    <button
                      onClick={handleSaveGatewayProviders}
                      disabled={updateMutation.isPending || pendingProviders === null}
                      className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
                    >
                      {updateMutation.isPending ? "Saving..." : "Save"}
                    </button>
                  </div>
                </div>
              ) : (
                <div>
                  {(instance.enabled_providers ?? []).length === 0 ? (
                    <p className="text-sm text-gray-400 italic">No providers enabled.</p>
                  ) : (
                    <div className="divide-y divide-gray-100">
                      {(instance.enabled_providers ?? []).map((pid) => {
                        const p = allProviders.find((x) => x.id === pid);
                        if (!p) return null;
                        const catalog = MODEL_CATALOG.find((c) => c.key === p.provider);
                        const iconKey = catalog?.lobeIconKey;
                        const prefix = `${p.key}/`;
                        const enabledModels = (instance.models.extra ?? [])
                          .filter((m) => m.startsWith(prefix))
                          .map((m) => m.slice(prefix.length));
                        return (
                          <div key={pid} className="py-3 first:pt-0 last:pb-0">
                            <div className="flex items-center gap-2">
                              {iconKey ? (
                                <ProviderIcon provider={iconKey} size={18} />
                              ) : (
                                <span className="w-4 h-4 rounded-full bg-gray-200 flex items-center justify-center text-xs font-medium text-gray-500 shrink-0">
                                  {p.name[0].toUpperCase()}
                                </span>
                              )}
                              <span className="text-sm font-medium text-gray-900">{p.name}</span>
                            </div>
                            {enabledModels.length > 0 && (
                              <div className="ml-6 mt-1 flex flex-wrap gap-1">
                                {enabledModels.map((m) => (
                                  <span key={m} className="px-2 py-0.5 bg-gray-100 text-gray-600 text-xs rounded font-mono">
                                    {m}
                                  </span>
                                ))}
                              </div>
                            )}
                          </div>
                        );
                      })}
                    </div>
                  )}
                </div>
              )}
            </div>
          )}

          {/* SSH Connection Status */}
          <SSHStatus
            status={sshStatus.data}
            isLoading={sshStatus.isLoading}
            isError={sshStatus.isError}
            onRefresh={() => sshStatus.refetch()}
            onTroubleshoot={instance.status === "running" && sshStatus.data ? () => setTroubleshootOpen(true) : undefined}
            onEvents={instance.status === "running" ? () => setEventsOpen(true) : undefined}
          />
          {troubleshootOpen && (
            <SSHTroubleshoot
              instanceId={instanceId}
              onClose={() => setTroubleshootOpen(false)}
            />
          )}
          {eventsOpen && (
            <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
              <div className="bg-white rounded-lg shadow-xl w-full max-w-2xl max-h-[80vh] flex flex-col">
                <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
                  <h3 className="text-sm font-medium text-gray-900">Connection Events</h3>
                  <button
                    onClick={() => setEventsOpen(false)}
                    className="text-gray-400 hover:text-gray-600 text-lg leading-none"
                  >
                    &times;
                  </button>
                </div>
                <div className="overflow-y-auto p-6">
                  <SSHEventLog
                    events={sshEvents.data?.events}
                    isLoading={sshEvents.isLoading}
                    isError={sshEvents.isError}
                  />
                </div>
              </div>
            </div>
          )}

          {/* Timezone Override */}
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <div className="flex items-center justify-between mb-4">
              <h3 className="text-sm font-medium text-gray-900">
                Timezone Override
              </h3>
              <button
                type="button"
                onClick={() => {
                  if (editingTimezone) {
                    setPendingTimezone(null);
                  } else {
                    setPendingTimezone(instance.timezone ?? "");
                  }
                  setEditingTimezone(!editingTimezone);
                }}
                className="text-xs text-blue-600 hover:text-blue-800"
              >
                {editingTimezone ? "Cancel" : "Edit"}
              </button>
            </div>

            {editingTimezone ? (
              <div className="space-y-3">
                <input
                  type="text"
                  value={pendingTimezone ?? ""}
                  onChange={(e) => setPendingTimezone(e.target.value)}
                  placeholder="e.g., America/New_York (empty = use global default)"
                  className="w-full text-sm border border-gray-300 rounded-md px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                />
                <p className="text-xs text-gray-500">
                  Leave empty to use the global default timezone. Changing timezone requires a container restart to take effect.
                </p>
                <div className="flex justify-end">
                  <button
                    onClick={handleSaveTimezone}
                    disabled={updateMutation.isPending || pendingTimezone === null}
                    className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
                  >
                    {updateMutation.isPending ? "Saving..." : "Save"}
                  </button>
                </div>
              </div>
            ) : (
              <p className="text-sm text-gray-500">
                {instance.has_timezone_override
                  ? instance.timezone
                  : "Using global default"}
              </p>
            )}
          </div>

          {/* User-Agent Override */}
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <div className="flex items-center justify-between mb-4">
              <h3 className="text-sm font-medium text-gray-900">
                User-Agent Override
              </h3>
              <button
                type="button"
                onClick={() => {
                  if (editingUserAgent) {
                    setPendingUserAgent(null);
                  } else {
                    setPendingUserAgent(instance.user_agent ?? "");
                  }
                  setEditingUserAgent(!editingUserAgent);
                }}
                className="text-xs text-blue-600 hover:text-blue-800"
              >
                {editingUserAgent ? "Cancel" : "Edit"}
              </button>
            </div>

            {editingUserAgent ? (
              <div className="space-y-3">
                <input
                  type="text"
                  value={pendingUserAgent ?? ""}
                  onChange={(e) => setPendingUserAgent(e.target.value)}
                  placeholder="Leave empty to use global default or browser built-in"
                  className="w-full text-sm border border-gray-300 rounded-md px-3 py-2 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-blue-500"
                />
                <p className="text-xs text-gray-500">
                  Custom User-Agent string for Chromium. Leave empty to use the global default (or browser built-in if no global default is set). Changing requires a container restart to take effect.
                </p>
                <div className="flex justify-end">
                  <button
                    onClick={handleSaveUserAgent}
                    disabled={updateMutation.isPending || pendingUserAgent === null}
                    className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
                  >
                    {updateMutation.isPending ? "Saving..." : "Save"}
                  </button>
                </div>
              </div>
            ) : (
              <p className="text-sm text-gray-500">
                {instance.has_user_agent_override
                  ? instance.user_agent
                  : "Using global default"}
              </p>
            )}
          </div>

          {/* Extra Models */}
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <div className="flex items-center justify-between mb-4">
              <div>
                <h3 className="text-sm font-medium text-gray-900">Extra Models</h3>
                <p className="text-xs text-gray-500 mt-0.5">
                  Additional models added on top of the global defaults for this instance.
                </p>
              </div>
              <button
                type="button"
                onClick={() => {
                  if (editingExtraModels) {
                    setEditingExtraModels(false);
                    setPendingExtraModels(null);
                  } else {
                    setEditingExtraModels(true);
                    setPendingExtraModels(instance.models.extra ?? []);
                  }
                }}
                className="text-xs text-blue-600 hover:text-blue-800"
              >
                {editingExtraModels ? "Cancel" : "Edit"}
              </button>
            </div>

            {editingExtraModels ? (
              <div className="space-y-3">
                <ModelListEditor
                  models={pendingExtraModels ?? []}
                  onChange={setPendingExtraModels}
                  showCatalog={true}
                />
                <div className="flex justify-end">
                  <button
                    onClick={handleSaveExtraModels}
                    disabled={updateMutation.isPending || pendingExtraModels === null}
                    className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
                  >
                    {updateMutation.isPending ? "Saving..." : "Save"}
                  </button>
                </div>
              </div>
            ) : (
              <div>
                {(instance.models.extra ?? []).length === 0 ? (
                  <p className="text-sm text-gray-400 italic">No extra models configured.</p>
                ) : (
                  <div className="flex flex-wrap gap-2">
                    {(instance.models.extra ?? []).map((m) => (
                      <span
                        key={m}
                        className="inline-flex items-center px-2.5 py-1 bg-blue-50 text-blue-700 text-sm rounded-md border border-blue-200 font-mono"
                      >
                        {m}
                      </span>
                    ))}
                  </div>
                )}
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
                  containerRef={desktopHook.containerRef}
                  reconnect={desktopHook.reconnect}
                  copyFromRemote={desktopHook.copyFromRemote}
                  pasteToRemote={desktopHook.pasteToRemote}
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
