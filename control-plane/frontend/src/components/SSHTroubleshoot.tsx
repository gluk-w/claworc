import { useState } from "react";
import { X, Play, RefreshCw, Key, AlertCircle, CheckCircle2, Loader2 } from "lucide-react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { testSSHConnection, reconnectSSH, fetchSSHFingerprint, type SSHTarget } from "@/api/ssh";
import type { SSHTestResponse, SSHReconnectResponse } from "@/types/ssh";

interface SSHTroubleshootProps {
  instanceId: number;
  containerImage?: string | null;
  onClose: () => void;
}

// isLegacyEmbedded mirrors database.IsLegacyEmbedded — legacy combined images
// run Chromium inside the agent pod, so there is no separate browser SSH
// endpoint to probe. Empty / null is treated as legacy too: pre-upgrade rows
// often store "" and rely on the configured default image.
function isLegacyEmbedded(image: string | null | undefined): boolean {
  if (!image) return true;
  return image.includes("openclaw-vnc-");
}

type TargetResults<T> = { agent: T | null; browser: T | null };

export default function SSHTroubleshoot({ instanceId, containerImage, onClose }: SSHTroubleshootProps) {
  const showBrowser = !isLegacyEmbedded(containerImage);

  const [testResults, setTestResults] = useState<TargetResults<SSHTestResponse>>({ agent: null, browser: null });
  const [reconnectResults, setReconnectResults] = useState<TargetResults<SSHReconnectResponse>>({ agent: null, browser: null });

  const fingerprint = useQuery({
    queryKey: ["ssh-fingerprint"],
    queryFn: fetchSSHFingerprint,
    staleTime: 60_000,
  });

  // Settled response from one fan-out leg. We always render a panel for each
  // target the user opted into, even when the request itself rejected (e.g.
  // network error) — surface that as a synthetic error response.
  function settledToTest(target: SSHTarget, r: PromiseSettledResult<SSHTestResponse>): SSHTestResponse {
    if (r.status === "fulfilled") return { ...r.value, target };
    return {
      status: "error",
      output: "",
      latency_ms: 0,
      error: r.reason instanceof Error ? r.reason.message : "Request failed. The server may be unreachable.",
      target,
    };
  }

  function settledToReconnect(target: SSHTarget, r: PromiseSettledResult<SSHReconnectResponse>): SSHReconnectResponse {
    if (r.status === "fulfilled") return { ...r.value, target };
    return {
      status: "error",
      latency_ms: 0,
      error: r.reason instanceof Error ? r.reason.message : "Request failed. The server may be unreachable.",
      target,
    };
  }

  const testMutation = useMutation({
    mutationFn: async () => {
      const targets: SSHTarget[] = showBrowser ? ["agent", "browser"] : ["agent"];
      const settled = await Promise.allSettled(targets.map((t) => testSSHConnection(instanceId, t)));
      const next: TargetResults<SSHTestResponse> = { agent: null, browser: null };
      targets.forEach((t, i) => {
        const r = settled[i];
        if (r) next[t] = settledToTest(t, r);
      });
      return next;
    },
    onSuccess: (data) => setTestResults(data),
  });

  const reconnectMutation = useMutation({
    mutationFn: async () => {
      const targets: SSHTarget[] = showBrowser ? ["agent", "browser"] : ["agent"];
      const settled = await Promise.allSettled(targets.map((t) => reconnectSSH(instanceId, t)));
      const next: TargetResults<SSHReconnectResponse> = { agent: null, browser: null };
      targets.forEach((t, i) => {
        const r = settled[i];
        if (r) next[t] = settledToReconnect(t, r);
      });
      return next;
    },
    onSuccess: (data) => setReconnectResults(data),
  });

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/40" onClick={onClose} />
      <div className="relative bg-white rounded-lg shadow-xl w-full max-w-lg mx-4 max-h-[90vh] overflow-y-auto">
        {/* Header */}
        <div className="flex items-center justify-between p-4 border-b border-gray-200">
          <h2 className="text-base font-semibold text-gray-900">SSH Troubleshooting</h2>
          <button onClick={onClose} className="p-1 text-gray-400 hover:text-gray-600 rounded">
            <X size={18} />
          </button>
        </div>

        <div className="p-4 space-y-5">
          {/* Connection Test */}
          <section>
            <h3 className="text-sm font-medium text-gray-900 mb-2">Connection Test</h3>
            <p className="text-xs text-gray-500 mb-3">
              {showBrowser
                ? "Runs a simple SSH command against the agent pod and the browser pod to verify end-to-end connectivity."
                : "Runs a simple SSH command against the agent pod to verify end-to-end connectivity."}
            </p>
            <button
              onClick={() => {
                setTestResults({ agent: null, browser: null });
                testMutation.mutate();
              }}
              disabled={testMutation.isPending}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {testMutation.isPending ? (
                <Loader2 size={14} className="animate-spin" />
              ) : (
                <Play size={14} />
              )}
              {testMutation.isPending ? "Testing..." : "Run Test"}
            </button>

            <TestResultPanel label="Agent pod" result={testResults.agent} />
            {showBrowser && <TestResultPanel label="Browser pod" result={testResults.browser} />}
          </section>

          {/* Manual Reconnect */}
          <section>
            <h3 className="text-sm font-medium text-gray-900 mb-2">Manual Reconnect</h3>
            <p className="text-xs text-gray-500 mb-3">
              {showBrowser
                ? "Closes the agent and browser SSH connections and re-establishes them. Re-uploads the public key on each pod."
                : "Closes the existing SSH connection and re-establishes it. This will re-upload the public key to the instance."}
            </p>
            <button
              onClick={() => {
                setReconnectResults({ agent: null, browser: null });
                reconnectMutation.mutate();
              }}
              disabled={reconnectMutation.isPending}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-amber-600 rounded-md hover:bg-amber-700 disabled:opacity-50"
            >
              {reconnectMutation.isPending ? (
                <Loader2 size={14} className="animate-spin" />
              ) : (
                <RefreshCw size={14} />
              )}
              {reconnectMutation.isPending ? "Reconnecting..." : "Reconnect"}
            </button>

            <ReconnectResultPanel label="Agent pod" result={reconnectResults.agent} />
            {showBrowser && <ReconnectResultPanel label="Browser pod" result={reconnectResults.browser} />}
          </section>

          {/* SSH Public Key Fingerprint */}
          <section>
            <h3 className="text-sm font-medium text-gray-900 mb-2 flex items-center gap-1.5">
              <Key size={14} />
              SSH Public Key
            </h3>
            <p className="text-xs text-gray-500 mb-3">
              Global control plane public key fingerprint. This key is shared across all instances.
            </p>
            {fingerprint.isLoading && (
              <p className="text-xs text-gray-400">Loading...</p>
            )}
            {fingerprint.isError && (
              <p className="text-xs text-red-600">Failed to load fingerprint.</p>
            )}
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
          </section>

          {/* Troubleshooting Tips */}
          <section>
            <h3 className="text-sm font-medium text-gray-900 mb-2">Troubleshooting Tips</h3>
            <ul className="text-xs text-gray-600 space-y-1.5 list-disc list-inside">
              <li>Ensure the instance is running and the container has started.</li>
              <li>If the instance was recently restarted, the SSH key may need to be re-uploaded — use Reconnect above.</li>
              <li>Check Connection Events on the Overview tab for recent errors.</li>
              <li>Repeated "health_check_failed" events may indicate the agent is under heavy load.</li>
              <li>If only the Browser pod fails, check that the browser deployment is Ready and the SSH NetworkPolicy allows port 22.</li>
            </ul>
          </section>
        </div>
      </div>
    </div>
  );
}

function TestResultPanel({ label, result }: { label: string; result: SSHTestResponse | null }) {
  if (!result) return null;
  const ok = result.status === "ok";
  return (
    <div
      className={`mt-3 p-3 rounded-md text-sm ${
        ok ? "bg-green-50 border border-green-200" : "bg-red-50 border border-red-200"
      }`}
    >
      <div className="flex items-center gap-1.5 mb-1">
        {ok ? (
          <CheckCircle2 size={14} className="text-green-600" />
        ) : (
          <AlertCircle size={14} className="text-red-600" />
        )}
        <span className={`font-medium ${ok ? "text-green-800" : "text-red-800"}`}>{label}: {ok ? "Success" : "Failed"}</span>
        <span className="text-gray-500 ml-auto text-xs">{result.latency_ms}ms</span>
      </div>
      {result.output && (
        <pre className="text-xs text-gray-700 mt-1 whitespace-pre-wrap">{result.output.trim()}</pre>
      )}
      {result.error && <p className="text-xs text-red-700 mt-1">{result.error}</p>}
    </div>
  );
}

function ReconnectResultPanel({ label, result }: { label: string; result: SSHReconnectResponse | null }) {
  if (!result) return null;
  const ok = result.status === "ok";
  return (
    <div
      className={`mt-3 p-3 rounded-md text-sm ${
        ok ? "bg-green-50 border border-green-200" : "bg-red-50 border border-red-200"
      }`}
    >
      <div className="flex items-center gap-1.5">
        {ok ? (
          <CheckCircle2 size={14} className="text-green-600" />
        ) : (
          <AlertCircle size={14} className="text-red-600" />
        )}
        <span className={`font-medium ${ok ? "text-green-800" : "text-red-800"}`}>{label}: {ok ? "Reconnected" : "Reconnect Failed"}</span>
        <span className="text-gray-500 ml-auto text-xs">{result.latency_ms}ms</span>
      </div>
      {result.error && <p className="text-xs text-red-700 mt-1">{result.error}</p>}
    </div>
  );
}
