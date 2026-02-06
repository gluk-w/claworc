import { useState } from "react";
import {
  Monitor,
  Terminal,
  Play,
  Square,
  RefreshCw,
  Trash2,
} from "lucide-react";
import ConfirmDialog from "./ConfirmDialog";
import type { Instance } from "@/types/instance";

const K8S_NODE_IP = "192.168.1.104";

interface ActionButtonsProps {
  instance: Instance;
  onStart: (id: number) => void;
  onStop: (id: number) => void;
  onRestart: (id: number) => void;
  onDelete: (id: number) => void;
  loading?: boolean;
}

export default function ActionButtons({
  instance,
  onStart,
  onStop,
  onRestart,
  onDelete,
  loading,
}: ActionButtonsProps) {
  const [showConfirm, setShowConfirm] = useState(false);
  const isStopped = instance.status === "stopped";
  const isRunning = instance.status === "running";

  const openVnc = (port: number, type: string) => {
    window.open(
      `http://${K8S_NODE_IP}:${port}/vnc.html?autoconnect=true`,
      `vnc-${type}-${instance.name}`,
    );
  };

  return (
    <>
      <div className="flex items-center gap-1">
        <button
          onClick={() => openVnc(instance.nodeport_chrome, "chrome")}
          disabled={isStopped}
          title="Chrome VNC"
          className="p-1.5 text-gray-500 hover:text-blue-600 hover:bg-blue-50 rounded disabled:opacity-30 disabled:cursor-not-allowed"
        >
          <Monitor size={16} />
        </button>
        <button
          onClick={() => openVnc(instance.nodeport_terminal, "term")}
          disabled={isStopped}
          title="Terminal VNC"
          className="p-1.5 text-gray-500 hover:text-blue-600 hover:bg-blue-50 rounded disabled:opacity-30 disabled:cursor-not-allowed"
        >
          <Terminal size={16} />
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
            disabled={loading || !isRunning}
            title="Stop"
            className="p-1.5 text-gray-500 hover:text-yellow-600 hover:bg-yellow-50 rounded disabled:opacity-30 disabled:cursor-not-allowed"
          >
            <Square size={16} />
          </button>
        )}
        <button
          onClick={() => onRestart(instance.id)}
          disabled={loading || !isRunning}
          title="Restart"
          className="p-1.5 text-gray-500 hover:text-orange-600 hover:bg-orange-50 rounded disabled:opacity-30 disabled:cursor-not-allowed"
        >
          <RefreshCw size={16} />
        </button>
        <button
          onClick={() => setShowConfirm(true)}
          disabled={loading}
          title="Delete"
          className="p-1.5 text-gray-500 hover:text-red-600 hover:bg-red-50 rounded disabled:opacity-50"
        >
          <Trash2 size={16} />
        </button>
      </div>
      {showConfirm && (
        <ConfirmDialog
          title="Delete Instance"
          message={`Are you sure you want to delete "${instance.display_name}"? This will remove all Kubernetes resources and data.`}
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
