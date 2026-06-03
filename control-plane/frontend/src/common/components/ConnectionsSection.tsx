import { useState } from "react";
import { Plus, Trash2, Link2 } from "lucide-react";
import {
  useConnections,
  useDeleteConnection,
  useComposioToolkits,
} from "@common/hooks/useConnections";
import ConnectionModal from "@common/components/ConnectionModal";
import type { Connection } from "@common/types/connection";

const STATUS_STYLES: Record<Connection["status"], string> = {
  ACTIVE: "bg-green-50 text-green-700",
  INITIATED: "bg-amber-50 text-amber-700",
  FAILED: "bg-red-50 text-red-700",
  EXPIRED: "bg-gray-100 text-gray-600",
};

export default function ConnectionsSection({
  instanceId,
}: {
  instanceId: number;
}) {
  const { data: connections = [], isLoading } = useConnections(instanceId);
  const deleteConnection = useDeleteConnection(instanceId);
  const [modalOpen, setModalOpen] = useState(false);

  // Service icons come from the Composio toolkit catalog, keyed by slug. Load it
  // whenever there are connections to render (shared cache with the wizard).
  const { data: toolkits = [] } = useComposioToolkits(connections.length > 0);
  const logoBySlug = new Map(toolkits.map((t) => [t.slug, t.logo]));

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <div className="flex items-center justify-between mb-3">
        <h3 className="text-sm font-medium text-gray-900">
          External Connections
        </h3>
        <button
          type="button"
          onClick={() => setModalOpen(true)}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
        >
          <Plus size={12} />
          Add connection
        </button>
      </div>

      {isLoading ? (
        <p className="text-sm text-gray-400 italic">Loading…</p>
      ) : connections.length === 0 ? (
        <p className="text-xs text-gray-500">
          <a
            href="https://claworc.com/docs/connections"
            target="_blank"
            rel="noreferrer"
            className="text-blue-600 hover:underline"
          >
            Connect external services
          </a>{" "}
          (Gmail, GitHub, Google Analytics, …) for this agent securely.
        </p>
      ) : (
        <div className="divide-y divide-gray-100">
          {connections.map((c) => (
            <div key={c.id} className="flex items-center py-3">
              <div className="min-w-0 flex-1 flex items-center gap-3">
                {logoBySlug.get(c.toolkit_slug) ? (
                  <img
                    src={logoBySlug.get(c.toolkit_slug)}
                    alt=""
                    className="w-5 h-5 rounded object-contain shrink-0"
                  />
                ) : (
                  <Link2 size={16} className="text-gray-400 shrink-0" />
                )}
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-gray-900">
                      {c.name}
                    </span>
                    <span
                      className={`text-[10px] font-medium px-1.5 py-0.5 rounded ${STATUS_STYLES[c.status]}`}
                    >
                      {c.status}
                    </span>
                  </div>
                  {c.account_label && (
                    <span className="text-xs text-gray-500 truncate block">
                      {c.account_label}
                    </span>
                  )}
                </div>
              </div>
              <button
                type="button"
                onClick={() => {
                  if (
                    window.confirm(`Remove the ${c.name} connection?`)
                  ) {
                    deleteConnection.mutate(c.id);
                  }
                }}
                disabled={deleteConnection.isPending}
                className="p-1 text-gray-400 hover:text-red-600 disabled:opacity-50"
                title="Remove connection"
              >
                <Trash2 size={14} />
              </button>
            </div>
          ))}
        </div>
      )}

      <ConnectionModal
        open={modalOpen}
        instanceId={instanceId}
        onClose={() => setModalOpen(false)}
      />
    </div>
  );
}
