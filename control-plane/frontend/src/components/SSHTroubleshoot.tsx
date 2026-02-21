import { useState } from "react";
import {
  Wrench,
  Play,
  RefreshCw,
  CheckCircle,
  XCircle,
  Clock,
  Fingerprint,
  Loader2,
  AlertTriangle,
  Info,
} from "lucide-react";
import { useSSHTest, useSSHReconnect, useSSHFingerprint } from "@/hooks/useSSH";
import type { SSHTestResult } from "@/types/ssh";

interface SSHTroubleshootProps {
  instanceId: number;
  onClose: () => void;
}

export default function SSHTroubleshoot({
  instanceId,
  onClose,
}: SSHTroubleshootProps) {
  const [testResult, setTestResult] = useState<SSHTestResult | null>(null);
  const [testError, setTestError] = useState<string | null>(null);
  const [reconnectMessage, setReconnectMessage] = useState<string | null>(null);
  const [reconnectError, setReconnectError] = useState<string | null>(null);

  const testMutation = useSSHTest(instanceId);
  const reconnectMutation = useSSHReconnect(instanceId);
  const { data: fingerprint, isLoading: fingerprintLoading } = useSSHFingerprint(instanceId);

  const handleTest = () => {
    setTestResult(null);
    setTestError(null);
    testMutation.mutate(undefined, {
      onSuccess: (data) => setTestResult(data),
      onError: (err: any) =>
        setTestError(err.response?.data?.detail || "Connection test failed"),
    });
  };

  const handleReconnect = () => {
    setReconnectMessage(null);
    setReconnectError(null);
    reconnectMutation.mutate(undefined, {
      onSuccess: (data) => setReconnectMessage(data.message),
      onError: (err: any) =>
        setReconnectError(
          err.response?.data?.detail || "Reconnection failed",
        ),
    });
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/50" onClick={onClose} />
      <div className="relative bg-white rounded-lg shadow-lg p-6 max-w-lg w-full mx-4 max-h-[90vh] overflow-y-auto">
        <div className="flex items-center gap-2 mb-4">
          <Wrench size={18} className="text-gray-700" />
          <h3 className="text-lg font-semibold text-gray-900">
            SSH Troubleshooting
          </h3>
        </div>

        {/* Connection Test */}
        <div className="mb-6">
          <div className="flex items-center justify-between mb-2">
            <h4 className="text-sm font-medium text-gray-900">
              Connection Test
            </h4>
            <button
              onClick={handleTest}
              disabled={testMutation.isPending}
              className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
            >
              {testMutation.isPending ? (
                <Loader2 size={12} className="animate-spin" />
              ) : (
                <Play size={12} />
              )}
              {testMutation.isPending ? "Testing..." : "Run Test"}
            </button>
          </div>
          <p className="text-xs text-gray-500 mb-3">
            Tests SSH connectivity, tunnel health, and command execution.
          </p>

          {testResult && (
            <div className="rounded-md border border-gray-200 p-3 space-y-2">
              <div className="flex items-center gap-2">
                {testResult.success ? (
                  <CheckCircle size={14} className="text-green-500" />
                ) : (
                  <XCircle size={14} className="text-red-500" />
                )}
                <span
                  className={`text-sm font-medium ${testResult.success ? "text-green-700" : "text-red-700"}`}
                >
                  {testResult.success ? "Connection OK" : "Connection Failed"}
                </span>
              </div>

              <div className="flex items-center gap-2 text-xs text-gray-600">
                <Clock size={12} />
                <span>Latency: {testResult.latency_ms}ms</span>
              </div>

              <div className="flex items-center gap-2 text-xs text-gray-600">
                {testResult.command_test ? (
                  <CheckCircle size={12} className="text-green-500" />
                ) : (
                  <XCircle size={12} className="text-red-500" />
                )}
                <span>
                  Command execution:{" "}
                  {testResult.command_test ? "OK" : "Failed"}
                </span>
              </div>

              {testResult.tunnel_status.length > 0 && (
                <div className="mt-2">
                  <p className="text-xs font-medium text-gray-700 mb-1">
                    Tunnel Status:
                  </p>
                  {testResult.tunnel_status.map((t) => (
                    <div
                      key={t.service}
                      className="flex items-center gap-2 text-xs text-gray-600 ml-2"
                    >
                      {t.healthy ? (
                        <CheckCircle size={12} className="text-green-500" />
                      ) : (
                        <XCircle size={12} className="text-red-500" />
                      )}
                      <span>
                        {t.service}: {t.healthy ? "Healthy" : t.error || "Unhealthy"}
                      </span>
                    </div>
                  ))}
                </div>
              )}

              {testResult.error && (
                <p className="text-xs text-red-600 mt-1">{testResult.error}</p>
              )}
            </div>
          )}

          {testError && (
            <div className="rounded-md border border-red-200 bg-red-50 p-3">
              <p className="text-xs text-red-700">{testError}</p>
            </div>
          )}
        </div>

        {/* Reconnect */}
        <div className="mb-6">
          <div className="flex items-center justify-between mb-2">
            <h4 className="text-sm font-medium text-gray-900">
              Force Reconnect
            </h4>
            <button
              onClick={handleReconnect}
              disabled={reconnectMutation.isPending}
              className="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-amber-600 rounded-md hover:bg-amber-700 disabled:opacity-50"
            >
              {reconnectMutation.isPending ? (
                <Loader2 size={12} className="animate-spin" />
              ) : (
                <RefreshCw size={12} />
              )}
              {reconnectMutation.isPending ? "Reconnecting..." : "Reconnect"}
            </button>
          </div>
          <p className="text-xs text-gray-500 mb-3">
            Drops the current SSH connection and establishes a new one.
          </p>

          {reconnectMessage && (
            <div className="rounded-md border border-green-200 bg-green-50 p-3">
              <div className="flex items-center gap-2">
                <CheckCircle size={14} className="text-green-500" />
                <p className="text-xs text-green-700">{reconnectMessage}</p>
              </div>
            </div>
          )}

          {reconnectError && (
            <div className="rounded-md border border-red-200 bg-red-50 p-3">
              <div className="flex items-center gap-2">
                <XCircle size={14} className="text-red-500" />
                <p className="text-xs text-red-700">{reconnectError}</p>
              </div>
            </div>
          )}
        </div>

        {/* SSH Public Key Fingerprint */}
        <div className="mb-6">
          <div className="flex items-center gap-2 mb-2">
            <Fingerprint size={14} className="text-gray-700" />
            <h4 className="text-sm font-medium text-gray-900">
              SSH Key Fingerprint
            </h4>
          </div>
          {fingerprintLoading ? (
            <div className="animate-pulse h-4 bg-gray-200 rounded w-2/3" />
          ) : fingerprint ? (
            <div className="rounded-md border border-gray-200 bg-gray-50 p-3">
              <p className="text-xs text-gray-500 mb-1">
                {fingerprint.algorithm}
              </p>
              <code className="text-xs text-gray-800 break-all font-mono">
                {fingerprint.fingerprint}
              </code>
              <div className="flex items-center gap-1.5 mt-2">
                {fingerprint.verified ? (
                  <>
                    <CheckCircle size={12} className="text-green-500" />
                    <span className="text-xs text-green-700">Verified</span>
                  </>
                ) : (
                  <>
                    <AlertTriangle size={12} className="text-amber-500" />
                    <span className="text-xs text-amber-700">
                      Fingerprint mismatch — key may have been modified
                    </span>
                  </>
                )}
              </div>
            </div>
          ) : (
            <p className="text-xs text-gray-500">
              Fingerprint not available.
            </p>
          )}
        </div>

        {/* Troubleshooting Tips */}
        <div className="mb-6">
          <div className="flex items-center gap-2 mb-2">
            <Info size={14} className="text-gray-700" />
            <h4 className="text-sm font-medium text-gray-900">
              Troubleshooting Tips
            </h4>
          </div>
          <div className="space-y-2 text-xs text-gray-600">
            <div className="flex gap-2">
              <AlertTriangle
                size={12}
                className="text-amber-500 mt-0.5 shrink-0"
              />
              <span>
                If the connection is stuck in "reconnecting", try forcing a
                reconnect above.
              </span>
            </div>
            <div className="flex gap-2">
              <AlertTriangle
                size={12}
                className="text-amber-500 mt-0.5 shrink-0"
              />
              <span>
                High latency (&gt;500ms) may indicate network issues between the
                control plane and the instance.
              </span>
            </div>
            <div className="flex gap-2">
              <AlertTriangle
                size={12}
                className="text-amber-500 mt-0.5 shrink-0"
              />
              <span>
                If tunnels are unhealthy but the connection is active, the
                remote services (VNC, Gateway) may not be running on the
                instance.
              </span>
            </div>
            <div className="flex gap-2">
              <AlertTriangle
                size={12}
                className="text-amber-500 mt-0.5 shrink-0"
              />
              <span>
                Verify the SSH key fingerprint matches the instance's
                authorized_keys to rule out MITM issues.
              </span>
            </div>
            <div className="flex gap-2">
              <AlertTriangle
                size={12}
                className="text-amber-500 mt-0.5 shrink-0"
              />
              <span>
                Check the Connection Events log for patterns like repeated
                reconnection failures that may indicate a persistent issue.
              </span>
            </div>
          </div>
        </div>

        {/* Close Button */}
        <div className="flex justify-end">
          <button
            onClick={onClose}
            className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
