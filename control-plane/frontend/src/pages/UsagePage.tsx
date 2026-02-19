import { useState } from "react";
import { useUsage, useProxyStatus } from "@/hooks/useUsage";

type GroupBy = "instance" | "provider" | "model" | "day" | "";
type Period = "7d" | "30d" | "90d" | "all";

function getDateRange(period: Period): { since?: string; until?: string } {
  if (period === "all") return {};
  const now = new Date();
  const days = period === "7d" ? 7 : period === "30d" ? 30 : 90;
  const since = new Date(now.getTime() - days * 86400000);
  return { since: since.toISOString().slice(0, 10) };
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(1) + "K";
  return String(n);
}

export default function UsagePage() {
  const { data: proxyStatus } = useProxyStatus();
  const [groupBy, setGroupBy] = useState<GroupBy>("instance");
  const [period, setPeriod] = useState<Period>("30d");

  const dateRange = getDateRange(period);
  const { data: usage, isLoading } = useUsage({
    ...dateRange,
    group_by: groupBy || undefined,
  });

  if (proxyStatus && !proxyStatus.proxy_enabled) {
    return (
      <div className="text-center py-12 text-gray-500">
        <p className="text-lg font-medium mb-2">LLM Proxy not enabled</p>
        <p className="text-sm">
          Set <code className="bg-gray-100 px-1.5 py-0.5 rounded text-xs">CLAWORC_PROXY_ENABLED=true</code> to enable usage tracking.
        </p>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-xl font-semibold text-gray-900">Usage</h1>
        <div className="flex items-center gap-3">
          <select
            value={period}
            onChange={(e) => setPeriod(e.target.value as Period)}
            className="text-sm border border-gray-300 rounded-md px-2 py-1.5"
          >
            <option value="7d">Last 7 days</option>
            <option value="30d">Last 30 days</option>
            <option value="90d">Last 90 days</option>
            <option value="all">All time</option>
          </select>
          <select
            value={groupBy}
            onChange={(e) => setGroupBy(e.target.value as GroupBy)}
            className="text-sm border border-gray-300 rounded-md px-2 py-1.5"
          >
            <option value="instance">By Instance</option>
            <option value="provider">By Provider</option>
            <option value="model">By Model</option>
            <option value="day">By Day</option>
            <option value="">Total</option>
          </select>
        </div>
      </div>

      {isLoading ? (
        <div className="text-center py-12 text-gray-500">Loading...</div>
      ) : !usage || usage.length === 0 ? (
        <div className="text-center py-12 text-gray-500">No usage data yet.</div>
      ) : (
        <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="bg-gray-50 border-b border-gray-200">
                <th className="text-left px-4 py-3 font-medium text-gray-600">
                  {groupBy === "day" ? "Date" : groupBy === "" ? "" : groupBy.charAt(0).toUpperCase() + groupBy.slice(1)}
                </th>
                <th className="text-right px-4 py-3 font-medium text-gray-600">Requests</th>
                <th className="text-right px-4 py-3 font-medium text-gray-600">Input Tokens</th>
                <th className="text-right px-4 py-3 font-medium text-gray-600">Output Tokens</th>
                <th className="text-right px-4 py-3 font-medium text-gray-600">Est. Cost</th>
              </tr>
            </thead>
            <tbody>
              {usage.map((row, i) => (
                <tr key={i} className="border-b border-gray-100 last:border-0">
                  <td className="px-4 py-3 text-gray-900 font-medium">{row.group}</td>
                  <td className="px-4 py-3 text-right text-gray-700">{row.requests.toLocaleString()}</td>
                  <td className="px-4 py-3 text-right text-gray-700">{formatTokens(row.input_tokens)}</td>
                  <td className="px-4 py-3 text-right text-gray-700">{formatTokens(row.output_tokens)}</td>
                  <td className="px-4 py-3 text-right text-gray-700 font-medium">{row.estimated_cost_usd}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
