import { RefreshCw } from "lucide-react";
import { formatDistanceToNow } from "date-fns";
import { useChannelHealth } from "@common/hooks/useChannelHealth";
import type { ChannelAccountHealth, ChannelHealthSummary } from "@common/types/channel";
import type { Instance } from "@common/types/instance";

const overallStyles: Record<string, string> = {
  healthy: "bg-green-100 text-green-800",
  degraded: "bg-yellow-100 text-yellow-800",
  unhealthy: "bg-red-100 text-red-800",
  unreachable: "bg-red-100 text-red-800",
  no_channels: "bg-gray-100 text-gray-800",
  unknown: "bg-gray-100 text-gray-800",
};

const channelStatusStyles: Record<string, string> = {
  healthy: "bg-green-100 text-green-800",
  stale: "bg-yellow-100 text-yellow-800",
  disconnected: "bg-red-100 text-red-800",
  not_running: "bg-red-100 text-red-800",
  disabled: "bg-gray-100 text-gray-800",
  unknown: "bg-gray-100 text-gray-800",
};

function statusLabel(status: string): string {
  return status.replace(/_/g, " ");
}

function capitalize(s: string): string {
  return s ? s.charAt(0).toUpperCase() + s.slice(1) : s;
}

function relativeTime(ts: string | null): string | null {
  if (!ts) return null;
  const d = new Date(ts);
  if (isNaN(d.getTime())) return null;
  return formatDistanceToNow(d, { addSuffix: true });
}

function ChannelRow({ ch }: { ch: ChannelAccountHealth }) {
  const badgeStyle = channelStatusStyles[ch.status] ?? "bg-gray-100 text-gray-800";
  const lastEvent = relativeTime(ch.last_event_at);
  return (
    <div className="py-2.5 first:pt-0 last:pb-0">
      <div className="flex items-center gap-2 flex-wrap">
        <span className="text-sm font-medium text-gray-900">
          {capitalize(ch.channel)}
          {ch.account_id && ch.account_id !== "default" && (
            <span className="font-normal text-gray-500"> ({ch.account_id})</span>
          )}
        </span>
        <span
          className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${badgeStyle}`}
        >
          {statusLabel(ch.status)}
        </span>
        {ch.mode && <span className="text-xs text-gray-500">{ch.mode}</span>}
        <span className="ml-auto text-xs text-gray-500">
          {lastEvent ? `last event ${lastEvent}` : "no events yet"}
        </span>
      </div>
      {(ch.last_error || ch.reconnect_attempts > 0) && (
        <div className="mt-1 flex items-center gap-3 flex-wrap">
          {ch.last_error && <span className="text-xs text-red-600">{ch.last_error}</span>}
          {ch.reconnect_attempts > 0 && (
            <span className="text-xs text-gray-500">
              {ch.reconnect_attempts} reconnect attempt{ch.reconnect_attempts === 1 ? "" : "s"}
            </span>
          )}
        </div>
      )}
    </div>
  );
}

export default function ChannelHealthPanel({ instanceId }: { instanceId: number }) {
  const health = useChannelHealth(instanceId);

  if (health.isLoading && !health.data) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="text-sm text-gray-500">Loading channel health...</div>
      </div>
    );
  }

  if (health.isError && !health.data) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="flex items-center justify-between">
          <div className="text-sm text-red-600">Failed to load channel health.</div>
          <button
            onClick={() => health.refetch()}
            className="p-1 text-gray-400 hover:text-gray-600 rounded"
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
        </div>
      </div>
    );
  }

  if (!health.data) return null;

  const data = health.data;

  // Monitoring is turned off server-side — hide the panel entirely.
  if (data.overall === "disabled") return null;

  const overallStyle = overallStyles[data.overall] ?? "bg-gray-100 text-gray-800";
  const checkedAt = relativeTime(data.checked_at);

  let body;
  if (data.overall === "no_channels") {
    body = <p className="text-sm text-gray-400 italic">No channels configured</p>;
  } else if (data.overall === "unreachable") {
    body = (
      <p className="text-sm text-red-600">
        Gateway unreachable — the OpenClaw process may be down
      </p>
    );
  } else if (data.overall === "unknown") {
    body = <p className="text-sm text-gray-400 italic">Waiting for first health check…</p>;
  } else {
    body = (
      <div className="divide-y divide-gray-100">
        {data.channels.map((ch) => (
          <ChannelRow key={`${ch.channel}/${ch.account_id}`} ch={ch} />
        ))}
      </div>
    );
  }

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2">
          <h3 className="text-sm font-medium text-gray-900">Channel Health</h3>
          <span
            className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${overallStyle}`}
          >
            {statusLabel(data.overall)}
          </span>
        </div>
        <div className="flex items-center gap-2">
          {checkedAt && <span className="text-xs text-gray-400">checked {checkedAt}</span>}
          <button
            onClick={() => health.refetch()}
            disabled={health.isFetching}
            className="p-1 text-gray-400 hover:text-gray-600 rounded disabled:opacity-50"
            title="Refresh"
          >
            <RefreshCw size={14} className={health.isFetching ? "animate-spin" : ""} />
          </button>
        </div>
      </div>
      {body}
    </div>
  );
}

/**
 * Compact warning indicator for agent list rows/cards. Renders only when the
 * instance's channel health summary is "unhealthy" or "unreachable".
 */
export function ChannelHealthIndicator({ instance }: { instance: Instance }) {
  const summary: ChannelHealthSummary | null | undefined = instance.channel_health;
  if (!summary) return null;
  if (summary.overall !== "unhealthy" && summary.overall !== "unreachable") return null;

  const tooltip =
    summary.overall === "unreachable"
      ? "Gateway unreachable — the OpenClaw process may be down"
      : `${summary.unhealthy_count} channel${summary.unhealthy_count === 1 ? "" : "s"} not responding`;

  return (
    <span className="relative group">
      <span
        data-testid="channel-health-indicator"
        className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs font-medium bg-red-100 text-red-800"
      >
        <span className="w-1.5 h-1.5 rounded-full bg-red-500" />
        Warning
      </span>
      <span className="pointer-events-none absolute bottom-full left-1/2 -translate-x-1/2 mb-2 whitespace-pre rounded bg-gray-900 px-2.5 py-1.5 text-xs text-white opacity-0 transition-opacity group-hover:opacity-100 z-50">
        {tooltip}
      </span>
    </span>
  );
}
