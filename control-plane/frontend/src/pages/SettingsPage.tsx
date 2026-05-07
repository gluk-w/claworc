import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import {
  AlertTriangle,
  Eye,
  EyeOff,
  Key,
  Pencil,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react";
import ProviderIcon from "@/components/ProviderIcon";
import ProviderModal from "@/components/ProviderModal";
import EnvVarsEditor from "@/components/EnvVarsEditor";
import StickyActionBar from "@/components/StickyActionBar";
import CreateTeamDialog from "@/components/CreateTeamDialog";
import TeamMembersPanel from "@/components/TeamMembersPanel";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSettings, useUpdateSettings } from "@/hooks/useSettings";
import { useProviders, useCatalogIconMap } from "@/hooks/useProviders";
import { fetchSSHFingerprint, rotateSSHKey } from "@/api/ssh";
import { syncAllProviders } from "@/api/llm";
import {
  fetchTeams,
  updateTeam,
  deleteTeam,
  type Team,
} from "@/api/teams";
import { useTeam } from "@/contexts/TeamContext";
import { successToast, errorToast } from "@/utils/toast";
import { validateResourceQuantities } from "@/utils/resourceValidation";
import type { LLMProvider } from "@/types/instance";
import type { Settings, SettingsUpdatePayload } from "@/types/settings";

type TabKey = "api-keys" | "environment" | "teams" | "misc";
const TAB_KEYS: TabKey[] = ["api-keys", "environment", "teams", "misc"];

function isTabKey(v: string | null): v is TabKey {
  return !!v && (TAB_KEYS as string[]).includes(v);
}

// formatExpiresIn formats a duration in milliseconds as e.g. "2 days",
// "3 hours 54 minutes", "3 minutes", or "less than a minute" / "expired".
function formatExpiresIn(ms: number): string {
  if (ms <= 0) return "expired";
  const totalMin = Math.floor(ms / 60000);
  if (totalMin < 1) return "less than a minute";
  const days = Math.floor(totalMin / (60 * 24));
  const hours = Math.floor((totalMin % (60 * 24)) / 60);
  const minutes = totalMin % 60;
  if (days >= 1) return `${days} ${days === 1 ? "day" : "days"}`;
  if (hours >= 1) {
    if (minutes === 0) return `${hours} ${hours === 1 ? "hour" : "hours"}`;
    return `${hours} ${hours === 1 ? "hour" : "hours"} ${minutes} ${minutes === 1 ? "minute" : "minutes"}`;
  }
  return `${minutes} ${minutes === 1 ? "minute" : "minutes"}`;
}

export default function SettingsPage() {
  const { data: settings, isLoading } = useSettings();
  const updateMutation = useUpdateSettings();
  const [searchParams, setSearchParams] = useSearchParams();
  const tabParam = searchParams.get("tab");
  const activeTab: TabKey = isTabKey(tabParam) ? tabParam : "api-keys";

  const setActiveTab = (t: TabKey) => {
    const next = new URLSearchParams(searchParams);
    next.set("tab", t);
    setSearchParams(next, { replace: true });
  };

  // Deferred-save state shared by API Keys + Environment tabs.
  const [pendingBraveKey, setPendingBraveKey] = useState<string | null>(null);
  const [resources, setResources] = useState<Record<string, string>>({});
  const [resetKey, setResetKey] = useState(0);

  if (isLoading || !settings) {
    return <div className="text-center py-12 text-gray-500">Loading...</div>;
  }

  const effective = (key: string): string => {
    if (key in resources) return resources[key];
    const v = (settings as Record<string, unknown>)[key];
    return typeof v === "string" ? v : "";
  };
  const resourceErrors = validateResourceQuantities({
    cpu_request: effective("default_cpu_request"),
    cpu_limit: effective("default_cpu_limit"),
    memory_request: effective("default_memory_request"),
    memory_limit: effective("default_memory_limit"),
    storage_home: effective("default_storage_home"),
    storage_homebrew: effective("default_storage_homebrew"),
  });
  const resourcesValid = Object.keys(resourceErrors).length === 0;

  const hasChanges =
    pendingBraveKey !== null || Object.keys(resources).length > 0;
  const stickyVisible =
    (activeTab === "api-keys" || activeTab === "environment") && hasChanges;

  const handleSave = () => {
    if (!resourcesValid) return;
    const payload: SettingsUpdatePayload = { ...resources };
    if (pendingBraveKey !== null) payload.brave_api_key = pendingBraveKey;

    updateMutation.mutate(payload, {
      onSuccess: () => {
        setPendingBraveKey(null);
        setResources({});
      },
    });
  };

  const handleReset = () => {
    setPendingBraveKey(null);
    setResources({});
    setResetKey((k) => k + 1);
  };

  // Env var changes save independently of the rest of the page so the editor
  // has its own Save button inside the card (see EnvVarsEditor). Per-instance
  // restart progress surfaces via TaskToasts (task type `instance.restart`).
  const handleSaveEnvVars = async (delta: {
    set: Record<string, string>;
    unset: string[];
  }) => {
    const payload: SettingsUpdatePayload = {};
    if (Object.keys(delta.set).length > 0) payload.env_vars_set = delta.set;
    if (delta.unset.length > 0) payload.env_vars_unset = delta.unset;
    await updateMutation.mutateAsync(payload);
  };

  return (
    <div>
      <h1 className="text-xl font-semibold text-gray-900 mb-6">Settings</h1>

      <div className="border-b border-gray-200 mb-6 flex gap-6">
        <TabButton active={activeTab === "api-keys"} onClick={() => setActiveTab("api-keys")}>
          API Keys
        </TabButton>
        <TabButton active={activeTab === "environment"} onClick={() => setActiveTab("environment")}>
          Environment
        </TabButton>
        <TabButton active={activeTab === "teams"} onClick={() => setActiveTab("teams")}>
          Teams
        </TabButton>
        <TabButton active={activeTab === "misc"} onClick={() => setActiveTab("misc")}>
          Misc
        </TabButton>
      </div>

      <div className="pb-24">
        {activeTab === "api-keys" && (
          <ApiKeysTab
            settings={settings}
            pendingBraveKey={pendingBraveKey}
            setPendingBraveKey={setPendingBraveKey}
          />
        )}
        {activeTab === "environment" && (
          <EnvironmentTab
            settings={settings}
            resetKey={resetKey}
            resources={resources}
            setResources={setResources}
            resourceErrors={resourceErrors}
            handleSaveEnvVars={handleSaveEnvVars}
            isSaving={updateMutation.isPending}
          />
        )}
        {activeTab === "teams" && <TeamsTab />}
        {activeTab === "misc" && <MiscTab settings={settings} />}
      </div>

      <StickyActionBar visible={stickyVisible}>
        <button
          type="button"
          onClick={handleReset}
          disabled={updateMutation.isPending}
          className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          Reset
        </button>
        <button
          onClick={handleSave}
          disabled={updateMutation.isPending || !hasChanges || !resourcesValid}
          className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {updateMutation.isPending ? "Saving..." : "Save"}
        </button>
      </StickyActionBar>
    </div>
  );
}

function TabButton({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={`-mb-px py-2 text-sm border-b-2 ${
        active
          ? "border-blue-600 text-blue-700 font-medium"
          : "border-transparent text-gray-500 hover:text-gray-700"
      }`}
    >
      {children}
    </button>
  );
}

// ---------- API Keys tab ----------

function ApiKeysTab({
  settings,
  pendingBraveKey,
  setPendingBraveKey,
}: {
  settings: Settings;
  pendingBraveKey: string | null;
  setPendingBraveKey: (v: string | null) => void;
}) {
  const queryClient = useQueryClient();
  const { data: providers = [] } = useProviders();
  const catalogIconMap = useCatalogIconMap();

  const [modalOpen, setModalOpen] = useState(false);
  const [modalMode, setModalMode] = useState<"create" | "edit">("create");
  const [modalProvider, setModalProvider] = useState<LLMProvider | null>(null);
  const [editingBrave, setEditingBrave] = useState(false);
  const [braveValue, setBraveValue] = useState("");
  const [showBrave, setShowBrave] = useState(false);

  const syncMutation = useMutation({
    mutationFn: syncAllProviders,
    onSuccess: () => {
      successToast("Catalog synced");
      queryClient.invalidateQueries({ queryKey: ["llm-providers"] });
    },
    onError: (err) => errorToast("Sync failed", err),
  });

  const openCreateModal = () => {
    setModalMode("create");
    setModalProvider(null);
    setModalOpen(true);
  };
  const openEditModal = (p: LLMProvider) => {
    setModalMode("edit");
    setModalProvider(p);
    setModalOpen(true);
  };

  return (
    <div className="space-y-8 max-w-2xl">
      <div className="flex items-center gap-2 px-3 py-2 bg-amber-50 border border-amber-200 rounded-md text-sm text-amber-800">
        <AlertTriangle size={16} className="shrink-0" />
        Changing global API keys will update all instances that don't have overrides.
      </div>

      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-sm font-medium text-gray-900">Model API Keys</h3>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={() => syncMutation.mutate()}
              disabled={syncMutation.isPending}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <RefreshCw size={12} className={syncMutation.isPending ? "animate-spin" : ""} />
              {syncMutation.isPending ? "Syncing..." : "Sync Models"}
            </button>
            <button
              type="button"
              onClick={openCreateModal}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
            >
              <Plus size={12} />
              Add Provider
            </button>
          </div>
        </div>

        {providers.length === 0 ? (
          <p className="text-sm text-gray-400 italic">No providers configured.</p>
        ) : (
          <div className="divide-y divide-gray-100">
            {providers.map((p) => {
              const isOAuth = p.api_type === "openai-codex-responses";
              const apiKeyDisplay = p.masked_api_key || "not set";
              const oauthDisplay = isOAuth
                ? p.oauth_connected && p.oauth_expires_at
                  ? `Expires in ${formatExpiresIn(p.oauth_expires_at - Date.now())}`
                  : "ChatGPT account not linked"
                : null;
              const displayModels = (p.models || []).map((m) => m.id);
              return (
                <div key={p.id}>
                  <div className="flex items-center py-3 -mx-2 px-2 rounded transition-colors">
                    <div className="min-w-0 flex-1 flex items-center gap-3">
                      <div className="shrink-0 w-6 h-6 flex items-center justify-center">
                        {p.provider ? (
                          <ProviderIcon provider={catalogIconMap[p.provider] ?? p.provider} size={22} />
                        ) : (
                          <span className="w-6 h-6 rounded-full bg-gray-100 flex items-center justify-center text-xs font-medium text-gray-500">
                            {p.name[0].toUpperCase()}
                          </span>
                        )}
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <span className="text-sm font-medium text-gray-900">{p.name}</span>
                          <span className="text-xs font-mono text-gray-400 bg-gray-100 px-1.5 py-0.5 rounded">{p.key}</span>
                        </div>
                        <p className="text-xs font-mono text-gray-500 mt-0.5 truncate">{p.base_url}</p>
                        {isOAuth ? (
                          <p className="text-xs text-gray-400 mt-0.5">{oauthDisplay}</p>
                        ) : (
                          <p className="text-xs text-gray-400 mt-0.5">
                            API key: <span className="font-mono">{apiKeyDisplay}</span>
                          </p>
                        )}
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={() => openEditModal(p)}
                      className="shrink-0 ml-2 p-1 text-gray-400 hover:text-gray-600 rounded"
                      title="Edit provider"
                    >
                      <Pencil size={14} />
                    </button>
                  </div>
                  <div className="pb-3 px-2">
                    {displayModels.length === 0 ? (
                      <p className="text-xs text-gray-400 italic">No models available.</p>
                    ) : (
                      <div className="flex flex-wrap gap-1">
                        {displayModels.map((id) => (
                          <span key={id} className="font-mono text-xs bg-gray-100 text-gray-600 px-1.5 py-0.5 rounded">
                            {id}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-4">Brave API Key</h3>
        <p className="text-xs text-gray-500 mb-3">Used for web search (not an LLM provider key).</p>
        {editingBrave ? (
          <div className="flex gap-2">
            <div className="relative flex-1">
              <input
                type={showBrave ? "text" : "password"}
                value={braveValue}
                onChange={(e) => {
                  setBraveValue(e.target.value);
                  setPendingBraveKey(e.target.value);
                }}
                className="w-full px-3 py-1.5 pr-10 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                placeholder="Enter Brave API key"
              />
              <button
                type="button"
                onClick={() => setShowBrave(!showBrave)}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
              >
                {showBrave ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
            </div>
            <button
              type="button"
              onClick={() => {
                setEditingBrave(false);
                setBraveValue("");
                setPendingBraveKey(null);
              }}
              className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
          </div>
        ) : (
          <div className="flex items-center gap-2">
            <span className="text-sm text-gray-500 font-mono">
              {pendingBraveKey !== null
                ? pendingBraveKey
                  ? "****" + pendingBraveKey.slice(-4)
                  : "(not set)"
                : settings.brave_api_key || "(not set)"}
            </span>
            <button
              type="button"
              onClick={() => setEditingBrave(true)}
              className="text-xs text-blue-600 hover:text-blue-800"
            >
              Change
            </button>
          </div>
        )}
      </div>

      <ProviderModal
        open={modalOpen}
        mode={modalMode}
        provider={modalProvider ?? undefined}
        existingKeys={providers.map((p) => p.key)}
        onClose={() => setModalOpen(false)}
        onSaved={() => {}}
      />
    </div>
  );
}

// ---------- Environment tab ----------

function EnvironmentTab({
  settings,
  resetKey,
  resources,
  setResources,
  resourceErrors,
  handleSaveEnvVars,
  isSaving,
}: {
  settings: Settings;
  resetKey: number;
  resources: Record<string, string>;
  setResources: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  resourceErrors: ReturnType<typeof validateResourceQuantities>;
  handleSaveEnvVars: (delta: { set: Record<string, string>; unset: string[] }) => Promise<void>;
  isSaving: boolean;
}) {
  const resourceFields: {
    key: string;
    label: string;
    errorKey: "cpu_request" | "cpu_limit" | "memory_request" | "memory_limit" | "storage_home" | "storage_homebrew";
  }[] = [
    { key: "default_cpu_request", label: "CPU Request", errorKey: "cpu_request" },
    { key: "default_cpu_limit", label: "CPU Limit", errorKey: "cpu_limit" },
    { key: "default_memory_request", label: "Memory Request", errorKey: "memory_request" },
    { key: "default_memory_limit", label: "Memory Limit", errorKey: "memory_limit" },
    { key: "default_storage_homebrew", label: "Homebrew Storage", errorKey: "storage_homebrew" },
    { key: "default_storage_home", label: "Home Storage", errorKey: "storage_home" },
  ];

  void resources; // referenced via setResources only; reads come from settings + resources merge

  return (
    <div className="space-y-8 max-w-2xl">
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-1">Instance Defaults</h3>
        <p className="text-xs text-gray-500 mb-4">
          Applied only when a new instance is created. Changing these values does not affect existing instances.
        </p>
        <div key={resetKey} className="space-y-4">
          <div>
            <label className="block text-xs text-gray-500 mb-1">Image</label>
            <input
              type="text"
              defaultValue={settings.default_agent_image ?? ""}
              onChange={(e) => setResources((r) => ({ ...r, default_agent_image: e.target.value }))}
              placeholder="glukw/claworc-agent:latest"
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">Timezone</label>
            <input
              type="text"
              defaultValue={settings.default_timezone ?? ""}
              onChange={(e) => setResources((r) => ({ ...r, default_timezone: e.target.value }))}
              placeholder="e.g., America/New_York"
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            {resourceFields.map((field) => {
              const err = resourceErrors[field.errorKey];
              return (
                <div key={field.key}>
                  <label className="block text-xs text-gray-500 mb-1">{field.label}</label>
                  <input
                    type="text"
                    defaultValue={(settings as Record<string, unknown>)[field.key] as string ?? ""}
                    onChange={(e) => setResources((r) => ({ ...r, [field.key]: e.target.value }))}
                    className={`w-full px-3 py-1.5 border rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 ${err ? "border-red-300" : "border-gray-300"}`}
                  />
                  {err && <p className="mt-1 text-xs text-red-600">{err}</p>}
                </div>
              );
            })}
          </div>
          {resourceErrors.cpu_pair && (
            <p className="mt-2 text-xs text-red-600">{resourceErrors.cpu_pair}</p>
          )}
          {resourceErrors.memory_pair && (
            <p className="mt-2 text-xs text-red-600">{resourceErrors.memory_pair}</p>
          )}
        </div>
      </div>

      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-1">Browser Defaults</h3>
        <p className="text-xs text-gray-500 mb-4">Browser settings used to launch a browser for each instance.</p>
        <div key={resetKey} className="space-y-4">
          <div>
            <label className="block text-xs text-gray-500 mb-1">Image</label>
            <input
              type="text"
              defaultValue={settings.default_browser_image ?? ""}
              onChange={(e) => setResources((r) => ({ ...r, default_browser_image: e.target.value }))}
              placeholder="glukw/claworc-browser-chromium:latest"
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="block text-xs text-gray-500 mb-1">Idle Timeout (min)</label>
              <input
                type="number"
                min={1}
                defaultValue={settings.default_browser_idle_minutes ?? "15"}
                onChange={(e) => setResources((r) => ({ ...r, default_browser_idle_minutes: e.target.value }))}
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <div>
              <label className="block text-xs text-gray-500 mb-1">Ready Timeout (sec)</label>
              <input
                type="number"
                min={5}
                defaultValue={settings.default_browser_ready_seconds ?? "60"}
                onChange={(e) => setResources((r) => ({ ...r, default_browser_ready_seconds: e.target.value }))}
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">Resolution</label>
            <input
              type="text"
              defaultValue={settings.default_vnc_resolution ?? "1920x1080"}
              onChange={(e) => setResources((r) => ({ ...r, default_vnc_resolution: e.target.value }))}
              placeholder="e.g., 1920x1080"
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
          <div>
            <label className="block text-xs text-gray-500 mb-1">User-Agent</label>
            <input
              type="text"
              defaultValue={settings.default_user_agent ?? ""}
              onChange={(e) => setResources((r) => ({ ...r, default_user_agent: e.target.value }))}
              placeholder="Leave empty to use browser default"
              className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>
        </div>
      </div>

      <EnvVarsEditor
        values={settings.default_env_vars ?? {}}
        title="Environment Variables"
        description="Passed to every OpenClaw instance at container start. Per-instance values override these when the name matches. Values are encrypted at rest. Saving restarts every running instance so the change takes effect immediately."
        onSave={handleSaveEnvVars}
        isSaving={isSaving}
        emptyMessage="No global environment variables set."
      />
    </div>
  );
}

// ---------- Teams tab ----------

function TeamsTab() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const { setActiveTeamId } = useTeam();
  const { data: teams = [], isLoading } = useQuery({
    queryKey: ["teams"],
    queryFn: fetchTeams,
  });

  const [showCreate, setShowCreate] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [editingName, setEditingName] = useState("");
  const [membersTeam, setMembersTeam] = useState<Team | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Team | null>(null);

  const renameMut = useMutation({
    mutationFn: ({ id, name }: { id: number; name: string }) =>
      updateTeam(id, { name }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["teams"] });
      setEditingId(null);
      successToast("Team renamed");
    },
    onError: (err) => errorToast("Failed to rename team", err),
  });

  const deleteMut = useMutation({
    mutationFn: (id: number) => deleteTeam(id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["teams"] });
      setDeleteTarget(null);
      successToast("Team deleted");
    },
    onError: (err) => {
      errorToast("Failed to delete team", err);
      setDeleteTarget(null);
    },
  });

  const startEdit = (t: Team) => {
    setEditingId(t.id);
    setEditingName(t.name);
  };

  const commitEdit = () => {
    if (!editingId) return;
    const name = editingName.trim();
    const target = teams.find((t) => t.id === editingId);
    if (!name || !target || name === target.name) {
      setEditingId(null);
      return;
    }
    renameMut.mutate({ id: editingId, name });
  };

  const goToInstances = (teamId: number) => {
    setActiveTeamId(teamId);
    navigate("/");
  };

  if (isLoading) {
    return <div className="text-gray-500">Loading teams...</div>;
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <p className="text-sm text-gray-500">
          Teams group instances and members. Each user can be a manager or a regular user of any team.
        </p>
        <button
          type="button"
          onClick={() => setShowCreate(true)}
          className="px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
        >
          Create Team
        </button>
      </div>

      <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 border-b border-gray-200">
            <tr>
              <th className="text-left px-4 py-3 font-medium text-gray-600">Name</th>
              <th className="text-left px-4 py-3 font-medium text-gray-600">Members</th>
              <th className="text-left px-4 py-3 font-medium text-gray-600">Instances</th>
              <th className="text-right px-4 py-3 font-medium text-gray-600">Actions</th>
            </tr>
          </thead>
          <tbody>
            {teams.map((team) => (
              <tr key={team.id} className="border-b border-gray-100 last:border-0">
                <td className="px-4 py-3">
                  {editingId === team.id ? (
                    <input
                      autoFocus
                      type="text"
                      value={editingName}
                      onChange={(e) => setEditingName(e.target.value)}
                      onBlur={commitEdit}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") commitEdit();
                        if (e.key === "Escape") setEditingId(null);
                      }}
                      className="px-2 py-1 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                    />
                  ) : (
                    <button
                      onClick={() => startEdit(team)}
                      className="font-medium text-blue-600 hover:text-blue-800"
                      title="Click to rename"
                    >
                      {team.name}
                      {team.is_default && (
                        <span className="ml-2 text-xs text-gray-400 font-normal">default</span>
                      )}
                    </button>
                  )}
                </td>
                <td className="px-4 py-3">
                  <button
                    onClick={() => setMembersTeam(team)}
                    className="text-blue-600 hover:text-blue-800"
                    title="Manage members"
                  >
                    {team.member_count ?? 0}
                  </button>
                </td>
                <td className="px-4 py-3">
                  <button
                    onClick={() => goToInstances(team.id)}
                    className="text-blue-600 hover:text-blue-800"
                    title="View instances"
                  >
                    {team.instance_count ?? 0}
                  </button>
                </td>
                <td className="px-4 py-3 text-right">
                  <div className="flex items-center justify-end gap-1">
                    <button
                      onClick={() => startEdit(team)}
                      className="p-1.5 text-gray-400 hover:text-gray-600 rounded"
                      title="Rename team"
                    >
                      <Pencil size={16} />
                    </button>
                    <button
                      onClick={() => !team.is_default && setDeleteTarget(team)}
                      disabled={team.is_default}
                      className="p-1.5 text-gray-400 hover:text-red-600 rounded disabled:opacity-30 disabled:hover:text-gray-400"
                      title={team.is_default ? "Default team cannot be deleted" : "Delete team"}
                    >
                      <Trash2 size={16} />
                    </button>
                  </div>
                </td>
              </tr>
            ))}
            {teams.length === 0 && (
              <tr>
                <td colSpan={4} className="text-center text-gray-400 py-6">
                  No teams yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {showCreate && <CreateTeamDialog onClose={() => setShowCreate(false)} />}

      {membersTeam && (
        <TeamMembersModal team={membersTeam} onClose={() => setMembersTeam(null)} />
      )}

      {deleteTarget && (
        <DeleteTeamDialog
          team={deleteTarget}
          onCancel={() => setDeleteTarget(null)}
          onConfirm={() => deleteMut.mutate(deleteTarget.id)}
          isPending={deleteMut.isPending}
        />
      )}
    </div>
  );
}

function TeamMembersModal({ team, onClose }: { team: Team; onClose: () => void }) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onClick={onClose}
      onKeyDown={(e) => {
        if (e.key === "Escape") onClose();
      }}
    >
      <div
        className="bg-white rounded-xl shadow-xl w-full max-w-md mx-4 flex flex-col max-h-[80vh]"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="px-6 py-4 border-b border-gray-200">
          <h2 className="text-base font-semibold text-gray-900">{team.name}</h2>
        </div>
        <div className="overflow-y-auto flex-1 px-6 py-4">
          <TeamMembersPanel team={team} />
        </div>
        <div className="px-6 py-4 border-t border-gray-200 flex items-center justify-end">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 text-sm text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}

function DeleteTeamDialog({
  team,
  onCancel,
  onConfirm,
  isPending,
}: {
  team: Team;
  onCancel: () => void;
  onConfirm: () => void;
  isPending: boolean;
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40" onClick={onCancel}>
      <div
        className="bg-white rounded-xl shadow-xl w-full max-w-md mx-4 p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="text-base font-semibold text-gray-900 mb-2">Delete team</h2>
        <p className="text-sm text-gray-600 mb-4">
          Delete team "{team.name}"? Instances belonging to this team will need to be reassigned.
        </p>
        <div className="flex justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="px-3 py-1.5 text-sm text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={isPending}
            className="px-3 py-1.5 text-sm text-white bg-red-600 rounded-md hover:bg-red-700 disabled:opacity-50"
          >
            {isPending ? "Deleting..." : "Delete"}
          </button>
        </div>
      </div>
    </div>
  );
}

// ---------- Misc tab ----------

function MiscTab({ settings }: { settings: Settings }) {
  const queryClient = useQueryClient();
  const updateMutation = useUpdateSettings();

  const fingerprint = useQuery({
    queryKey: ["ssh-fingerprint"],
    queryFn: fetchSSHFingerprint,
    staleTime: 60_000,
  });
  const rotateMutation = useMutation({
    mutationFn: rotateSSHKey,
    onSuccess: () => {
      successToast("SSH key rotated successfully");
      queryClient.invalidateQueries({ queryKey: ["ssh-fingerprint"] });
    },
    onError: (err) => errorToast("Failed to rotate SSH key", err),
  });

  return (
    <div className="space-y-8 max-w-2xl">
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="flex items-center justify-between mb-2">
          <h3 className="text-sm font-medium text-gray-900 flex items-center gap-1.5">
            <Key size={14} />
            SSH Tunnel
          </h3>
          <button
            onClick={() => rotateMutation.mutate()}
            disabled={rotateMutation.isPending}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <RefreshCw size={12} className={rotateMutation.isPending ? "animate-spin" : ""} />
            {rotateMutation.isPending ? "Rotating..." : "Rotate Key"}
          </button>
        </div>
        <p className="text-xs text-gray-500 mb-3">
          Global control plane SSH key used to connect to all instances.
        </p>
        {fingerprint.isLoading && <p className="text-xs text-gray-400">Loading...</p>}
        {fingerprint.isError && <p className="text-xs text-red-600">Failed to load fingerprint.</p>}
        {fingerprint.data && (
          <div className="bg-gray-50 border border-gray-200 rounded-md p-3">
            <div className="mb-2">
              <dt className="text-xs text-gray-500 mb-0.5">Fingerprint</dt>
              <dd className="text-xs font-mono text-gray-900 break-all">{fingerprint.data.fingerprint}</dd>
            </div>
            <div>
              <dt className="text-xs text-gray-500 mb-0.5">Public Key</dt>
              <dd className="text-xs font-mono text-gray-700 break-all whitespace-pre-wrap leading-relaxed">
                {fingerprint.data.public_key.trim()}
              </dd>
            </div>
          </div>
        )}
      </div>

      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-1">Anonymous Analytics</h3>
        <p className="text-xs text-gray-500 mb-4">
          Help us improve Claworc by sharing anonymous usage statistics. We never collect API keys, env-var values, file paths, or instance names. See{" "}
          <a href="https://claworc.com/docs/analytics" className="text-blue-600 hover:underline" target="_blank" rel="noreferrer">
            what's collected
          </a>
          .
        </p>
        <div className="space-y-4">
          <label className="inline-flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={settings.analytics_consent === "opt_in"}
              onChange={(e) => {
                updateMutation.mutate({ analytics_consent: e.target.checked ? "opt_in" : "opt_out" });
              }}
              className="h-4 w-4 text-blue-600 rounded border-gray-300"
            />
            <span className="text-sm text-gray-700">Share anonymous usage statistics</span>
          </label>
          <div>
            <dt className="text-xs text-gray-500 mb-1">Installation ID</dt>
            <dd className="text-xs font-mono text-gray-700 break-all">{settings.installation_id || "—"}</dd>
          </div>
        </div>
      </div>
    </div>
  );
}
