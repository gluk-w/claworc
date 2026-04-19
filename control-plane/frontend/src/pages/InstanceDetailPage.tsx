import { useState, useEffect, useRef, createElement } from "react";
import { Link, useParams, useNavigate, useLocation } from "react-router-dom";
import { AlertTriangle, X, Maximize, ExternalLink, Plus } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import StatusBadge from "@/components/StatusBadge";
import ActionButtons from "@/components/ActionButtons";
import MonacoConfigEditor from "@/components/MonacoConfigEditor";
import LogViewer from "@/components/LogViewer";
import TerminalPanel from "@/components/TerminalPanel";
import VncPanel from "@/components/VncPanel";
import ChatPanel from "@/components/ChatPanel";
import FileBrowser from "@/components/FileBrowser";
import EditInput from "@/components/EditInput";
import { useInstanceBackups } from "@/hooks/useBackups";
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
  useInstanceStats,
  useUpdateInstanceImage,
} from "@/hooks/useInstances";
import { useProviders } from "@/hooks/useProviders";
import { useQueries, useQueryClient } from "@tanstack/react-query";
import { fetchCatalogProviderDetail } from "@/api/llm";
import type { CatalogProviderDetail } from "@/api/llm";
import ProviderIcon from "@/components/ProviderIcon";
import ProviderModelSelector from "@/components/ProviderModelSelector";
import ProviderModal from "@/components/ProviderModal";
import EnvVarsEditor from "@/components/EnvVarsEditor";
import AppToast from "@/components/AppToast";
import toast from "react-hot-toast";
import { envVarRestartToast } from "@/utils/toast";
import { useSSHStatus, useSSHEvents } from "@/hooks/useSSHStatus";
import { useInstanceLogs } from "@/hooks/useInstanceLogs";
import { useTerminal } from "@/hooks/useTerminal";
import { useDesktop } from "@/hooks/useDesktop";
import { useChat } from "@/hooks/useChat";
import type { InstanceUpdatePayload } from "@/types/instance";
import { buildSSHTooltip } from "@/utils/sshTooltip";

type Tab = "chat" | "terminal" | "files" | "config" | "logs" | "settings";

export default function InstanceDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const instanceId = Number(id);

  const qc = useQueryClient();
  const { isAdmin } = useAuth();
  const { data: instance, isLoading } = useInstance(instanceId);
  const { data: allProviders = [] } = useProviders();

  // Fetch catalog model lists for all catalog providers (used in edit mode)
  const catalogKeys = [...new Set(allProviders.filter((p) => p.provider).map((p) => p.provider))];
  const catalogDetailResults = useQueries({
    queries: catalogKeys.map((key) => ({
      queryKey: ["catalog-provider", key],
      queryFn: () => fetchCatalogProviderDetail(key),
      staleTime: 5 * 60 * 1000,
    })),
  });
  const catalogDetailMap: Record<string, CatalogProviderDetail> = {};
  catalogKeys.forEach((key, i) => {
    if (catalogDetailResults[i]?.data) catalogDetailMap[key] = catalogDetailResults[i].data!;
  });

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
  const updateImageMutation = useUpdateInstanceImage();

  // Get initial tab from URL hash (supports #files:///path pattern)
  const getTabFromHash = (): Tab => {
    const hash = location.hash.slice(1); // Remove '#'
    if (hash === "terminal" || hash === "config" || hash === "logs" || hash === "settings") {
      return hash;
    }
    if (hash === "chat" || hash === "chrome") {
      return "chat";
    }
    if (hash === "overview") {
      return "settings";
    }
    if (hash === "files" || hash.startsWith("files://")) {
      return "files";
    }
    return "chat";
  };

  const getFilesPathFromHash = (): string => {
    const hash = location.hash.slice(1);
    if (hash.startsWith("files://")) {
      const rest = hash.slice("files://".length);
      return rest ? `/${rest}` : "/";
    }
    return "/";
  };

  const [activeTab, setActiveTab] = useState<Tab>(getTabFromHash());
  const { data: stats } = useInstanceStats(instanceId, activeTab === "settings");
  const [editedConfig, setEditedConfig] = useState<string | null>(null);
  // Terminal/Chat are mounted once the user first visits the tab, then stay mounted
  const [terminalActivated, setTerminalActivated] = useState(getTabFromHash() === "terminal");
  const [chatActivated, setChatActivated] = useState(getTabFromHash() === "chat");
  const [chatViewMode, setChatViewMode] = useState<"chat-browser" | "chat-only">("chat-browser");
  const chatContainerRef = useRef<HTMLDivElement>(null);

  // SSH troubleshoot dialog
  const [troubleshootOpen, setTroubleshootOpen] = useState(false);
  // SSH events modal
  const [eventsOpen, setEventsOpen] = useState(false);

  // Timezone override editing state
  const [editingTimezone, setEditingTimezone] = useState(false);
  const [pendingTimezone, setPendingTimezone] = useState<string | null>(null);

  // User-Agent override editing state
  const [editingUserAgent, setEditingUserAgent] = useState(false);
  const [pendingUserAgent, setPendingUserAgent] = useState<string | null>(null);

  // Display name editing state
  const [editingDisplayName, setEditingDisplayName] = useState(false);
  const [pendingDisplayName, setPendingDisplayName] = useState<string | null>(null);

  // Resource limits editing state
  const [editingResources, setEditingResources] = useState(false);
  const [pendingCPURequest, setPendingCPURequest] = useState("");
  const [pendingCPULimit, setPendingCPULimit] = useState("");
  const [pendingMemoryRequest, setPendingMemoryRequest] = useState("");
  const [pendingMemoryLimit, setPendingMemoryLimit] = useState("");

  // VNC resolution editing state
  const [editingResolution, setEditingResolution] = useState(false);
  const [pendingResolution, setPendingResolution] = useState<string | null>(null);

  // Gateway providers editing state
  const [editingGatewayProviders, setEditingGatewayProviders] = useState(false);
  const [pendingProviders, setPendingProviders] = useState<number[] | null>(null);
  const [pendingProviderModels, setPendingProviderModels] = useState<Record<number, string[]> | null>(null);
  const [pendingDefaultModel, setPendingDefaultModel] = useState<string>("");

  // Instance provider modal state
  const [instanceProviderModalOpen, setInstanceProviderModalOpen] = useState(false);
  const [editingInstanceProvider, setEditingInstanceProvider] = useState<import("@/types/instance").LLMProvider | undefined>(undefined);


  // Update tab when hash changes
  useEffect(() => {
    const tab = getTabFromHash();
    setActiveTab(tab);
    if (tab === "terminal") setTerminalActivated(true);
    if (tab === "chat") setChatActivated(true);
    if (tab === "config") qc.invalidateQueries({ queryKey: ["instances", instanceId, "config"] });
  }, [location.hash]);

  const handleFilesPathChange = (path: string) => {
    const hash = path === "/" ? "files" : `files://${path.replace(/^\//, "")}`;
    navigate(`#${hash}`, { replace: true });
  };

  const chatInitSentRef = useRef(false);

  const logsHook = useInstanceLogs(instanceId, activeTab === "logs");
  const termHook = useTerminal(instanceId, terminalActivated && instance?.status === "running");
  const desktopHook = useDesktop(instanceId, chatActivated && chatViewMode === "chat-browser" && instance?.status === "running");
  const chatHook = useChat(instanceId, chatActivated && instance?.status === "running");

  // Auto-send disabled — user sends first message manually
  // useEffect(() => { ... }, []);

  // Reset init flag when switching away from chat tab so re-entering starts fresh
  useEffect(() => {
    if (activeTab !== "chat") {
      chatInitSentRef.current = false;
    }
  }, [activeTab]);

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
          const axiosMsg = (err as any)?.response?.data?.error ?? (err as any)?.response?.data?.detail;
          const message = axiosMsg ?? (err instanceof Error ? err.message : "Unknown error");
          const hint = "Fix the JSON syntax in the editor and try again.";
          toast.custom(
            createElement(AppToast, { title: "Failed to save settings", description: `${message} — ${hint}`, status: "error", toastId }),
            { id: toastId, duration: 5000 },
          );
        },
      },
    );
  };

  const handleResetConfig = () => {
    setEditedConfig(null);
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

  const handleSaveDisplayName = () => {
    if (!pendingDisplayName?.trim()) return;
    updateMutation.mutate(
      { id: instanceId, payload: { display_name: pendingDisplayName.trim() } },
      {
        onSuccess: () => {
          setEditingDisplayName(false);
          setPendingDisplayName(null);
        },
      },
    );
  };

  const isValidCPU = (v: string) => /^\d+m$/.test(v) || /^\d+(\.\d+)?$/.test(v);
  const isValidMemory = (v: string) => /^\d+(Mi|Gi)$/.test(v);
  const cpuToMillis = (v: string) => v.endsWith("m") ? parseInt(v) : parseFloat(v) * 1000;
  const memToBytes = (v: string) => v.endsWith("Gi") ? parseInt(v) * 1024 : parseInt(v);
  const isValidResolution = (v: string) => v === "" || /^\d+x\d+$/.test(v);

  const resourcesValid =
    isValidCPU(pendingCPURequest) &&
    isValidCPU(pendingCPULimit) &&
    isValidMemory(pendingMemoryRequest) &&
    isValidMemory(pendingMemoryLimit) &&
    cpuToMillis(pendingCPURequest) <= cpuToMillis(pendingCPULimit) &&
    memToBytes(pendingMemoryRequest) <= memToBytes(pendingMemoryLimit);

  const handleSaveResources = () => {
    if (!resourcesValid) return;
    updateMutation.mutate(
      {
        id: instanceId,
        payload: {
          cpu_request: pendingCPURequest,
          cpu_limit: pendingCPULimit,
          memory_request: pendingMemoryRequest,
          memory_limit: pendingMemoryLimit,
        },
      },
      {
        onSuccess: () => {
          setEditingResources(false);
        },
      },
    );
  };

  const handleSaveResolution = () => {
    if (pendingResolution === null) return;
    updateMutation.mutate(
      { id: instanceId, payload: { vnc_resolution: pendingResolution } },
      {
        onSuccess: () => {
          setEditingResolution(false);
          setPendingResolution(null);
        },
      },
    );
  };

  const handleSaveEnvVars = async (delta: { set: Record<string, string>; unset: string[] }) => {
    const payload: InstanceUpdatePayload = {};
    if (Object.keys(delta.set).length > 0) payload.env_vars_set = delta.set;
    if (delta.unset.length > 0) payload.env_vars_unset = delta.unset;
    const updated = await updateMutation.mutateAsync({ id: instanceId, payload });
    if (updated?.restarting && instance) {
      envVarRestartToast(instanceId, instance.display_name);
    }
  };

  const handleUpdateImage = () => {
    const toastId = "image-update";
    toast.custom(
      createElement(AppToast, { title: "Updating image...", description: "Pulling latest and restarting", status: "loading", toastId }),
      { id: toastId, duration: Infinity },
    );
    updateImageMutation.mutate(instanceId, {
      onSuccess: () => {
        toast.custom(
          createElement(AppToast, { title: "Image update started", description: "Instance is restarting with the new image", status: "success", toastId }),
          { id: toastId, duration: 3000 },
        );
      },
      onError: (err: unknown) => {
        const axiosMsg = (err as any)?.response?.data?.error ?? (err as any)?.response?.data?.detail;
        const message = axiosMsg ?? (err instanceof Error ? err.message : "Unknown error");
        toast.custom(
          createElement(AppToast, { title: "Image update failed", description: message, status: "error", toastId }),
          { id: toastId, duration: 5000 },
        );
      },
    });
  };

  const formatBytes = (bytes: number): string => {
    if (bytes >= 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)}Gi`;
    if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(0)}Mi`;
    return `${bytes}B`;
  };

  const renderProviderCard = (p: import("@/types/instance").LLMProvider, isInstanceProvider: boolean) => {
    const iconKey = p.provider ? catalogDetailMap[p.provider]?.icon_key ?? undefined : undefined;
    const displayModels: string[] = (p.models ?? []).length > 0
      ? (p.models ?? []).map((m) => m.id)
      : (instance.models.extra ?? [])
          .filter((m) => m.startsWith(`${p.key}/`))
          .map((m) => m.slice(`${p.key}/`.length));
    return (
      <div key={`${isInstanceProvider ? "inst" : "global"}-${p.id}`} className="bg-white rounded-lg border border-gray-200 px-4 py-3">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-lg bg-gray-100 flex items-center justify-center shrink-0">
            {iconKey ? (
              <ProviderIcon provider={iconKey} size={18} />
            ) : (
              <span className="text-xs font-semibold text-gray-500">{p.name[0].toUpperCase()}</span>
            )}
          </div>
          <span className="text-sm font-semibold text-gray-900">{p.name}</span>
          {isInstanceProvider && (
            <span className="px-1.5 py-0.5 text-xs font-medium text-amber-700 bg-amber-50 border border-amber-200 rounded-full">Instance</span>
          )}
          {p.api_type && p.api_type !== "openai-completions" && (
            <span className="px-1.5 py-0.5 text-xs font-mono text-gray-400 bg-gray-100 rounded">{p.api_type}</span>
          )}
          {isInstanceProvider && (
            <button
              type="button"
              onClick={() => { setEditingInstanceProvider(p); setInstanceProviderModalOpen(true); }}
              className="ml-auto text-xs text-gray-400 hover:text-gray-600"
            >
              Edit
            </button>
          )}
        </div>
        {displayModels.length > 0 && (
          <div className="mt-2 flex flex-wrap gap-1">
            {displayModels.map((m) => {
              const isPrimary = instance.default_model === `${p.key}/${m}`;
              return (
                <span
                  key={m}
                  className={`px-2 py-0.5 text-xs rounded font-mono ${isPrimary ? "bg-blue-100 text-blue-700 ring-1 ring-blue-300" : "bg-gray-100 text-gray-600"}`}
                >
                  {m}{isPrimary && <span className="ml-1 font-sans not-italic">★</span>}
                </span>
              );
            })}
          </div>
        )}
      </div>
    );
  };

  const handleSaveGatewayProviders = () => {
    if (pendingProviders === null) return;

    // Collect models from pendingProviderModels with provider prefix.
    // Skip providers that define their own models via the API — those are pushed
    // to the container directly from the provider definition, not via models.extra.
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
          default_model: pendingDefaultModel,
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
          setPendingDefaultModel("");
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
    { key: "chat", label: "Chat" },
    { key: "terminal", label: "Terminal" },
    { key: "files", label: "Files" },
    { key: "config", label: "Config" },
    { key: "logs", label: "Logs" },
    { key: "settings", label: "Settings" },
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
            <Link
              key={tab.key}
              to={{ hash: tab.key }}
              replace
              className={`pb-3 text-sm font-medium border-b-2 ${activeTab === tab.key
                  ? "border-blue-600 text-blue-600"
                  : "border-transparent text-gray-500 hover:text-gray-700"
                }`}
            >
              {tab.label}
            </Link>
          ))}
        </nav>
      </div>

      {activeTab === "settings" && (
        <div className="space-y-8">
          {/* Instance Details — unified card */}
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <h3 className="text-sm font-medium text-gray-900 mb-4">Instance Details</h3>
            <div className="grid grid-cols-2 gap-y-4 gap-x-8">
              {/* Display Name — admin editable */}
              <div>
                <dt className="text-xs text-gray-500">Display Name</dt>
                {isAdmin && editingDisplayName ? (
                  <dd className="mt-0.5 flex gap-2">
                    <EditInput
                      type="text"
                      value={pendingDisplayName ?? ""}
                      onChange={(e) => setPendingDisplayName(e.target.value)}
                      onSave={handleSaveDisplayName}
                      onCancel={() => { setEditingDisplayName(false); setPendingDisplayName(null); }}
                      className="flex-1 min-w-0 text-sm border border-gray-300 rounded-md px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-blue-500"
                    />
                    <button onClick={handleSaveDisplayName} disabled={updateMutation.isPending || !pendingDisplayName?.trim()} className="px-3 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed shrink-0">{updateMutation.isPending ? "Saving..." : "Save"}</button>
                    <button type="button" onClick={() => { setEditingDisplayName(false); setPendingDisplayName(null); }} className="text-xs text-blue-600 hover:text-blue-800 shrink-0">Cancel</button>
                  </dd>
                ) : (
                  <dd className="text-sm text-gray-900 mt-0.5">
                    {instance.display_name}
                    {isAdmin && (
                      <button type="button" onClick={() => { setPendingDisplayName(instance.display_name); setEditingDisplayName(true); }} className="ml-2 text-xs text-blue-600 hover:text-blue-800">Edit</button>
                    )}
                  </dd>
                )}
              </div>

              {/* Agent Image */}
              <div>
                <dt className="text-xs text-gray-500">Agent Image</dt>
                <dd className="text-sm text-gray-900 mt-0.5 break-all">
                  {instance.live_image_info
                    ? instance.live_image_info
                    : instance.has_image_override
                      ? instance.container_image ?? ""
                      : "Default"}
                  {isAdmin && instance.status === "running" && (() => {
                    const img = instance.live_image_info ?? instance.container_image ?? "";
                    return img && !img.includes("@sha256:");
                  })() && (
                    <button
                      type="button"
                      onClick={handleUpdateImage}
                      disabled={updateImageMutation.isPending || instance.status !== "running"}
                      className="ml-2 text-xs text-blue-600 hover:text-blue-800 disabled:opacity-50 disabled:cursor-not-allowed"
                    >
                      {updateImageMutation.isPending ? "Updating..." : "Update"}
                    </button>
                  )}
                </dd>
              </div>

              {/* Created / Updated */}
              <div>
                <dt className="text-xs text-gray-500">Created</dt>
                <dd className="text-sm text-gray-900 mt-0.5">{instance.created_at}</dd>
              </div>
              <div>
                <dt className="text-xs text-gray-500">Updated</dt>
                <dd className="text-sm text-gray-900 mt-0.5">{instance.updated_at}</dd>
              </div>

              {/* VNC Resolution — admin editable */}
              <div>
                <dt className="text-xs text-gray-500">VNC Resolution</dt>
                {isAdmin && editingResolution ? (
                  <dd className="mt-0.5">
                    <div className="flex gap-2">
                      <EditInput
                        type="text"
                        value={pendingResolution ?? ""}
                        onChange={(e) => setPendingResolution(e.target.value)}
                        onSave={handleSaveResolution}
                        onCancel={() => { setEditingResolution(false); setPendingResolution(null); }}
                        placeholder="e.g., 1920x1080 (empty = default)"
                        className={`flex-1 min-w-0 text-sm border rounded-md px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-blue-500 ${pendingResolution && !isValidResolution(pendingResolution) ? "border-red-300" : "border-gray-300"}`}
                      />
                      <button onClick={handleSaveResolution} disabled={updateMutation.isPending || pendingResolution === null || (pendingResolution !== "" && !isValidResolution(pendingResolution))} className="px-3 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed shrink-0">{updateMutation.isPending ? "Saving..." : "Save"}</button>
                      <button type="button" onClick={() => { setEditingResolution(false); setPendingResolution(null); }} className="text-xs text-blue-600 hover:text-blue-800 shrink-0">Cancel</button>
                    </div>
                    <p className="text-xs text-gray-400 mt-1">WIDTHxHEIGHT. Empty = default. Requires restart.</p>
                  </dd>
                ) : (
                  <dd className="text-sm text-gray-900 mt-0.5">
                    {instance.has_resolution_override ? instance.vnc_resolution : "Default"}
                    {isAdmin && (
                      <button type="button" onClick={() => { setPendingResolution(instance.vnc_resolution ?? ""); setEditingResolution(true); }} className="ml-2 text-xs text-blue-600 hover:text-blue-800">Edit</button>
                    )}
                  </dd>
                )}
              </div>

              {/* Timezone — editable by all */}
              <div>
                <dt className="text-xs text-gray-500">Timezone</dt>
                {editingTimezone ? (
                  <dd className="mt-0.5">
                    <div className="flex gap-2">
                      <EditInput
                        type="text"
                        value={pendingTimezone ?? ""}
                        onChange={(e) => setPendingTimezone(e.target.value)}
                        onSave={handleSaveTimezone}
                        onCancel={() => { setEditingTimezone(false); setPendingTimezone(null); }}
                        placeholder="e.g., America/New_York (empty = default)"
                        className="flex-1 min-w-0 text-sm border border-gray-300 rounded-md px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-blue-500"
                      />
                      <button onClick={handleSaveTimezone} disabled={updateMutation.isPending || pendingTimezone === null} className="px-3 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed shrink-0">{updateMutation.isPending ? "Saving..." : "Save"}</button>
                      <button type="button" onClick={() => { setEditingTimezone(false); setPendingTimezone(null); }} className="text-xs text-blue-600 hover:text-blue-800 shrink-0">Cancel</button>
                    </div>
                    <p className="text-xs text-gray-400 mt-1">IANA timezone. Empty = global default. Requires restart.</p>
                  </dd>
                ) : (
                  <dd className="text-sm text-gray-900 mt-0.5">
                    {instance.has_timezone_override ? instance.timezone : "Default"}
                    <button type="button" onClick={() => { setPendingTimezone(instance.timezone ?? ""); setEditingTimezone(true); }} className="ml-2 text-xs text-blue-600 hover:text-blue-800">Edit</button>
                  </dd>
                )}
              </div>

              {/* User-Agent — editable by all, single column */}
              <div>
                <dt className="text-xs text-gray-500">User-Agent</dt>
                {editingUserAgent ? (
                  <dd className="mt-0.5">
                    <div className="flex gap-2">
                      <EditInput
                        type="text"
                        value={pendingUserAgent ?? ""}
                        onChange={(e) => setPendingUserAgent(e.target.value)}
                        onSave={handleSaveUserAgent}
                        onCancel={() => { setEditingUserAgent(false); setPendingUserAgent(null); }}
                        placeholder="Empty = default"
                        className="flex-1 min-w-0 text-sm border border-gray-300 rounded-md px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-blue-500"
                      />
                      <button onClick={handleSaveUserAgent} disabled={updateMutation.isPending || pendingUserAgent === null} className="px-3 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed shrink-0">{updateMutation.isPending ? "Saving..." : "Save"}</button>
                      <button type="button" onClick={() => { setEditingUserAgent(false); setPendingUserAgent(null); }} className="text-xs text-blue-600 hover:text-blue-800 shrink-0">Cancel</button>
                    </div>
                    <p className="text-xs text-gray-400 mt-1">Custom Chromium User-Agent. Requires restart.</p>
                  </dd>
                ) : (
                  <dd className="text-sm text-gray-900 mt-0.5">
                    {instance.has_user_agent_override ? instance.user_agent : "Default"}
                    <button type="button" onClick={() => { setPendingUserAgent(instance.user_agent ?? ""); setEditingUserAgent(true); }} className="ml-2 text-xs text-blue-600 hover:text-blue-800">Edit</button>
                  </dd>
                )}
              </div>
            </div>
          </div>

          {/* Resources card */}
          <div className="bg-white rounded-lg border border-gray-200 p-6">
            <div className="flex items-center justify-between mb-4">
              <h3 className="text-sm font-medium text-gray-900">Resources</h3>
              {isAdmin && (
                <button
                  type="button"
                  onClick={() => {
                    if (editingResources) {
                      setEditingResources(false);
                    } else {
                      setPendingCPURequest(instance.cpu_request);
                      setPendingCPULimit(instance.cpu_limit);
                      setPendingMemoryRequest(instance.memory_request);
                      setPendingMemoryLimit(instance.memory_limit);
                      setEditingResources(true);
                    }
                  }}
                  className="text-xs text-blue-600 hover:text-blue-800"
                >
                  {editingResources ? "Cancel" : "Edit"}
                </button>
              )}
            </div>
            {editingResources ? (
              <div className="space-y-4">
                <div className="grid grid-cols-2 gap-4">
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">CPU Request</label>
                    <EditInput type="text" value={pendingCPURequest} onChange={(e) => setPendingCPURequest(e.target.value)} placeholder="e.g., 500m"
                      onSave={handleSaveResources} onCancel={() => setEditingResources(false)}
                      className={`w-full px-3 py-1.5 border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${pendingCPURequest && !isValidCPU(pendingCPURequest) ? "border-red-300" : "border-gray-300"}`} />
                  </div>
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">CPU Limit</label>
                    <EditInput type="text" value={pendingCPULimit} onChange={(e) => setPendingCPULimit(e.target.value)} placeholder="e.g., 2000m"
                      onSave={handleSaveResources} onCancel={() => setEditingResources(false)}
                      className={`w-full px-3 py-1.5 border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${pendingCPULimit && !isValidCPU(pendingCPULimit) ? "border-red-300" : "border-gray-300"}`} />
                  </div>
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">Memory Request</label>
                    <EditInput type="text" value={pendingMemoryRequest} onChange={(e) => setPendingMemoryRequest(e.target.value)} placeholder="e.g., 1Gi"
                      onSave={handleSaveResources} onCancel={() => setEditingResources(false)}
                      className={`w-full px-3 py-1.5 border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${pendingMemoryRequest && !isValidMemory(pendingMemoryRequest) ? "border-red-300" : "border-gray-300"}`} />
                  </div>
                  <div>
                    <label className="block text-xs text-gray-500 mb-1">Memory Limit</label>
                    <EditInput type="text" value={pendingMemoryLimit} onChange={(e) => setPendingMemoryLimit(e.target.value)} placeholder="e.g., 4Gi"
                      onSave={handleSaveResources} onCancel={() => setEditingResources(false)}
                      className={`w-full px-3 py-1.5 border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${pendingMemoryLimit && !isValidMemory(pendingMemoryLimit) ? "border-red-300" : "border-gray-300"}`} />
                  </div>
                </div>
                <p className="text-xs text-gray-400">CPU in millicores (e.g., 500m). Memory in Mi or Gi (e.g., 1Gi). Request must not exceed limit.</p>
                {pendingCPURequest && pendingCPULimit && isValidCPU(pendingCPURequest) && isValidCPU(pendingCPULimit) && cpuToMillis(pendingCPURequest) > cpuToMillis(pendingCPULimit) && (
                  <p className="text-xs text-red-600">CPU request cannot exceed CPU limit.</p>
                )}
                {pendingMemoryRequest && pendingMemoryLimit && isValidMemory(pendingMemoryRequest) && isValidMemory(pendingMemoryLimit) && memToBytes(pendingMemoryRequest) > memToBytes(pendingMemoryLimit) && (
                  <p className="text-xs text-red-600">Memory request cannot exceed memory limit.</p>
                )}
                <div className="flex justify-end pt-2">
                  <button onClick={handleSaveResources} disabled={updateMutation.isPending || !resourcesValid} className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed">{updateMutation.isPending ? "Saving..." : "Save"}</button>
                </div>
              </div>
            ) : (
              <div className="grid grid-cols-2 gap-y-4 gap-x-8">
                <div>
                  <dt className="text-xs text-gray-500">CPU Request</dt>
                  <dd className="text-sm text-gray-900 mt-0.5">{instance.cpu_request}</dd>
                </div>
                <div>
                  <dt className="text-xs text-gray-500">CPU Limit</dt>
                  <dd className="text-sm text-gray-900 mt-0.5">
                    {instance.cpu_limit}
                    {stats && (
                      <span className="ml-2 text-xs text-gray-400">
                        (using {stats.cpu_usage_millicores}m / {stats.cpu_usage_percent.toFixed(0)}%)
                      </span>
                    )}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs text-gray-500">Memory Request</dt>
                  <dd className="text-sm text-gray-900 mt-0.5">{instance.memory_request}</dd>
                </div>
                <div>
                  <dt className="text-xs text-gray-500">Memory Limit</dt>
                  <dd className="text-sm text-gray-900 mt-0.5">
                    {instance.memory_limit}
                    {stats && (
                      <span className="ml-2 text-xs text-gray-400">
                        (using {formatBytes(stats.memory_usage_bytes)} / {stats.memory_limit_bytes > 0 ? ((stats.memory_usage_bytes / stats.memory_limit_bytes) * 100).toFixed(0) : "?"}%)
                      </span>
                    )}
                  </dd>
                </div>
                <div>
                  <dt className="text-xs text-gray-500">Storage (Homebrew)</dt>
                  <dd className="text-sm text-gray-900 mt-0.5">
                    {instance.storage_homebrew}
                    <span className="ml-1 text-xs text-gray-400">(set at creation)</span>
                  </dd>
                </div>
                <div>
                  <dt className="text-xs text-gray-500">Storage (Home)</dt>
                  <dd className="text-sm text-gray-900 mt-0.5">
                    {instance.storage_home}
                    <span className="ml-1 text-xs text-gray-400">(set at creation)</span>
                  </dd>
                </div>
              </div>
            )}
          </div>

          {/* Environment Variables */}
          <EnvVarsEditor
            values={instance.env_vars ?? {}}
            title="Environment Variables"
            description="Per-instance values override globals with the same name. Values are encrypted at rest. Saving restarts this instance so the change takes effect immediately."
            onSave={handleSaveEnvVars}
            isSaving={updateMutation.isPending}
            emptyMessage="No instance-specific env vars. Globals from Settings apply."
          />

          {/* LLM Gateway Providers (admin only) */}
          {isAdmin && (
            <div className="bg-white rounded-lg border border-gray-200 p-6">
              <div className="flex items-center justify-between mb-4">
                <div>
                  <h3 className="text-sm font-medium text-gray-900">Enabled Models</h3>
                  <p className="text-xs text-gray-500 mt-0.5">
                    Pick among available model(s) for the agent.
                  </p>
                </div>
                <div className="flex items-center gap-3">
                  <button
                    type="button"
                    onClick={() => {
                      setEditingInstanceProvider(undefined);
                      setInstanceProviderModalOpen(true);
                    }}
                    className="inline-flex items-center gap-1 text-xs text-blue-600 hover:text-blue-800"
                  >
                    <Plus size={12} />
                    Add provider
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      if (editingGatewayProviders) {
                        setPendingProviders(null);
                        setPendingProviderModels(null);
                        setPendingDefaultModel("");
                      } else {
                        const allProvidersForEdit = [...allProviders, ...(instance.instance_providers ?? [])];
                        setPendingProviders(instance.enabled_providers ?? []);
                        const initialModels: Record<number, string[]> = {};
                        for (const p of allProvidersForEdit) {
                          const prefix = `${p.key}/`;
                          initialModels[p.id] = (instance.models.extra ?? [])
                            .filter((m) => m.startsWith(prefix))
                            .map((m) => m.slice(prefix.length));
                        }
                        setPendingProviderModels(initialModels);
                        setPendingDefaultModel(instance.default_model ?? "");
                      }
                      setEditingGatewayProviders(!editingGatewayProviders);
                    }}
                    className="text-xs text-blue-600 hover:text-blue-800"
                  >
                    {editingGatewayProviders ? "Cancel" : "Edit"}
                  </button>
                </div>
              </div>

              {editingGatewayProviders ? (
                <div className="space-y-4">
                  {allProviders.length === 0 ? (
                    <p className="text-sm text-gray-400 italic">No providers defined. Add providers in Settings first.</p>
                  ) : (
                    <ProviderModelSelector
                      providers={allProviders}
                      instanceProviders={instance.instance_providers ?? []}
                      catalogDetailMap={catalogDetailMap}
                      enabledProviders={pendingProviders ?? []}
                      providerModels={pendingProviderModels ?? {}}
                      defaultModel={pendingDefaultModel}
                      onUpdate={(newEnabled, newModels, newDefault) => {
                        setPendingProviders(newEnabled);
                        setPendingProviderModels(newModels);
                        setPendingDefaultModel(newDefault);
                      }}
                    />
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
                  {(instance.enabled_providers ?? []).length === 0 && (instance.instance_providers ?? []).length === 0 ? (
                    <p className="text-sm text-gray-400 italic">No providers enabled.</p>
                  ) : (
                    <div className="space-y-2">
                      {/* Global enabled providers */}
                      {(instance.enabled_providers ?? []).map((pid) => {
                        const p = allProviders.find((x) => x.id === pid);
                        if (!p) return null;
                        return renderProviderCard(p, false);
                      })}
                      {/* Instance-specific providers */}
                      {(instance.instance_providers ?? []).map((p) =>
                        renderProviderCard(p, true)
                      )}
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
          {/* Backups section (admin only) */}
          {isAdmin && (
            <BackupStatusCard instanceId={instanceId} instanceName={instance?.name || ""} />
          )}

          {eventsOpen && (
            <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
              <div className="bg-white rounded-lg shadow-xl w-full max-w-2xl max-h-[80vh] flex flex-col">
                <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
                  <h3 className="text-sm font-medium text-gray-900">Connection Events</h3>
                  <button
                    onClick={() => setEventsOpen(false)}
                    className="text-gray-400 hover:text-gray-600"
                  >
                    <X size={16} />
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

        </div>
      )}

      {chatActivated && (
        <div
          ref={chatContainerRef}
          className="bg-gray-900 rounded-lg border border-gray-700 overflow-hidden h-[calc(100vh-142px)] min-h-[400px] flex flex-col"
          style={activeTab !== "chat" ? { display: "none" } : undefined}
        >
          {instance.status === "running" ? (
            <>
              {/* Fullscreen / New Window bar */}
              <div className="flex items-center justify-end gap-2 px-3 py-1 bg-gray-800 border-b border-gray-700">
                <button
                  onClick={() => {
                    const popup = window.open(`/instances/${instanceId}/chat`, "_blank");
                    if (popup) {
                      const handler = (e: MessageEvent) => {
                        if (e.source === popup && e.data?.type === "chat-history-request" && e.data?.instanceId === instanceId) {
                          popup.postMessage({ type: "chat-history", messages: chatHook.messages }, window.location.origin);
                          window.removeEventListener("message", handler);
                        }
                      };
                      window.addEventListener("message", handler);
                    }
                  }}
                  className="flex items-center gap-1 px-1.5 py-1 text-xs text-gray-400 hover:text-white rounded"
                  title="Open in new window"
                >
                  <ExternalLink size={14} /> New Window
                </button>
                <button
                  onClick={() => {
                    if (document.fullscreenElement) {
                      document.exitFullscreen();
                    } else {
                      chatContainerRef.current?.requestFullscreen();
                    }
                  }}
                  className="flex items-center gap-1 px-1.5 py-1 text-xs text-gray-400 hover:text-white rounded"
                  title="Toggle fullscreen"
                >
                  <Maximize size={14} /> Full Screen
                </button>
              </div>
              <div className="flex flex-1 min-h-0">
                <div className={chatViewMode === "chat-browser" ? "w-[400px] flex-shrink-0 border-r border-gray-700 relative" : "flex-1 relative"}>
                  <ChatPanel
                    messages={chatHook.messages}
                    connectionState={chatHook.connectionState}
                    thinkingLabel={chatHook.thinkingLabel}
                    onSend={chatHook.sendMessage}
                    onStop={chatHook.stopResponse}
                    onNewChat={chatHook.newChat}
                    onReconnect={chatHook.reconnect}
                    viewMode={chatViewMode}
                    onViewModeChange={setChatViewMode}
                  />
                </div>
                {chatViewMode === "chat-browser" && (
                  <div className="flex-1 min-w-0 relative">
                    <VncPanel
                      instanceId={instanceId}
                      connectionState={desktopHook.connectionState}
                      containerRef={desktopHook.containerRef}
                      reconnect={desktopHook.reconnect}
                      copyFromRemote={desktopHook.copyFromRemote}
                      pasteToRemote={desktopHook.pasteToRemote}
                      showNewWindow={false}
                      showFullscreen={false}
                    />
                  </div>
                )}
              </div>
            </>
          ) : (
            <div className="flex items-center justify-center h-full w-full text-gray-500 text-sm">
              Instance must be running to use Chat.
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
      <ProviderModal
        open={instanceProviderModalOpen}
        mode={editingInstanceProvider ? "edit" : "create"}
        provider={editingInstanceProvider}
        instanceId={instanceId}
        existingKeys={[...allProviders.map((p) => p.key), ...(instance.instance_providers ?? []).map((p) => p.key)]}
        onClose={() => { setInstanceProviderModalOpen(false); setEditingInstanceProvider(undefined); }}
        onSaved={() => {}}
        onDeleted={() => {}}
      />
    </div>
  );
}

function BackupStatusCard({ instanceId, instanceName }: { instanceId: number; instanceName: string }) {
  const { data: backups = [] } = useInstanceBackups(instanceId);
  const completed = backups.filter((b) => b.status === "completed");
  const lastBackup = completed.length > 0 ? completed[0] : null;

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <h3 className="text-sm font-medium text-gray-900 mb-4">Backups</h3>
      <div className="space-y-2">
        <div className="flex items-center gap-2">
          <span className="text-xs text-gray-500">Status:</span>
          {lastBackup ? (
            <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-green-100 text-green-800">
              Backed up
            </span>
          ) : (
            <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-gray-100 text-gray-600">
              No backups
            </span>
          )}
        </div>
        {lastBackup && (
          <p className="text-xs text-gray-500">
            Last backup: {new Date(lastBackup.created_at).toLocaleString()}
          </p>
        )}
        <a
          href={`/backups?instance=${encodeURIComponent(instanceName)}`}
          className="text-xs text-blue-600 hover:text-blue-800"
        >
          View backups
        </a>
      </div>
    </div>
  );
}
