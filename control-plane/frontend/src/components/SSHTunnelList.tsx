import { formatDistanceToNow } from "date-fns";
import type { SSHTunnelStatus } from "@/types/ssh";

function formatTime(iso: string | undefined): string {
  if (!iso) return "—";
  try {
    return formatDistanceToNow(new Date(iso), { addSuffix: true });
  } catch {
    return "—";
  }
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / Math.pow(1024, i);
  return `${value.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

interface SSHTunnelListProps {
  tunnels: SSHTunnelStatus[];
}

export default function SSHTunnelList({ tunnels }: SSHTunnelListProps) {
  if (tunnels.length === 0) {
    return (
      <p className="text-sm text-gray-500 py-3">No active tunnels.</p>
    );
  }

  return (
    <div className="overflow-x-auto">
      <table className="min-w-full text-sm">
        <thead>
          <tr className="border-b border-gray-200 text-left text-xs text-gray-500">
            <th className="pb-2 pr-4 font-medium">Service</th>
            <th className="pb-2 pr-4 font-medium">Local Port</th>
            <th className="pb-2 pr-4 font-medium">Remote Port</th>
            <th className="pb-2 pr-4 font-medium">Status</th>
            <th className="pb-2 pr-4 font-medium">Last Check</th>
            <th className="pb-2 pr-4 font-medium">Transferred</th>
            <th className="pb-2 font-medium">Error</th>
          </tr>
        </thead>
        <tbody>
          {tunnels.map((tunnel) => (
            <tr
              key={`${tunnel.service}-${tunnel.local_port}`}
              className="border-b border-gray-100"
            >
              <td className="py-2 pr-4 font-medium text-gray-900">
                {tunnel.service}
              </td>
              <td className="py-2 pr-4 text-gray-700 tabular-nums">
                {tunnel.local_port}
              </td>
              <td className="py-2 pr-4 text-gray-700 tabular-nums">
                {tunnel.remote_port}
              </td>
              <td className="py-2 pr-4">
                <span
                  className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium ${
                    tunnel.healthy
                      ? "bg-green-100 text-green-800"
                      : "bg-red-100 text-red-800"
                  }`}
                >
                  <span
                    className={`h-1.5 w-1.5 rounded-full ${
                      tunnel.healthy ? "bg-green-500" : "bg-red-500"
                    }`}
                  />
                  {tunnel.healthy ? "healthy" : "unhealthy"}
                </span>
              </td>
              <td className="py-2 pr-4 text-gray-500">
                {formatTime(tunnel.last_check)}
              </td>
              <td className="py-2 pr-4 text-gray-700 tabular-nums">
                {formatBytes(tunnel.bytes_transferred)}
              </td>
              <td className="py-2 text-gray-500 max-w-[200px] truncate">
                {tunnel.last_error || "—"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
