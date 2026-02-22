import type { SSHStatusTunnel } from "@/types/ssh";

function formatTime(ts: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  if (isNaN(d.getTime())) return "—";
  return d.toLocaleTimeString();
}

interface SSHTunnelListProps {
  tunnels: SSHStatusTunnel[];
  isLoading: boolean;
}

export default function SSHTunnelList({ tunnels, isLoading }: SSHTunnelListProps) {
  if (isLoading && tunnels.length === 0) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <div className="text-sm text-gray-500">Loading tunnel status...</div>
      </div>
    );
  }

  if (tunnels.length === 0) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6">
        <h3 className="text-sm font-medium text-gray-900 mb-2">SSH Tunnels</h3>
        <div className="text-sm text-gray-400">No active tunnels.</div>
      </div>
    );
  }

  return (
    <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
      <div className="px-6 py-4 border-b border-gray-200">
        <h3 className="text-sm font-medium text-gray-900">SSH Tunnels</h3>
      </div>
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-gray-200">
          <thead className="bg-gray-50">
            <tr>
              <th className="px-6 py-2 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Tunnel
              </th>
              <th className="px-6 py-2 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Local Port
              </th>
              <th className="px-6 py-2 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Remote Port
              </th>
              <th className="px-6 py-2 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Status
              </th>
              <th className="px-6 py-2 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Health
              </th>
              <th className="px-6 py-2 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
                Last Check
              </th>
            </tr>
          </thead>
          <tbody className="bg-white divide-y divide-gray-200">
            {tunnels.map((t) => {
              const healthy = t.status === "healthy";
              return (
                <tr key={t.label}>
                  <td className="px-6 py-3 text-sm text-gray-900 font-medium whitespace-nowrap">
                    {t.label}
                  </td>
                  <td className="px-6 py-3 text-sm text-gray-600 whitespace-nowrap">
                    {t.local_port}
                  </td>
                  <td className="px-6 py-3 text-sm text-gray-600 whitespace-nowrap">
                    {t.remote_port}
                  </td>
                  <td className="px-6 py-3 whitespace-nowrap">
                    <span
                      className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium ${
                        healthy
                          ? "bg-green-100 text-green-800"
                          : "bg-red-100 text-red-800"
                      }`}
                    >
                      <span
                        className={`w-1.5 h-1.5 rounded-full ${healthy ? "bg-green-500" : "bg-red-500"}`}
                      />
                      {t.status}
                    </span>
                  </td>
                  <td className="px-6 py-3 text-sm text-gray-600 whitespace-nowrap">
                    {t.successful_checks} ok / {t.failed_checks} failed
                  </td>
                  <td className="px-6 py-3 text-sm text-gray-600 whitespace-nowrap">
                    {formatTime(t.last_health_check)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}
