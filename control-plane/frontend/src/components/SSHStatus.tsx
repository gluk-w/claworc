import { RefreshCw } from "lucide-react";
import type { SSHStatusResponse } from "@/types/ssh";

const stateStyles: Record<string, { dot: string; text: string; label: string }> = {
  connected: { dot: "bg-green-500", text: "text-green-800", label: "Connected" },
  connecting: { dot: "bg-yellow-500", text: "text-yellow-800", label: "Connecting" },
  reconnecting: { dot: "bg-yellow-500", text: "text-yellow-800", label: "Reconnecting" },
  disconnected: { dot: "bg-gray-400", text: "text-gray-600", label: "Disconnected" },
  failed: { dot: "bg-red-500", text: "text-red-800", label: "Failed" },
};

function formatTime(ts: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleTimeString();
}

interface SSHStatusProps {
  status: SSHStatusResponse | undefined;
  isLoading: boolean;
  isError: boolean;
  onRefresh: () => void;
}

export default function SSHStatus({ status, isLoading, isError, onRefresh }: SSHStatusProps) {
  if (isLoading && !status) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="text-sm text-gray-500">Loading SSH status...</div>
      </div>
    );
  }

  if (isError && !status) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="flex items-center justify-between">
          <div className="text-sm text-red-600">Failed to load SSH status.</div>
          <button
            onClick={onRefresh}
            className="p-1 text-gray-400 hover:text-gray-600 rounded"
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
        </div>
      </div>
    );
  }

  if (!status) return null;

  const defaultStyle = { dot: "bg-gray-400", text: "text-gray-600", label: "Unknown" };
  const style = stateStyles[status.state] ?? defaultStyle;
  const tunnelSummary = status.tunnels.length > 0
    ? status.tunnels.map((t) => {
        const healthy = t.status === "healthy";
        return (
          <span
            key={t.label}
            className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium ${
              healthy ? "bg-green-50 text-green-700" : "bg-red-50 text-red-700"
            }`}
          >
            <span className={`w-1.5 h-1.5 rounded-full ${healthy ? "bg-green-500" : "bg-red-500"}`} />
            {t.label}
          </span>
        );
      })
    : <span className="text-xs text-gray-400">No active tunnels</span>;

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-sm font-medium text-gray-900">SSH Connection</h3>
        <button
          onClick={onRefresh}
          disabled={isLoading}
          className="p-1 text-gray-400 hover:text-gray-600 rounded disabled:opacity-50"
          title="Refresh"
        >
          <RefreshCw size={14} className={isLoading ? "animate-spin" : ""} />
        </button>
      </div>

      <div className="grid grid-cols-2 gap-y-3 gap-x-8">
        <div>
          <dt className="text-xs text-gray-500">State</dt>
          <dd className="flex items-center gap-1.5 mt-0.5">
            <span className={`w-2 h-2 rounded-full ${style.dot}`} />
            <span className={`text-sm font-medium ${style.text}`}>{style.label}</span>
          </dd>
        </div>
        <div>
          <dt className="text-xs text-gray-500">Uptime</dt>
          <dd className="text-sm text-gray-900 mt-0.5">
            {status.metrics?.uptime ?? "—"}
          </dd>
        </div>
        <div>
          <dt className="text-xs text-gray-500">Last Health Check</dt>
          <dd className="text-sm text-gray-900 mt-0.5">
            {status.metrics ? formatTime(status.metrics.last_health_check) : "—"}
          </dd>
        </div>
        <div>
          <dt className="text-xs text-gray-500">Health Checks</dt>
          <dd className="text-sm text-gray-900 mt-0.5">
            {status.metrics
              ? `${status.metrics.successful_checks} ok / ${status.metrics.failed_checks} failed`
              : "—"}
          </dd>
        </div>
        <div className="col-span-2">
          <dt className="text-xs text-gray-500 mb-1">Active Tunnels</dt>
          <dd className="flex flex-wrap gap-2">{tunnelSummary}</dd>
        </div>
      </div>
    </div>
  );
}
