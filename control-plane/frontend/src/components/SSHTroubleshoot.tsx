import { useState } from "react";
import { X, Play, RefreshCw, Key, AlertCircle, CheckCircle2, Loader2 } from "lucide-react";
import { useQuery, useMutation } from "@tanstack/react-query";
import { testSSHConnection, reconnectSSH, fetchSSHFingerprint } from "@/api/ssh";
import type { SSHTestResponse, SSHReconnectResponse } from "@/types/ssh";

interface SSHTroubleshootProps {
  instanceId: number;
  onClose: () => void;
}

export default function SSHTroubleshoot({ instanceId, onClose }: SSHTroubleshootProps) {
  const [testResult, setTestResult] = useState<SSHTestResponse | null>(null);
  const [reconnectResult, setReconnectResult] = useState<SSHReconnectResponse | null>(null);

  const fingerprint = useQuery({
    queryKey: ["ssh-fingerprint"],
    queryFn: fetchSSHFingerprint,
    staleTime: 60_000,
  });

  const testMutation = useMutation({
    mutationFn: () => testSSHConnection(instanceId),
    onSuccess: (data) => setTestResult(data),
  });

  const reconnectMutation = useMutation({
    mutationFn: () => reconnectSSH(instanceId),
    onSuccess: (data) => setReconnectResult(data),
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
              Runs a simple command over SSH to verify end-to-end connectivity.
            </p>
            <button
              onClick={() => {
                setTestResult(null);
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

            {testResult && (
              <div
                className={`mt-3 p-3 rounded-md text-sm ${
                  testResult.status === "ok"
                    ? "bg-green-50 border border-green-200"
                    : "bg-red-50 border border-red-200"
                }`}
              >
                <div className="flex items-center gap-1.5 mb-1">
                  {testResult.status === "ok" ? (
                    <CheckCircle2 size={14} className="text-green-600" />
                  ) : (
                    <AlertCircle size={14} className="text-red-600" />
                  )}
                  <span className={`font-medium ${testResult.status === "ok" ? "text-green-800" : "text-red-800"}`}>
                    {testResult.status === "ok" ? "Success" : "Failed"}
                  </span>
                  <span className="text-gray-500 ml-auto text-xs">{testResult.latency_ms}ms</span>
                </div>
                {testResult.output && (
                  <pre className="text-xs text-gray-700 mt-1 whitespace-pre-wrap">{testResult.output.trim()}</pre>
                )}
                {testResult.error && (
                  <p className="text-xs text-red-700 mt-1">{testResult.error}</p>
                )}
              </div>
            )}

            {testMutation.isError && (
              <div className="mt-3 p-3 rounded-md text-sm bg-red-50 border border-red-200">
                <p className="text-xs text-red-700">Request failed. The server may be unreachable.</p>
              </div>
            )}
          </section>

          {/* Manual Reconnect */}
          <section>
            <h3 className="text-sm font-medium text-gray-900 mb-2">Manual Reconnect</h3>
            <p className="text-xs text-gray-500 mb-3">
              Closes the existing SSH connection and re-establishes it. This will re-upload the public key to the instance.
            </p>
            <button
              onClick={() => {
                setReconnectResult(null);
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

            {reconnectResult && (
              <div
                className={`mt-3 p-3 rounded-md text-sm ${
                  reconnectResult.status === "ok"
                    ? "bg-green-50 border border-green-200"
                    : "bg-red-50 border border-red-200"
                }`}
              >
                <div className="flex items-center gap-1.5">
                  {reconnectResult.status === "ok" ? (
                    <CheckCircle2 size={14} className="text-green-600" />
                  ) : (
                    <AlertCircle size={14} className="text-red-600" />
                  )}
                  <span className={`font-medium ${reconnectResult.status === "ok" ? "text-green-800" : "text-red-800"}`}>
                    {reconnectResult.status === "ok" ? "Reconnected" : "Reconnect Failed"}
                  </span>
                  <span className="text-gray-500 ml-auto text-xs">{reconnectResult.latency_ms}ms</span>
                </div>
                {reconnectResult.error && (
                  <p className="text-xs text-red-700 mt-1">{reconnectResult.error}</p>
                )}
              </div>
            )}

            {reconnectMutation.isError && (
              <div className="mt-3 p-3 rounded-md text-sm bg-red-50 border border-red-200">
                <p className="text-xs text-red-700">Request failed. The server may be unreachable.</p>
              </div>
            )}
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
              <li>If the connection is "Failed", try a manual reconnect to re-establish it.</li>
            </ul>
          </section>
        </div>
      </div>
    </div>
  );
}
