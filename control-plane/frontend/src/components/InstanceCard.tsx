import { Link } from "react-router-dom";
import { formatDistanceToNow } from "date-fns";
import { GripVertical } from "lucide-react";
import StatusBadge from "./StatusBadge";
import ActionButtons from "./ActionButtons";
import { useSSHStatus } from "@/hooks/useSSHStatus";
import { buildSSHTooltip } from "@/utils/sshTooltip";
import type { Instance } from "@/types/instance";
import type { SyntheticListenerMap } from "@dnd-kit/core/dist/hooks/utilities";

interface InstanceCardProps {
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

export default function InstanceCard({
  instance,
  onStart,
  onStop,
  onRestart,
  onClone,
  onDelete,
  loading,
  dragHandleListeners,
  dragHandleAttributes,
}: InstanceCardProps) {
  const sshStatus = useSSHStatus(instance.id, instance.status === "running");

  const createdAt = instance.created_at
    ? formatDistanceToNow(new Date(instance.created_at), { addSuffix: true })
    : "";

  const tooltip =
    instance.status === "creating" && instance.status_message
      ? instance.status_message
      : buildSSHTooltip(sshStatus.data);

  return (
    <div className="bg-white border border-gray-200 rounded-xl p-4 flex flex-col gap-2 hover:shadow-sm hover:border-blue-300 transition-all">
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0 flex-1">
          <button
            className="cursor-grab touch-none text-gray-400 hover:text-gray-600 shrink-0"
            {...dragHandleListeners}
            {...dragHandleAttributes}
          >
            <GripVertical size={16} />
          </button>
          <Link
            data-testid={`instance-link-${instance.id}`}
            to={`/instances/${instance.id}`}
            className="text-sm font-medium text-blue-600 hover:text-blue-800 truncate"
          >
            {instance.display_name}
          </Link>
        </div>
        <StatusBadge status={instance.status} tooltip={tooltip} />
      </div>
      <div className="text-xs text-gray-500">{createdAt}</div>
      <div className="flex justify-end">
        <ActionButtons
          instance={instance}
          onStart={onStart}
          onStop={onStop}
          onRestart={onRestart}
          onClone={onClone}
          onDelete={onDelete}
          loading={loading}
        />
      </div>
    </div>
  );
}
