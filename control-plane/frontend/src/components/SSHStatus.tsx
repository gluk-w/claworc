import { RefreshCw } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { formatDistanceToNow } from "date-fns";
import { useSSHStatus } from "@/hooks/useSSH";
import type { SSHConnectionState } from "@/types/ssh";

const stateStyles: Record<SSHConnectionState, { dot: string; badge: string }> =
  {
    connected: {
      dot: "bg-green-500",
      badge: "bg-green-100 text-green-800",
    },
    connecting: {
      dot: "bg-yellow-500",
      badge: "bg-yellow-100 text-yellow-800",
    },
    reconnecting: {
      dot: "bg-yellow-500",
      badge: "bg-yellow-100 text-yellow-800",
    },
    disconnected: {
      dot: "bg-gray-400",
      badge: "bg-gray-100 text-gray-800",
    },
    failed: {
      dot: "bg-red-500",
      badge: "bg-red-100 text-red-800",
    },
  };

function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  const m = Math.floor(seconds / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  const rm = m % 60;
  if (h < 24) return `${h}h ${rm}m`;
  const d = Math.floor(h / 24);
  const rh = h % 24;
  return `${d}d ${rh}h`;
}

function formatTime(iso: string): string {
  if (!iso) return "—";
  try {
    return formatDistanceToNow(new Date(iso), { addSuffix: true });
  } catch {
    return "—";
  }
}

interface SSHStatusProps {
  instanceId: number;
  enabled?: boolean;
}

export default function SSHStatus({
  instanceId,
  enabled = true,
}: SSHStatusProps) {
  const queryClient = useQueryClient();
  const { data, isLoading, isError, isFetching } = useSSHStatus(
    instanceId,
    enabled,
  );

  const handleRefresh = () => {
    queryClient.invalidateQueries({ queryKey: ["ssh-status", instanceId] });
  };

  if (isLoading) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="animate-pulse space-y-3">
          <div className="h-4 bg-gray-200 rounded w-1/3" />
          <div className="h-3 bg-gray-200 rounded w-1/2" />
          <div className="h-3 bg-gray-200 rounded w-2/5" />
        </div>
      </div>
    );
  }

  if (isError) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="flex items-center justify-between">
          <p className="text-sm text-red-600">
            Failed to load SSH connection status.
          </p>
          <button
            onClick={handleRefresh}
            className="text-sm text-blue-600 hover:text-blue-800"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  if (!data) return null;

  const state = data.connection_state;
  const style = stateStyles[state] ?? stateStyles.disconnected;

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-sm font-medium text-gray-900">SSH Connection</h3>
        <button
          onClick={handleRefresh}
          disabled={isFetching}
          className="p-1 text-gray-400 hover:text-gray-600 disabled:opacity-50"
          title="Refresh SSH status"
        >
          <RefreshCw
            size={14}
            className={isFetching ? "animate-spin" : ""}
          />
        </button>
      </div>

      <div className="space-y-3">
        {/* Connection state */}
        <div className="flex items-center gap-2">
          <span className={`h-2.5 w-2.5 rounded-full ${style.dot}`} />
          <span
            className={`inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium ${style.badge}`}
          >
            {state}
          </span>
        </div>

        {/* Health metrics */}
        {data.health && (
          <div className="grid grid-cols-2 gap-y-2 gap-x-8 text-sm">
            <div>
              <dt className="text-xs text-gray-500">Uptime</dt>
              <dd className="text-gray-900 mt-0.5">
                {formatUptime(data.health.uptime_seconds)}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-gray-500">Last Health Check</dt>
              <dd className="text-gray-900 mt-0.5">
                {formatTime(data.health.last_health_check)}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-gray-500">Checks (OK / Failed)</dt>
              <dd className="text-gray-900 mt-0.5">
                {data.health.successful_checks} / {data.health.failed_checks}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-gray-500">Connected Since</dt>
              <dd className="text-gray-900 mt-0.5">
                {formatTime(data.health.connected_at)}
              </dd>
            </div>
          </div>
        )}

        {/* Active tunnels summary */}
        {data.tunnels.length > 0 && (
          <div>
            <dt className="text-xs text-gray-500 mb-1">Active Tunnels</dt>
            <div className="flex gap-2">
              {data.tunnels.map((t) => (
                <span
                  key={`${t.service}-${t.local_port}`}
                  className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs font-medium ${
                    t.healthy
                      ? "bg-green-50 text-green-700"
                      : "bg-red-50 text-red-700"
                  }`}
                >
                  <span
                    className={`h-1.5 w-1.5 rounded-full ${
                      t.healthy ? "bg-green-500" : "bg-red-500"
                    }`}
                  />
                  {t.service}
                </span>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
