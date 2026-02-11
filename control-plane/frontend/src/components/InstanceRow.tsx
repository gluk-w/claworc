import { Link } from "react-router-dom";
import { formatDistanceToNow } from "date-fns";
import { GripVertical } from "lucide-react";
import StatusBadge from "./StatusBadge";
import ActionButtons from "./ActionButtons";
import type { Instance } from "@/types/instance";
import type { SyntheticListenerMap } from "@dnd-kit/core/dist/hooks/utilities";

interface InstanceRowProps {
  instance: Instance;
  onStart: (id: number) => void;
  onStop: (id: number) => void;
  onRestart: (id: number) => void;
  onClone: (id: number) => void;
  onDelete: (id: number) => void;
  loading?: boolean;
  dragHandleListeners?: SyntheticListenerMap;
  dragHandleAttributes?: Record<string, any>;
}

export default function InstanceRow({
  instance,
  onStart,
  onStop,
  onRestart,
  onClone,
  onDelete,
  loading,
  dragHandleListeners,
  dragHandleAttributes,
}: InstanceRowProps) {
  const createdAt = instance.created_at
    ? formatDistanceToNow(new Date(instance.created_at), { addSuffix: true })
    : "";

  return (
    <>
      <td className="w-8 px-1 py-3">
        <button
          className="cursor-grab touch-none text-gray-400 hover:text-gray-600"
          {...dragHandleListeners}
          {...dragHandleAttributes}
        >
          <GripVertical size={16} />
        </button>
      </td>
      <td className="px-4 py-3">
        <Link
          data-testid={`instance-link-${instance.id}`}
          to={`/instances/${instance.id}`}
          className="text-sm font-medium text-blue-600 hover:text-blue-800"
        >
          {instance.display_name}
        </Link>
      </td>
      <td className="px-4 py-3">
        <StatusBadge status={instance.status} />
      </td>
      <td className="px-4 py-3 text-sm text-gray-500">{createdAt}</td>
      <td className="px-4 py-3">
        <ActionButtons
          instance={instance}
          onStart={onStart}
          onStop={onStop}
          onRestart={onRestart}
          onClone={onClone}
          onDelete={onDelete}
          loading={loading}
        />
      </td>
    </>
  );
}
