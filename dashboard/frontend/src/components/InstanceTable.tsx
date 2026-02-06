import InstanceRow from "./InstanceRow";
import type { Instance } from "@/types/instance";

interface InstanceTableProps {
  instances: Instance[];
  onStart: (id: number) => void;
  onStop: (id: number) => void;
  onRestart: (id: number) => void;
  onDelete: (id: number) => void;
  loading?: boolean;
}

export default function InstanceTable({
  instances,
  onStart,
  onStop,
  onRestart,
  onDelete,
  loading,
}: InstanceTableProps) {
  return (
    <div className="bg-white rounded-lg border border-gray-200 overflow-hidden">
      <table className="w-full">
        <thead>
          <tr className="bg-gray-50 border-b border-gray-200">
            <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Name
            </th>
            <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Status
            </th>
            <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              NodePorts
            </th>
            <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Created
            </th>
            <th className="px-4 py-3 text-left text-xs font-medium text-gray-500 uppercase tracking-wider">
              Actions
            </th>
          </tr>
        </thead>
        <tbody>
          {instances.map((inst) => (
            <InstanceRow
              key={inst.id}
              instance={inst}
              onStart={onStart}
              onStop={onStop}
              onRestart={onRestart}
              onDelete={onDelete}
              loading={loading}
            />
          ))}
        </tbody>
      </table>
    </div>
  );
}
