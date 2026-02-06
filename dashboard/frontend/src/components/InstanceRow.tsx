import { Link } from "react-router-dom";
import { formatDistanceToNow } from "date-fns";
import StatusBadge from "./StatusBadge";
import ActionButtons from "./ActionButtons";
import type { Instance } from "@/types/instance";

interface InstanceRowProps {
  instance: Instance;
  onStart: (id: number) => void;
  onStop: (id: number) => void;
  onRestart: (id: number) => void;
  onDelete: (id: number) => void;
  loading?: boolean;
}

export default function InstanceRow({
  instance,
  onStart,
  onStop,
  onRestart,
  onDelete,
  loading,
}: InstanceRowProps) {
  const createdAt = instance.created_at
    ? formatDistanceToNow(new Date(instance.created_at), { addSuffix: true })
    : "";

  return (
    <tr className="border-b border-gray-100 hover:bg-gray-50">
      <td className="px-4 py-3">
        <Link
          to={`/instances/${instance.id}`}
          className="text-sm font-medium text-blue-600 hover:text-blue-800"
        >
          {instance.display_name}
        </Link>
      </td>
      <td className="px-4 py-3">
        <StatusBadge status={instance.status} />
      </td>
      <td className="px-4 py-3 text-sm text-gray-600 font-mono">
        {instance.nodeport_chrome} / {instance.nodeport_terminal}
      </td>
      <td className="px-4 py-3 text-sm text-gray-500">{createdAt}</td>
      <td className="px-4 py-3">
        <ActionButtons
          instance={instance}
          onStart={onStart}
          onStop={onStop}
          onRestart={onRestart}
          onDelete={onDelete}
          loading={loading}
        />
      </td>
    </tr>
  );
}
