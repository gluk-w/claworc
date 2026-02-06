import { Link } from "react-router-dom";
import { Plus } from "lucide-react";
import InstanceTable from "@/components/InstanceTable";
import {
  useInstances,
  useStartInstance,
  useStopInstance,
  useRestartInstance,
  useDeleteInstance,
} from "@/hooks/useInstances";

export default function DashboardPage() {
  const { data: instances, isLoading } = useInstances();
  const startMutation = useStartInstance();
  const stopMutation = useStopInstance();
  const restartMutation = useRestartInstance();
  const deleteMutation = useDeleteInstance();

  const anyLoading =
    startMutation.isPending ||
    stopMutation.isPending ||
    restartMutation.isPending ||
    deleteMutation.isPending;

  return (
    <div>
      {isLoading ? (
        <div className="text-center py-12 text-gray-500">Loading...</div>
      ) : !instances || instances.length === 0 ? (
        <div className="text-center py-12">
          <p className="text-gray-500 mb-4">No instances yet.</p>
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
          onStop={(id) => stopMutation.mutate(id)}
          onRestart={(id) => restartMutation.mutate(id)}
          onDelete={(id) => deleteMutation.mutate(id)}
          loading={anyLoading}
        />
      )}
    </div>
  );
}
