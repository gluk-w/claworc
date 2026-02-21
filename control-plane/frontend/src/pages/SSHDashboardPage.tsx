import { useState, useMemo } from "react";
import { Link } from "react-router-dom";
import { formatDistanceToNow } from "date-fns";
import {
  Wifi,
  WifiOff,
  RefreshCw,
  AlertTriangle,
  ArrowUpDown,
  ChevronUp,
  ChevronDown,
} from "lucide-react";
import { useGlobalSSHStatus } from "@/hooks/useSSH";
import type {
  GlobalSSHInstanceStatus,
  SSHConnectionState,
} from "@/types/ssh";

type SortField =
  | "display_name"
  | "instance_status"
  | "connection_state"
  | "tunnel_count"
  | "uptime";
type SortDir = "asc" | "desc";

const stateStyles: Record<
  SSHConnectionState,
  { dot: string; badge: string }
> = {
  connected: { dot: "bg-green-500", badge: "bg-green-100 text-green-800" },
  connecting: { dot: "bg-yellow-500", badge: "bg-yellow-100 text-yellow-800" },
  reconnecting: {
    dot: "bg-yellow-500",
    badge: "bg-yellow-100 text-yellow-800",
  },
  disconnected: { dot: "bg-gray-400", badge: "bg-gray-100 text-gray-800" },
  failed: { dot: "bg-red-500", badge: "bg-red-100 text-red-800" },
};

function formatUptime(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return m > 0 ? `${h}h ${m}m` : `${h}h`;
}

function StatCard({
  label,
  count,
  icon,
  color,
}: {
  label: string;
  count: number;
  icon: React.ReactNode;
  color: string;
}) {
  return (
    <div className={`rounded-lg border p-4 ${color}`}>
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm font-medium opacity-75">{label}</p>
          <p className="text-2xl font-bold">{count}</p>
        </div>
        <div className="opacity-50">{icon}</div>
      </div>
    </div>
  );
}

export default function SSHDashboardPage() {
  const { data, isLoading, error } = useGlobalSSHStatus();
  const [sortField, setSortField] = useState<SortField>("display_name");
  const [sortDir, setSortDir] = useState<SortDir>("asc");
  const [filter, setFilter] = useState<"all" | SSHConnectionState>("all");

  const handleSort = (field: SortField) => {
    if (sortField === field) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortField(field);
      setSortDir("asc");
    }
  };

  const SortIcon = ({ field }: { field: SortField }) => {
    if (sortField !== field)
      return <ArrowUpDown size={14} className="opacity-30" />;
    return sortDir === "asc" ? (
      <ChevronUp size={14} />
    ) : (
      <ChevronDown size={14} />
    );
  };

  const filteredAndSorted = useMemo(() => {
    if (!data) return [];
    let items = data.instances;
    if (filter !== "all") {
      items = items.filter((i) => i.connection_state === filter);
    }

    const stateOrder: Record<string, number> = {
      connected: 0,
      reconnecting: 1,
      connecting: 2,
      failed: 3,
      disconnected: 4,
    };

    return [...items].sort((a, b) => {
      let cmp = 0;
      switch (sortField) {
        case "display_name":
          cmp = a.display_name.localeCompare(b.display_name);
          break;
        case "instance_status":
          cmp = a.instance_status.localeCompare(b.instance_status);
          break;
        case "connection_state":
          cmp =
            (stateOrder[a.connection_state] ?? 99) -
            (stateOrder[b.connection_state] ?? 99);
          break;
        case "tunnel_count":
          cmp = a.tunnel_count - b.tunnel_count;
          break;
        case "uptime":
          cmp =
            (a.health?.uptime_seconds ?? 0) - (b.health?.uptime_seconds ?? 0);
          break;
      }
      return sortDir === "asc" ? cmp : -cmp;
    });
  }, [data, sortField, sortDir, filter]);

  if (isLoading) {
    return (
      <div className="text-center py-12 text-gray-500">
        Loading SSH status...
      </div>
    );
  }

  if (error) {
    return (
      <div className="text-center py-12 text-red-600">
        Failed to load SSH status. Please try again.
      </div>
    );
  }

  if (!data) return null;

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold text-gray-900">
        SSH Connection Dashboard
      </h1>

      {/* Quick stats */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <StatCard
          label="Connected"
          count={data.connected}
          icon={<Wifi size={24} />}
          color="bg-green-50 border-green-200 text-green-900"
        />
        <StatCard
          label="Reconnecting"
          count={data.reconnecting}
          icon={<RefreshCw size={24} />}
          color="bg-yellow-50 border-yellow-200 text-yellow-900"
        />
        <StatCard
          label="Failed"
          count={data.failed}
          icon={<AlertTriangle size={24} />}
          color="bg-red-50 border-red-200 text-red-900"
        />
        <StatCard
          label="Disconnected"
          count={data.disconnected}
          icon={<WifiOff size={24} />}
          color="bg-gray-50 border-gray-200 text-gray-900"
        />
      </div>

      {/* Filter */}
      <div className="flex items-center gap-2">
        <span className="text-sm text-gray-500">Filter:</span>
        {(
          ["all", "connected", "reconnecting", "failed", "disconnected"] as const
        ).map((f) => (
          <button
            key={f}
            onClick={() => setFilter(f)}
            className={`px-3 py-1 text-xs rounded-full font-medium transition-colors ${
              filter === f
                ? "bg-blue-100 text-blue-800"
                : "bg-gray-100 text-gray-600 hover:bg-gray-200"
            }`}
          >
            {f === "all" ? "All" : f.charAt(0).toUpperCase() + f.slice(1)}
            {f !== "all" && (
              <span className="ml-1">
                (
                {f === "connected"
                  ? data.connected
                  : f === "reconnecting"
                    ? data.reconnecting
                    : f === "failed"
                      ? data.failed
                      : data.disconnected}
                )
              </span>
            )}
          </button>
        ))}
      </div>

      {/* Instance table */}
      <div className="bg-white border border-gray-200 rounded-lg overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-gray-200 bg-gray-50">
                <SortableHeader
                  label="Instance"
                  field="display_name"
                  currentField={sortField}
                  onSort={handleSort}
                  sortIcon={<SortIcon field="display_name" />}
                />
                <SortableHeader
                  label="Status"
                  field="instance_status"
                  currentField={sortField}
                  onSort={handleSort}
                  sortIcon={<SortIcon field="instance_status" />}
                />
                <SortableHeader
                  label="SSH"
                  field="connection_state"
                  currentField={sortField}
                  onSort={handleSort}
                  sortIcon={<SortIcon field="connection_state" />}
                />
                <SortableHeader
                  label="Tunnels"
                  field="tunnel_count"
                  currentField={sortField}
                  onSort={handleSort}
                  sortIcon={<SortIcon field="tunnel_count" />}
                />
                <SortableHeader
                  label="Uptime"
                  field="uptime"
                  currentField={sortField}
                  onSort={handleSort}
                  sortIcon={<SortIcon field="uptime" />}
                />
                <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                  Last Check
                </th>
                <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase">
                  Health
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-200">
              {filteredAndSorted.length === 0 ? (
                <tr>
                  <td
                    colSpan={7}
                    className="px-4 py-8 text-center text-gray-500"
                  >
                    {filter === "all"
                      ? "No instances found."
                      : `No instances with ${filter} SSH status.`}
                  </td>
                </tr>
              ) : (
                filteredAndSorted.map((inst) => (
                  <InstanceRow key={inst.instance_id} instance={inst} />
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

function SortableHeader({
  label,
  field,
  currentField,
  onSort,
  sortIcon,
}: {
  label: string;
  field: SortField;
  currentField: SortField;
  onSort: (f: SortField) => void;
  sortIcon: React.ReactNode;
}) {
  return (
    <th
      className={`px-4 py-3 text-left text-xs font-medium uppercase cursor-pointer select-none hover:bg-gray-100 transition-colors ${
        currentField === field ? "text-blue-700" : "text-gray-500"
      }`}
      onClick={() => onSort(field)}
    >
      <span className="flex items-center gap-1">
        {label}
        {sortIcon}
      </span>
    </th>
  );
}

function InstanceRow({ instance }: { instance: GlobalSSHInstanceStatus }) {
  const style =
    stateStyles[instance.connection_state] ?? stateStyles.disconnected;

  const lastCheck = instance.health?.last_health_check
    ? formatDistanceToNow(new Date(instance.health.last_health_check), {
        addSuffix: true,
      })
    : "-";

  const uptime =
    instance.health && instance.health.uptime_seconds > 0
      ? formatUptime(instance.health.uptime_seconds)
      : "-";

  const healthLabel =
    instance.health != null
      ? instance.health.healthy
        ? "Healthy"
        : "Unhealthy"
      : "-";
  const healthColor =
    instance.health != null
      ? instance.health.healthy
        ? "text-green-700"
        : "text-red-700"
      : "text-gray-400";

  const tunnelLabel =
    instance.tunnel_count > 0
      ? `${instance.healthy_tunnels}/${instance.tunnel_count}`
      : "-";

  return (
    <tr className="hover:bg-gray-50 transition-colors">
      <td className="px-4 py-3">
        <Link
          to={`/instances/${instance.instance_id}`}
          className="text-sm font-medium text-blue-600 hover:text-blue-800 hover:underline"
        >
          {instance.display_name}
        </Link>
      </td>
      <td className="px-4 py-3">
        <span className="text-sm text-gray-700 capitalize">
          {instance.instance_status}
        </span>
      </td>
      <td className="px-4 py-3">
        <span
          className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium ${style.badge}`}
        >
          <span className={`h-1.5 w-1.5 rounded-full ${style.dot}`} />
          {instance.connection_state}
        </span>
      </td>
      <td className="px-4 py-3 text-sm text-gray-700">{tunnelLabel}</td>
      <td className="px-4 py-3 text-sm text-gray-700">{uptime}</td>
      <td className="px-4 py-3 text-sm text-gray-500">{lastCheck}</td>
      <td className="px-4 py-3">
        <span className={`text-sm font-medium ${healthColor}`}>
          {healthLabel}
        </span>
      </td>
    </tr>
  );
}
