import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useUsageStats, useResetUsageLogs } from "@/hooks/useProviders";
import InstanceTeamPicker, {
  type SingleSelection,
} from "@/components/InstanceTeamPicker";
import {
  ResponsiveContainer,
  AreaChart,
  Area,
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
} from "recharts";

function formatCost(v: number) {
  return `$${v.toFixed(2)}`;
}

function formatTokens(v: number) {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}K`;
  return String(v);
}

function today() {
  return new Date().toISOString().slice(0, 10);
}

function daysAgo(n: number) {
  const d = new Date();
  d.setDate(d.getDate() - n);
  return d.toISOString().slice(0, 10);
}

export default function UsagePage() {
  const [searchParams, setSearchParams] = useSearchParams();

  const startDate = searchParams.get("start") ?? daysAgo(30);
  const endDate = searchParams.get("end") ?? today();
  const instanceId = searchParams.get("instance") ? Number(searchParams.get("instance")) : undefined;
  const providerId = searchParams.get("provider") ? Number(searchParams.get("provider")) : undefined;
  const teamId = searchParams.get("team") ? Number(searchParams.get("team")) : undefined;

  function updateParams(updates: Record<string, string | undefined>) {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      for (const [k, v] of Object.entries(updates)) {
        if (v === undefined) next.delete(k);
        else next.set(k, v);
      }
      return next;
    }, { replace: true });
  }

  const resetMutation = useResetUsageLogs();

  const { data: stats, isLoading } = useUsageStats({
    start_date: startDate,
    end_date: endDate,
    instance_id: instanceId,
    provider_id: providerId,
    team_id: teamId,
  });

  const pickerInstances = stats?.instances ?? [];
  const pickerTeams = stats?.teams ?? [];
  const pickerSelected: SingleSelection = teamId
    ? { kind: "team", teamId }
    : instanceId
      ? { kind: "instance", instanceId }
      : { kind: "all" };

  const handlePickerChange = (sel: SingleSelection) => {
    if (sel.kind === "all") {
      updateParams({ instance: undefined, team: undefined });
    } else if (sel.kind === "team") {
      updateParams({ team: String(sel.teamId), instance: undefined });
    } else {
      updateParams({ instance: String(sel.instanceId), team: undefined });
    }
  };

  const granularity = stats?.granularity ?? "day";

  function formatTimeLabel(label: string): string {
    if (granularity === "minute") return label.slice(11); // "HH:MM"
    if (granularity === "hour") return label.slice(5, 10) + " " + label.slice(11) + ":00"; // "MM-DD HH:00"
    return label.slice(5); // "MM-DD"
  }

  function formatTimeTooltip(label: string): string {
    if (granularity === "minute") return label.replace("T", " ") + ":00";
    if (granularity === "hour") return label.replace("T", " ") + ":00";
    return `Date: ${label}`;
  }

  const total = stats?.total;
  const timeSeries = stats?.time_series ?? [];
  const byInstance = (stats?.by_instance ?? []).map((s) => ({
    ...s,
    _label: s.instance_display_name || s.instance_name,
  }));
  const byProvider = stats?.by_provider ?? [];
  const byModel = (stats?.by_model ?? []).slice(0, 10);

  return (
    <div>
      <h1 className="text-xl font-semibold text-gray-900 mb-6">AI Usage</h1>

      <div className="space-y-6">

      {/* Filters */}
      <div className="flex flex-wrap items-end gap-4 bg-white border border-gray-200 rounded-lg p-4 shadow-sm">
        <div className="flex flex-col gap-1">
          <label className="text-xs font-medium text-gray-500">From</label>
          <input
            type="date"
            value={startDate}
            max={endDate}
            onChange={(e) => updateParams({ start: e.target.value })}
            className="border border-gray-300 rounded-md px-3 py-1.5 text-sm text-gray-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-xs font-medium text-gray-500">To</label>
          <input
            type="date"
            value={endDate}
            min={startDate}
            onChange={(e) => updateParams({ end: e.target.value })}
            className="border border-gray-300 rounded-md px-3 py-1.5 text-sm text-gray-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-xs font-medium text-gray-500">Instance</label>
          <InstanceTeamPicker
            mode="single"
            instances={pickerInstances}
            teams={pickerTeams}
            selected={pickerSelected}
            onChange={handlePickerChange}
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-xs font-medium text-gray-500">Provider</label>
          <select
            value={providerId ?? ""}
            onChange={(e) => updateParams({ provider: e.target.value || undefined })}
            className="border border-gray-300 rounded-md px-3 py-1.5 text-sm text-gray-700 focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            <option value="">All providers</option>
            {(stats?.providers ?? []).map((p) => (
              <option key={p.id} value={p.id}>
                {p.name || p.key}
              </option>
            ))}
          </select>
        </div>
        <span className="relative group self-end ml-auto">
          <button
            onClick={() => {
              if (window.confirm("Delete all usage logs? This cannot be undone.")) {
                resetMutation.mutate();
              }
            }}
            disabled={resetMutation.isPending}
            className="px-3 py-1.5 text-sm font-medium text-white bg-red-600 hover:bg-red-700 disabled:opacity-50 rounded-md"
          >
            Reset
          </button>
          <span className="pointer-events-none absolute bottom-full left-1/2 -translate-x-1/2 mb-2 whitespace-nowrap rounded bg-gray-900 px-2.5 py-1.5 text-xs text-white opacity-0 transition-opacity group-hover:opacity-100 z-50">
            Delete the usage information
          </span>
        </span>
      </div>

      {isLoading ? (
        <div className="text-sm text-gray-500">Loading...</div>
      ) : (
        <>
          {/* Summary cards */}
          <div className="grid grid-cols-2 sm:grid-cols-5 gap-4">
            {[
              { label: "Total Requests", value: total?.total_requests.toLocaleString() ?? "0" },
              {
                label: "Input Tokens",
                value: formatTokens(total?.input_tokens ?? 0),
              },
              {
                label: "Cached Input Tokens",
                value: formatTokens(total?.cached_input_tokens ?? 0),
              },
              {
                label: "Output Tokens",
                value: formatTokens(total?.output_tokens ?? 0),
              },
              { label: "Total Cost", value: formatCost(total?.cost_usd ?? 0) },
            ].map(({ label, value }) => (
              <div
                key={label}
                className="bg-white border border-gray-200 rounded-lg p-4 shadow-sm"
              >
                <div className="text-xs font-medium text-gray-500">{label}</div>
                <div className="text-2xl font-semibold text-gray-900 mt-1">{value}</div>
              </div>
            ))}
          </div>

          {/* Charts */}
          <div className="grid grid-cols-1 xl:grid-cols-2 gap-6">
            {/* Requests over time */}
            <div className="bg-white border border-gray-200 rounded-lg p-4 shadow-sm">
              <h2 className="text-sm font-semibold text-gray-700 mb-3">Requests over time</h2>
              <ResponsiveContainer width="100%" height={220}>
                <AreaChart data={timeSeries}>
                  <defs>
                    <linearGradient id="reqGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#3b82f6" stopOpacity={0.3} />
                      <stop offset="95%" stopColor="#3b82f6" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
                  <XAxis dataKey="date" tick={{ fontSize: 11 }} tickFormatter={formatTimeLabel} />
                  <YAxis tick={{ fontSize: 11 }} />
                  <Tooltip
                    formatter={(v: number) => [v.toLocaleString(), "Requests"]}
                    labelFormatter={formatTimeTooltip}
                  />
                  <Area
                    type="monotone"
                    dataKey="total_requests"
                    stroke="#3b82f6"
                    fill="url(#reqGrad)"
                    name="Requests"
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>

            {/* Cost over time */}
            <div className="bg-white border border-gray-200 rounded-lg p-4 shadow-sm">
              <h2 className="text-sm font-semibold text-gray-700 mb-3">Cost over time (USD)</h2>
              <ResponsiveContainer width="100%" height={220}>
                <AreaChart data={timeSeries}>
                  <defs>
                    <linearGradient id="costGrad" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#10b981" stopOpacity={0.3} />
                      <stop offset="95%" stopColor="#10b981" stopOpacity={0} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
                  <XAxis dataKey="date" tick={{ fontSize: 11 }} tickFormatter={formatTimeLabel} />
                  <YAxis tick={{ fontSize: 11 }} tickFormatter={(v) => `$${v.toFixed(2)}`} />
                  <Tooltip
                    formatter={(v: number) => [formatCost(v), "Cost"]}
                    labelFormatter={formatTimeTooltip}
                  />
                  <Area
                    type="monotone"
                    dataKey="cost_usd"
                    stroke="#10b981"
                    fill="url(#costGrad)"
                    name="Cost (USD)"
                  />
                </AreaChart>
              </ResponsiveContainer>
            </div>

            {/* Cost by instance */}
            <div className="bg-white border border-gray-200 rounded-lg p-4 shadow-sm">
              <h2 className="text-sm font-semibold text-gray-700 mb-3">Cost by instance (USD)</h2>
              {byInstance.length === 0 ? (
                <div className="text-sm text-gray-400 py-8 text-center">No data</div>
              ) : (
                <ResponsiveContainer width="100%" height={220}>
                  <BarChart data={byInstance} layout="vertical">
                    <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
                    <XAxis type="number" tick={{ fontSize: 11 }} tickFormatter={(v) => `$${v.toFixed(2)}`} />
                    <YAxis
                      type="category"
                      dataKey="_label"
                      tick={{ fontSize: 11 }}
                      width={90}
                    />
                    <Tooltip formatter={(v: number) => [formatCost(v), "Cost"]} />
                    <Bar dataKey="cost_usd" fill="#6366f1" name="Cost (USD)" radius={[0, 4, 4, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              )}
            </div>

            {/* Cost by provider */}
            <div className="bg-white border border-gray-200 rounded-lg p-4 shadow-sm">
              <h2 className="text-sm font-semibold text-gray-700 mb-3">Cost by provider (USD)</h2>
              {byProvider.length === 0 ? (
                <div className="text-sm text-gray-400 py-8 text-center">No data</div>
              ) : (
                <ResponsiveContainer width="100%" height={220}>
                  <BarChart data={byProvider} layout="vertical">
                    <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
                    <XAxis type="number" tick={{ fontSize: 11 }} tickFormatter={(v) => `$${v.toFixed(2)}`} />
                    <YAxis
                      type="category"
                      dataKey="provider_name"
                      tick={{ fontSize: 11 }}
                      width={90}
                    />
                    <Tooltip formatter={(v: number) => [formatCost(v), "Cost"]} />
                    <Bar dataKey="cost_usd" fill="#f59e0b" name="Cost (USD)" radius={[0, 4, 4, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              )}
            </div>

            {/* Tokens by model (top 10) */}
            <div className="bg-white border border-gray-200 rounded-lg p-4 shadow-sm xl:col-span-2">
              <h2 className="text-sm font-semibold text-gray-700 mb-3">
                Tokens by model — top 10
              </h2>
              {byModel.length === 0 ? (
                <div className="text-sm text-gray-400 py-8 text-center">No data</div>
              ) : (
                <ResponsiveContainer width="100%" height={260}>
                  <BarChart data={byModel} layout="vertical">
                    <CartesianGrid strokeDasharray="3 3" stroke="#f0f0f0" />
                    <XAxis type="number" tick={{ fontSize: 11 }} tickFormatter={formatTokens} />
                    <YAxis
                      type="category"
                      dataKey="model_id"
                      tick={{ fontSize: 10 }}
                      width={160}
                    />
                    <Tooltip formatter={(v: number) => [formatTokens(v), ""]} />
                    <Legend />
                    <Bar dataKey="input_tokens" stackId="a" fill="#3b82f6" name="Input tokens" />
                    <Bar dataKey="output_tokens" stackId="a" fill="#818cf8" name="Output tokens" radius={[0, 4, 4, 0]} />
                  </BarChart>
                </ResponsiveContainer>
              )}
            </div>
          </div>
        </>
      )}
      </div>
    </div>
  );
}
