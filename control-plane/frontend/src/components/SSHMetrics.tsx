import { useState } from "react";
import { ChevronDown, ChevronRight, BarChart3 } from "lucide-react";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Cell,
} from "recharts";
import { useSSHMetrics } from "@/hooks/useSSH";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type TooltipFormatter = any;

const HEALTH_COLORS = {
  good: "#22c55e",
  warning: "#eab308",
  bad: "#ef4444",
};

function getHealthColor(rate: number): string {
  if (rate >= 0.95) return HEALTH_COLORS.good;
  if (rate >= 0.8) return HEALTH_COLORS.warning;
  return HEALTH_COLORS.bad;
}

export default function SSHMetrics() {
  const { data, isLoading } = useSSHMetrics();
  const [expanded, setExpanded] = useState(false);

  if (isLoading || !data) return null;

  const hasData =
    data.uptime_buckets.some((b) => b.count > 0) ||
    data.health_rates.length > 0 ||
    data.reconnection_counts.length > 0;

  if (!hasData) return null;

  return (
    <div className="bg-white border border-gray-200 rounded-lg">
      <button
        onClick={() => setExpanded((e) => !e)}
        className="w-full flex items-center justify-between px-4 py-3 text-left hover:bg-gray-50 transition-colors"
      >
        <span className="flex items-center gap-2 text-sm font-medium text-gray-700">
          <BarChart3 size={16} />
          Connection Metrics
        </span>
        {expanded ? (
          <ChevronDown size={16} className="text-gray-400" />
        ) : (
          <ChevronRight size={16} className="text-gray-400" />
        )}
      </button>

      {expanded && (
        <div className="px-4 pb-4 space-y-6">
          {/* Uptime Distribution */}
          {data.uptime_buckets.some((b) => b.count > 0) && (
            <div>
              <h3 className="text-xs font-medium text-gray-500 uppercase mb-3">
                Connection Uptime Distribution
              </h3>
              <div className="h-48">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart data={data.uptime_buckets}>
                    <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" />
                    <XAxis
                      dataKey="label"
                      tick={{ fontSize: 12, fill: "#6b7280" }}
                    />
                    <YAxis
                      allowDecimals={false}
                      tick={{ fontSize: 12, fill: "#6b7280" }}
                    />
                    <Tooltip
                      contentStyle={{
                        fontSize: 12,
                        borderRadius: 6,
                        border: "1px solid #e5e7eb",
                      }}
                      formatter={((value: number | string) => [
                        `${value} instance${value !== 1 ? "s" : ""}`,
                        "Count",
                      ]) as TooltipFormatter}
                    />
                    <Bar dataKey="count" fill="#3b82f6" radius={[4, 4, 0, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </div>
          )}

          {/* Health Check Success Rate */}
          {data.health_rates.length > 0 && (
            <div>
              <h3 className="text-xs font-medium text-gray-500 uppercase mb-3">
                Health Check Success Rate
              </h3>
              <div className="h-48">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart
                    data={data.health_rates.map((r) => ({
                      name: r.display_name,
                      rate: Math.round(r.success_rate * 100),
                      total: r.total_checks,
                      rawRate: r.success_rate,
                    }))}
                    layout="vertical"
                  >
                    <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" />
                    <XAxis
                      type="number"
                      domain={[0, 100]}
                      tick={{ fontSize: 12, fill: "#6b7280" }}
                      tickFormatter={(v) => `${v}%`}
                    />
                    <YAxis
                      type="category"
                      dataKey="name"
                      tick={{ fontSize: 12, fill: "#6b7280" }}
                      width={120}
                    />
                    <Tooltip
                      contentStyle={{
                        fontSize: 12,
                        borderRadius: 6,
                        border: "1px solid #e5e7eb",
                      }}
                      formatter={((value: number | string, _name: string, props: { payload: { total: number } }) => [
                        `${value}% (${props.payload.total} checks)`,
                        "Success Rate",
                      ]) as TooltipFormatter}
                    />
                    <Bar dataKey="rate" radius={[0, 4, 4, 0]}>
                      {data.health_rates.map((entry, index) => (
                        <Cell
                          key={index}
                          fill={getHealthColor(entry.success_rate)}
                        />
                      ))}
                    </Bar>
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </div>
          )}

          {/* Reconnection Counts */}
          {data.reconnection_counts.length > 0 && (
            <div>
              <h3 className="text-xs font-medium text-gray-500 uppercase mb-3">
                Reconnection Attempts
              </h3>
              <div className="h-48">
                <ResponsiveContainer width="100%" height="100%">
                  <BarChart
                    data={data.reconnection_counts.map((r) => ({
                      name: r.display_name,
                      count: r.count,
                    }))}
                    layout="vertical"
                  >
                    <CartesianGrid strokeDasharray="3 3" stroke="#e5e7eb" />
                    <XAxis
                      type="number"
                      allowDecimals={false}
                      tick={{ fontSize: 12, fill: "#6b7280" }}
                    />
                    <YAxis
                      type="category"
                      dataKey="name"
                      tick={{ fontSize: 12, fill: "#6b7280" }}
                      width={120}
                    />
                    <Tooltip
                      contentStyle={{
                        fontSize: 12,
                        borderRadius: 6,
                        border: "1px solid #e5e7eb",
                      }}
                      formatter={((value: number | string) => [
                        `${value} attempt${value !== 1 ? "s" : ""}`,
                        "Reconnections",
                      ]) as TooltipFormatter}
                    />
                    <Bar
                      dataKey="count"
                      fill="#f59e0b"
                      radius={[0, 4, 4, 0]}
                    />
                  </BarChart>
                </ResponsiveContainer>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
