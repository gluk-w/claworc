import { useCallback } from "react";
import { Link } from "react-router-dom";
import { Plus } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import InstanceTable from "@/components/InstanceTable";
import {
  useInstances,
  useStartInstance,
  useStopInstance,
  useRestartInstance,
  useCloneInstance,
  useDeleteInstance,
  useRestartedToast,
  useReorderInstances,
} from "@/hooks/useInstances";
import type { Instance } from "@/types/instance";

export default function DashboardPage() {
  const { data: instances, isLoading } = useInstances();
  useRestartedToast(instances);
  const startMutation = useStartInstance();
  const stopMutation = useStopInstance();
  const restartMutation = useRestartInstance();
  const cloneMutation = useCloneInstance();
  const deleteMutation = useDeleteInstance();
  const reorderMutation = useReorderInstances();
  const queryClient = useQueryClient();

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

  return (
    <div>
      {isLoading ? (
        <div className="text-center py-12 text-gray-500">Loading...</div>
      ) : !instances || instances.length === 0 ? (
        <div className="text-center py-12">
          <p data-testid="empty-state-message" className="text-gray-500 mb-4">No instances yet.</p>
          <Link
            to="/instances/new"
            className="inline-flex items-center gap-1.5 px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
          >
            <Plus size={16} />
            Create your first instance
          </Link>
        </div>
      ) : (
        <InstanceTable
          instances={instances}
          onStart={(id) => startMutation.mutate(id)}
          onStop={(id) => {
            const inst = instances?.find((i) => i.id === id);
            if (inst) stopMutation.mutate({ id, displayName: inst.display_name });
          }}
          onRestart={(id) => {
            const inst = instances?.find((i) => i.id === id);
            if (inst) restartMutation.mutate({ id, displayName: inst.display_name });
          }}
          onClone={(id) => {
            const inst = instances?.find((i) => i.id === id);
            if (inst) cloneMutation.mutate({ id, displayName: inst.display_name });
          }}
          onDelete={(id) => deleteMutation.mutate(id)}
          onReorder={handleReorder}
          loadingInstanceId={loadingInstanceId}
        />
      )}
    </div>
  );
}
