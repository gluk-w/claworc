import { useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { isAxiosError } from "axios";
import { Check, Plus, Pencil, Eye, EyeOff } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import { setupCreateAdmin } from "@/api/auth";
import { useProviders, useCatalogIconMap } from "@/hooks/useProviders";
import { useUpdateSettings } from "@/hooks/useSettings";
import ProviderModal from "@/components/ProviderModal";
import ProviderIcon from "@/components/ProviderIcon";
import { getNetworkOrServerError } from "@/utils/http";
import { errorToast } from "@/utils/toast";
import type { LLMProvider } from "@/types/instance";

type Step = 1 | 2 | 3;

function getSetupError(error: unknown): string {
  const networkOrServer = getNetworkOrServerError(error);
  if (networkOrServer) return networkOrServer;
  if (isAxiosError(error)) {
    const detail = error.response?.data?.detail;
    if (typeof detail === "string") return detail;
  }
  return "Failed to create admin account";
}

export default function OnboardingPage() {
  const [step, setStep] = useState<Step>(1);

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50 px-4 py-8">
      <div className="w-full max-w-xl">
        <div className="mb-6 text-center">
          <h1 className="text-2xl font-semibold text-gray-900">Welcome to Claworc</h1>
          <p className="text-sm text-gray-500 mt-1">Let's get you set up. This takes about a minute.</p>
        </div>

        <Stepper step={step} />

        <div className="bg-white rounded-lg shadow-sm border border-gray-200 p-6">
          {step === 1 && <AdminStep onDone={() => setStep(2)} />}
          {step === 2 && <ProvidersStep onDone={() => setStep(3)} />}
          {step === 3 && <TrackingStep />}
        </div>
      </div>
    </div>
  );
}

function Stepper({ step }: { step: Step }) {
  const labels = ["Admin account", "API keys", "Analytics"];
  return (
    <div className="flex items-center justify-center gap-2 mb-6">
      {labels.map((label, i) => {
        const n = (i + 1) as Step;
        const done = step > n;
        const active = step === n;
        return (
          <div key={label} className="flex items-center gap-2">
            <div
              className={`w-7 h-7 rounded-full flex items-center justify-center text-xs font-medium border ${
                done
                  ? "bg-blue-600 border-blue-600 text-white"
                  : active
                    ? "bg-white border-blue-600 text-blue-700"
                    : "bg-white border-gray-300 text-gray-400"
              }`}
            >
              {done ? <Check size={14} /> : n}
            </div>
            <span
              className={`text-xs ${active ? "text-gray-900 font-medium" : "text-gray-500"}`}
            >
              {label}
            </span>
            {n < 3 && <div className="w-6 h-px bg-gray-300 mx-1" />}
          </div>
        );
      })}
    </div>
  );
}

// ---------- Step 1: Admin ----------

function AdminStep({ onDone }: { onDone: () => void }) {
  const { refetch } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    if (password !== confirmPassword) {
      setError("Passwords do not match");
      return;
    }
    setLoading(true);
    try {
      await setupCreateAdmin({ username, password });
      refetch();
      onDone();
    } catch (err) {
      setError(getSetupError(err));
    } finally {
      setLoading(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <div>
        <h2 className="text-base font-semibold text-gray-900">Create admin account</h2>
        <p className="text-sm text-gray-500">You'll use this account to sign in and manage Claworc.</p>
      </div>

      {error && (
        <div data-testid="onboarding-error" className="p-3 text-sm text-red-700 bg-red-50 border border-red-200 rounded-md">
          {error}
        </div>
      )}

      <div>
        <label className="block text-sm font-medium text-gray-700 mb-1">Username</label>
        <input
          data-testid="onboarding-username"
          type="text"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          required
          autoFocus
          autoComplete="username"
        />
      </div>
      <div>
        <label className="block text-sm font-medium text-gray-700 mb-1">Password</label>
        <input
          data-testid="onboarding-password"
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          required
          autoComplete="new-password"
        />
      </div>
      <div>
        <label className="block text-sm font-medium text-gray-700 mb-1">Confirm password</label>
        <input
          data-testid="onboarding-confirm-password"
          type="password"
          value={confirmPassword}
          onChange={(e) => setConfirmPassword(e.target.value)}
          className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          required
          autoComplete="new-password"
        />
      </div>

      <div className="flex justify-end pt-2">
        <button
          type="submit"
          disabled={loading}
          className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
        >
          {loading ? "Creating..." : "Continue"}
        </button>
      </div>
    </form>
  );
}

// ---------- Step 2: LLM Providers + Brave key ----------

function ProvidersStep({ onDone }: { onDone: () => void }) {
  const queryClient = useQueryClient();
  const { data: providers = [] } = useProviders();
  const catalogIconMap = useCatalogIconMap();
  const updateMutation = useUpdateSettings();

  const [modalOpen, setModalOpen] = useState(false);
  const [modalMode, setModalMode] = useState<"create" | "edit">("create");
  const [modalProvider, setModalProvider] = useState<LLMProvider | null>(null);
  const [braveKey, setBraveKey] = useState("");
  const [showBrave, setShowBrave] = useState(false);
  const [saving, setSaving] = useState(false);

  const openCreate = () => {
    setModalMode("create");
    setModalProvider(null);
    setModalOpen(true);
  };
  const openEdit = (p: LLMProvider) => {
    setModalMode("edit");
    setModalProvider(p);
    setModalOpen(true);
  };

  const handleContinue = async () => {
    if (!braveKey.trim()) {
      onDone();
      return;
    }
    setSaving(true);
    try {
      await updateMutation.mutateAsync({ brave_api_key: braveKey.trim() });
      onDone();
    } catch {
      // toast handled by hook
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-base font-semibold text-gray-900">Add API keys</h2>
        <p className="text-sm text-gray-500">
          Configure at least one LLM provider so instances could use it. You can skip and add them later in Settings
        </p>
      </div>

      <div>
        <div className="flex items-center justify-between mb-2">
          <h3 className="text-sm font-medium text-gray-900">LLM Providers</h3>
          <button
            type="button"
            onClick={openCreate}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
          >
            <Plus size={12} />
            Add Provider
          </button>
        </div>
        {providers.length === 0 ? (
          <p className="text-sm text-gray-400 italic px-3 py-4 border border-dashed border-gray-200 rounded-md text-center">
            No providers configured yet.
          </p>
        ) : (
          <div className="border border-gray-200 rounded-md divide-y divide-gray-100">
            {providers.map((p) => (
              <div key={p.id} className="flex items-center gap-3 px-3 py-2">
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
                  <div className="text-sm font-medium text-gray-900 truncate">{p.name}</div>
                  <div className="text-xs text-gray-400 truncate">
                    API key: <span className="font-mono">{p.masked_api_key || "not set"}</span>
                  </div>
                </div>
                <button
                  type="button"
                  onClick={() => openEdit(p)}
                  className="shrink-0 p-1 text-gray-400 hover:text-gray-600"
                  title="Edit provider"
                >
                  <Pencil size={14} />
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      <div>
        <h3 className="text-sm font-medium text-gray-900 mb-1">Brave API Key</h3>
        <p className="text-xs text-gray-500 mb-2">Optional. Used for web search (not an LLM provider).</p>
        <div className="relative">
          <input
            type={showBrave ? "text" : "password"}
            value={braveKey}
            onChange={(e) => setBraveKey(e.target.value)}
            placeholder="Leave empty to skip"
            className="w-full px-3 py-2 pr-10 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
          <button
            type="button"
            onClick={() => setShowBrave((v) => !v)}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
          >
            {showBrave ? <EyeOff size={14} /> : <Eye size={14} />}
          </button>
        </div>
      </div>

      <div className="flex justify-end pt-2">
        <button
          type="button"
          onClick={handleContinue}
          disabled={saving}
          className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
        >
          {saving ? "Saving..." : "Continue"}
        </button>
      </div>

      <ProviderModal
        open={modalOpen}
        mode={modalMode}
        provider={modalProvider ?? undefined}
        existingKeys={providers.map((p) => p.key)}
        onClose={() => setModalOpen(false)}
        onSaved={() => {
          queryClient.invalidateQueries({ queryKey: ["llm-providers"] });
        }}
      />
    </div>
  );
}

// ---------- Step 3: Tracking consent ----------

function TrackingStep() {
  const updateMutation = useUpdateSettings();
  const [shareStats, setShareStats] = useState(true);
  const [finishing, setFinishing] = useState(false);
  const { refetch } = useAuth();

  const handleFinish = async () => {
    setFinishing(true);
    try {
      await updateMutation.mutateAsync({
        analytics_consent: shareStats ? "opt_in" : "opt_out",
      });
      // Trigger re-render at app level so setup-required guard re-evaluates.
      refetch();
      // Hard navigate to / so the app re-mounts cleanly with auth + settings fresh.
      window.location.assign("/");
    } catch (err) {
      errorToast("Failed to save preference", err);
      setFinishing(false);
    }
  };

  return (
    <div className="space-y-5">
      <div>
        <h2 className="text-base font-semibold text-gray-900">Anonymous analytics</h2>
        <p className="text-sm text-gray-500 mt-1">
          Help us improve Claworc by sharing anonymous usage statistics. We never collect API keys,
          env-var values, file paths, or instance names. See{" "}
          <a
            href="https://claworc.com/docs/analytics"
            className="text-blue-600 hover:underline"
            target="_blank"
            rel="noreferrer"
          >
            what's collected
          </a>
          .
        </p>
      </div>

      <label className="flex items-start gap-3 p-3 border border-gray-200 rounded-md cursor-pointer hover:bg-gray-50">
        <input
          type="checkbox"
          checked={shareStats}
          onChange={(e) => setShareStats(e.target.checked)}
          className="mt-0.5 h-4 w-4 text-blue-600 rounded border-gray-300"
        />
        <div className="text-sm">
          <div className="font-medium text-gray-900">Share anonymous usage statistics</div>
          <div className="text-xs text-gray-500 mt-0.5">You can change this anytime in Settings.</div>
        </div>
      </label>

      <div className="flex justify-end pt-2">
        <button
          type="button"
          onClick={handleFinish}
          disabled={finishing}
          className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
        >
          {finishing ? "Finishing..." : "Finish setup"}
        </button>
      </div>
    </div>
  );
}
