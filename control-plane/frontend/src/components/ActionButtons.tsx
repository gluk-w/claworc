import { useState } from "react";
import { useNavigate } from "react-router-dom";
import {
  Monitor,
  Terminal,
  LayoutDashboard,
  Copy,
  Play,
  Square,
  RefreshCw,
  Trash2,
} from "lucide-react";
import ConfirmDialog from "./ConfirmDialog";
import type { Instance } from "@/types/instance";

interface ActionButtonsProps {
  instance: Instance;
  onStart: (id: number) => void;
  onStop: (id: number) => void;
  onRestart: (id: number) => void;
  onClone: (id: number) => void;
  onDelete: (id: number) => void;
  loading?: boolean;
}

export default function ActionButtons({
  instance,
  onStart,
  onStop,
  onRestart,
  onClone,
  onDelete,
  loading,
}: ActionButtonsProps) {
  const [showConfirm, setShowConfirm] = useState(false);
  const navigate = useNavigate();
  const isStopped = instance.status === "stopped";
  const isRunning = instance.status === "running";
  const isRestarting = instance.status === "restarting";
  const isStopping = instance.status === "stopping";
  const isUnavailable = !isRunning;

  const openVnc = (url: string, type: string) => {
    window.open(url, `vnc-${type}-${instance.name}`);
  };

  return (
    <>
      <div className="flex items-center gap-1">
        <button
          onClick={() => openVnc(instance.vnc_chrome_url, "chrome")}
          disabled={isUnavailable}
          title="Chrome Browser"
          className="p-1.5 text-gray-500 hover:text-blue-600 hover:bg-blue-50 rounded disabled:opacity-30 disabled:cursor-not-allowed"
        >
          <Monitor size={16} />
        </button>
        <button
          onClick={() => navigate(`/instances/${instance.id}#terminal`)}
          disabled={isUnavailable}
          title="Terminal"
          className="p-1.5 text-gray-500 hover:text-blue-600 hover:bg-blue-50 rounded disabled:opacity-30 disabled:cursor-not-allowed"
        >
          <Terminal size={16} />
        </button>
        <button
          onClick={() => {
            const gwUrl = `ws://${window.location.host}/api/v1/instances/${instance.id}/control/`;
            const params = new URLSearchParams({
              gatewayUrl: gwUrl,
              token: instance.gateway_token,
            });
            window.open(`/api/v1/instances/${instance.id}/control/?${params}`, `control-${instance.name}`);
          }}
          disabled={isUnavailable}
          title="Control UI"
          className="p-1.5 text-gray-500 hover:text-teal-600 hover:bg-teal-50 rounded disabled:opacity-30 disabled:cursor-not-allowed"
        >
          <LayoutDashboard size={16} />
        </button>
        <button
          onClick={() => onClone(instance.id)}
          disabled={loading || instance.status === "creating"}
          title="Clone"
          className="p-1.5 text-gray-500 hover:text-purple-600 hover:bg-purple-50 rounded disabled:opacity-30 disabled:cursor-not-allowed"
        >
          <Copy size={16} />
        </button>
        <button
          onClick={() => onRestart(instance.id)}
          disabled={loading || !isRunning || isRestarting}
          title="Restart"
          className="p-1.5 text-gray-500 hover:text-orange-600 hover:bg-orange-50 rounded disabled:opacity-30 disabled:cursor-not-allowed"
        >
          <RefreshCw size={16} />
        </button>
        {isStopped ? (
          <button
            onClick={() => onStart(instance.id)}
            disabled={loading}
            title="Start"
            className="p-1.5 text-gray-500 hover:text-green-600 hover:bg-green-50 rounded disabled:opacity-50"
          >
            <Play size={16} />
          </button>
        ) : (
          <button
            onClick={() => onStop(instance.id)}
            disabled={loading || !isRunning || isStopping}
            title="Stop"
            className="p-1.5 text-gray-500 hover:text-yellow-600 hover:bg-yellow-50 rounded disabled:opacity-30 disabled:cursor-not-allowed"
          >
            <Square size={16} />
          </button>
        )}
        <button
          onClick={() => setShowConfirm(true)}
          disabled={loading}
          title="Delete"
          className="p-1.5 text-gray-500 hover:text-red-600 hover:bg-red-50 rounded disabled:opacity-30 disabled:cursor-not-allowed"
        >
          <Trash2 size={16} />
        </button>
      </div>
      {showConfirm && (
        <ConfirmDialog
          title="Delete Instance"
          message={`Are you sure you want to delete "${instance.display_name}"? This will remove all container resources and data.`}
          onConfirm={() => {
            setShowConfirm(false);
            onDelete(instance.id);
          }}
          onCancel={() => setShowConfirm(false)}
        />
      )}
    </>
  );
}
