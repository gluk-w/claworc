import { useState, useEffect } from "react";
import { Loader2, AlertTriangle, Activity, Clock, XCircle, BarChart3 } from "lucide-react";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  Cell,
} from "recharts";
import { fetchProviderAnalytics } from "@/api/settings";
import type { ProviderStats } from "@/types/settings";

interface ProviderStatsTabProps {
  providerId: string;
  providerName: string;
  brandColor: string;
}

export default function ProviderStatsTab({
  providerId,
  providerName,
  brandColor,
}: ProviderStatsTabProps) {
  const [stats, setStats] = useState<ProviderStats | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      setIsLoading(true);
      setError(null);
      try {
        const data = await fetchProviderAnalytics();
        if (!cancelled) {
          setStats(data.providers[providerId] ?? null);
        }
      } catch {
        if (!cancelled) {
          setError("Failed to load analytics data");
        }
      } finally {
        if (!cancelled) {
          setIsLoading(false);
        }
      }
    }

    load();
    return () => {
      cancelled = true;
    };
  }, [providerId]);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-8" data-testid="stats-loading">
        <Loader2 size={20} className="animate-spin text-gray-400" />
        <span className="ml-2 text-sm text-gray-500">Loading stats...</span>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex items-center justify-center py-8 text-sm text-red-500" data-testid="stats-error">
        <AlertTriangle size={16} className="mr-1.5" />
        {error}
      </div>
    );
  }

  if (!stats || stats.total_requests === 0) {
    return (
      <div className="flex flex-col items-center justify-center py-8 text-center" data-testid="stats-empty">
        <BarChart3 size={24} className="text-gray-300 mb-2" />
        <p className="text-sm text-gray-500">No usage data for {providerName}</p>
        <p className="text-xs text-gray-400 mt-1">
          Stats will appear after testing API connections
        </p>
      </div>
    );
  }

  const errorRate = (stats.error_rate * 100).toFixed(1);
  const avgLatency = Math.round(stats.avg_latency);

  // Chart data for the simple bar chart
  const chartData = [
    { name: "Successful", value: stats.total_requests - stats.error_count, fill: brandColor },
    { name: "Errors", value: stats.error_count, fill: "#EF4444" },
  ];

  return (
    <div className="space-y-4" data-testid="stats-content">
      {/* Summary metrics */}
      <div className="grid grid-cols-2 gap-3">
        <div className="bg-gray-50 rounded-lg p-3">
          <div className="flex items-center gap-1.5 text-xs text-gray-500 mb-1">
            <Activity size={12} />
            Total Requests (7d)
          </div>
          <p className="text-lg font-semibold text-gray-900" data-testid="stats-total-requests">
            {stats.total_requests}
          </p>
        </div>

        <div className="bg-gray-50 rounded-lg p-3">
          <div className="flex items-center gap-1.5 text-xs text-gray-500 mb-1">
            <XCircle size={12} />
            Error Rate
          </div>
          <p
            className={`text-lg font-semibold ${
              stats.error_rate > 0.5 ? "text-red-600" : stats.error_rate > 0.1 ? "text-yellow-600" : "text-gray-900"
            }`}
            data-testid="stats-error-rate"
          >
            {errorRate}%
          </p>
        </div>

        <div className="bg-gray-50 rounded-lg p-3">
          <div className="flex items-center gap-1.5 text-xs text-gray-500 mb-1">
            <Clock size={12} />
            Avg Latency
          </div>
          <p className="text-lg font-semibold text-gray-900" data-testid="stats-avg-latency">
            {avgLatency}ms
          </p>
        </div>

        <div className="bg-gray-50 rounded-lg p-3">
          <div className="flex items-center gap-1.5 text-xs text-gray-500 mb-1">
            <AlertTriangle size={12} />
            Errors (7d)
          </div>
          <p
            className={`text-lg font-semibold ${stats.error_count > 0 ? "text-red-600" : "text-gray-900"}`}
            data-testid="stats-error-count"
          >
            {stats.error_count}
          </p>
        </div>
      </div>

      {/* Bar chart */}
      <div className="bg-gray-50 rounded-lg p-3">
        <p className="text-xs text-gray-500 mb-2">Request Distribution (7d)</p>
        <div style={{ width: "100%", height: 120 }} data-testid="stats-chart">
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={chartData} layout="vertical" margin={{ left: 0, right: 8 }}>
              <XAxis type="number" hide />
              <YAxis
                type="category"
                dataKey="name"
                width={80}
                tick={{ fontSize: 11, fill: "#6B7280" }}
                axisLine={false}
                tickLine={false}
              />
              <Tooltip
                formatter={(value) => [String(value), "Requests"]}
                contentStyle={{ fontSize: 12 }}
              />
              <Bar dataKey="value" radius={[0, 4, 4, 0]} barSize={24}>
                {chartData.map((entry, index) => (
                  <Cell key={index} fill={entry.fill} />
                ))}
              </Bar>
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* Last error */}
      {stats.last_error && (
        <div className="bg-red-50 border border-red-200 rounded-lg p-3" data-testid="stats-last-error">
          <p className="text-xs font-medium text-red-700 mb-1">Most Recent Error</p>
          <p className="text-xs text-red-600 break-words">{stats.last_error}</p>
          {stats.last_error_at && (
            <p className="text-xs text-red-400 mt-1">
              {new Date(stats.last_error_at).toLocaleString()}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
