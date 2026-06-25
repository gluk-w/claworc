import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Plus, List, LayoutGrid } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import AgentTable from "@common/components/AgentTable";
import AgentGrid from "@common/components/AgentGrid";
import {
  useInstances,
  useStartInstance,
  useStopInstance,
  useRestartInstance,
  useCloneInstance,
  useDeleteInstance,
  useRestartedToast,
  useReorderInstances,
} from "@common/hooks/useInstances";
import { useAuth } from "@common/contexts/AuthContext";
import { useTeam } from "@common/contexts/TeamContext";
import TeamSelector from "@common/components/TeamSelector";
import CreateTeamDialog from "@common/components/CreateTeamDialog";
import type { Instance } from "@common/types/instance";

type ViewMode = "list" | "grid";
const VIEW_MODE_KEY = "claworc.instances.viewMode";

function readInitialViewMode(): ViewMode {
  try {
    const v = localStorage.getItem(VIEW_MODE_KEY);
    if (v === "grid" || v === "list") return v;
  } catch {
    // localStorage unavailable (e.g., private mode)
  }
  return "list";
}

export default function DashboardPage() {
  const { activeTeamId, isManager } = useTeam();
  const { data: instances, isLoading } = useInstances(activeTeamId);
  const [showCreateTeam, setShowCreateTeam] = useState(false);
  useRestartedToast(instances);
  const startMutation = useStartInstance();
  const stopMutation = useStopInstance();
  const restartMutation = useRestartInstance();
  const cloneMutation = useCloneInstance();
  const deleteMutation = useDeleteInstance();
  const reorderMutation = useReorderInstances();
  const queryClient = useQueryClient();
  const [viewMode, setViewMode] = useState<ViewMode>(readInitialViewMode);

  useEffect(() => {
    try {
      localStorage.setItem(VIEW_MODE_KEY, viewMode);
    } catch {
      // ignore
    }
  }, [viewMode]);

  const handleReorder = useCallback(
    (orderedIds: number[]) => {
      // Optimistic update
      queryClient.setQueryData<Instance[]>(["instances"], (old) => {
        if (!old) return old;
        const byId = new Map(old.map((i) => [i.id, i]));
        return orderedIds.map((id) => byId.get(id)).filter(Boolean) as Instance[];
      });
      reorderMutation.mutate(orderedIds);
    },
    [queryClient, reorderMutation],
  );

  // Track which instance is currently being operated on
  const getLoadingInstanceId = () => {
    if (startMutation.isPending) return startMutation.variables;
    if (stopMutation.isPending) return stopMutation.variables?.id;
    if (restartMutation.isPending) return restartMutation.variables?.id;
    if (cloneMutation.isPending) return cloneMutation.variables?.id;
    if (deleteMutation.isPending) return deleteMutation.variables;
    return null;
  };

  const loadingInstanceId = getLoadingInstanceId();
  const { canCreateInstances: canCreateInstancesGlobal } = useAuth();
  const canCreateInstances = canCreateInstancesGlobal || isManager();

  const sharedHandlers = {
    onStart: (id: number) => startMutation.mutate(id),
    onStop: (id: number) => {
      const inst = instances?.find((i) => i.id === id);
      if (inst) stopMutation.mutate({ id, displayName: inst.display_name });
    },
    onRestart: (id: number) => {
      const inst = instances?.find((i) => i.id === id);
      if (inst) restartMutation.mutate({ id, displayName: inst.display_name });
    },
    onClone: (id: number) => {
      const inst = instances?.find((i) => i.id === id);
      if (inst) cloneMutation.mutate({ id, displayName: inst.display_name });
    },
    onDelete: (id: number) => deleteMutation.mutate(id),
  };

  const hasInstances = !!instances && instances.length > 0;

  return (
    <div>
      <div className="flex items-center justify-between mb-4 gap-3">
        <TeamSelector onCreateTeam={() => setShowCreateTeam(true)} />
        {hasInstances && (
          <div className="flex items-center gap-3">
            <div className="inline-flex border border-gray-200 rounded-md overflow-hidden">
            <button
              type="button"
              onClick={() => setViewMode("list")}
              title="List view"
              className={`p-1.5 ${
                viewMode === "list"
                  ? "bg-gray-100 text-gray-900"
                  : "bg-white text-gray-400 hover:text-gray-600"
              }`}
            >
              <List size={16} />
            </button>
            <button
              type="button"
              onClick={() => setViewMode("grid")}
              title="Grid view"
              className={`p-1.5 border-l border-gray-200 ${
                viewMode === "grid"
                  ? "bg-gray-100 text-gray-900"
                  : "bg-white text-gray-400 hover:text-gray-600"
              }`}
            >
              <LayoutGrid size={16} />
            </button>
          </div>
            <Link
              to="/instances/new"
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 ${
                canCreateInstances ? "" : "invisible"
              }`}
            >
              <Plus size={14} />
              Create agent
            </Link>
          </div>
        )}
      </div>
      {showCreateTeam && (
        <CreateTeamDialog onClose={() => setShowCreateTeam(false)} />
      )}

      {isLoading ? (
        <div className="text-center py-12 text-gray-500">Loading...</div>
      ) : !hasInstances ? (
        <div className="text-center py-12">
          <p data-testid="empty-state-message" className="text-gray-500 mb-4">No agents yet.</p>
          {canCreateInstances ? (
            <Link
              to="/instances/new"
              className="inline-flex items-center gap-1.5 px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
            >
              <Plus size={16} />
              Create your first agent
            </Link>
          ) : (
            <p className="text-gray-400 text-sm">Ask an administrator to create an agent for you.</p>
          )}
        </div>
      ) : viewMode === "grid" ? (
        <AgentGrid
          instances={instances!}
          {...sharedHandlers}
          onReorder={handleReorder}
          loadingInstanceId={loadingInstanceId}
        />
      ) : (
        <AgentTable
          instances={instances!}
          {...sharedHandlers}
          onReorder={handleReorder}
          loadingInstanceId={loadingInstanceId}
        />
      )}
    </div>
  );
}
