import { useState } from "react";
import { XCircle, Loader2 } from "lucide-react";
import type { OrchestratorStatus, OrchestratorReason } from "@/api/health";

interface Props {
  status: OrchestratorStatus | undefined;
  onRetry: () => Promise<unknown>;
}

function backendLabel(backend: string | undefined): string {
  if (backend === "docker") return "Docker";
  if (backend === "kubernetes") return "Kubernetes";
  return "Container backend";
}

function title(status: OrchestratorStatus | undefined): string {
  const b = status?.backend;
  if (b === "docker") return "Docker is unavailable";
  if (b === "kubernetes") return "Kubernetes is unavailable";
  // Prefer the most recently attempted backend if we have attempts.
  const lastAttempt = status?.attempts?.[status.attempts.length - 1];
  if (lastAttempt?.backend === "docker") return "Docker is unavailable";
  if (lastAttempt?.backend === "kubernetes") return "Kubernetes is unavailable";
  return "No container backend available";
}

function describe(status: OrchestratorStatus | undefined): string {
  const last = status?.attempts?.[status.attempts.length - 1];
  const reason = last?.reason as OrchestratorReason | undefined;
  const backend = backendLabel(last?.backend);
  switch (reason) {
    case "daemon_unreachable":
      return "Docker daemon is not running. Start Docker Desktop, then click Retry.";
    case "client_error":
      return `Could not connect to ${backend}. Check that it is installed and you have permission to use it.`;
    case "config_missing":
      return "Kubernetes configuration not found. Set up a kubeconfig or run inside a cluster.";
    case "namespace_missing":
      return "The configured Kubernetes namespace was not found.";
    case "invalid_setting":
      return "Invalid backend setting. Choose Docker, Kubernetes, or Auto.";
    default:
      return `${backend} is unreachable. Check the service and click Retry.`;
  }
}

export default function OrchestratorDownToast({ status, onRetry }: Props) {
  const [retrying, setRetrying] = useState(false);

  const handleRetry = async () => {
    if (retrying) return;
    setRetrying(true);
    try {
      await onRetry();
    } finally {
      setRetrying(false);
    }
  };

  return (
    <div className="flex items-start gap-3 min-w-[280px] max-w-[400px] bg-red-50 border border-red-200 rounded-lg shadow-lg px-4 py-3">
      <div className="mt-0.5">
        <XCircle size={18} className="text-red-500 shrink-0" />
      </div>
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-red-900 break-words whitespace-pre-wrap">
          {title(status)}
        </p>
        <p className="text-xs text-red-700 break-words whitespace-pre-wrap mt-0.5">
          {describe(status)}
        </p>
        <button
          type="button"
          onClick={handleRetry}
          disabled={retrying}
          className="mt-2 inline-flex items-center gap-1 text-xs font-medium text-white bg-red-600 hover:bg-red-700 disabled:bg-red-400 disabled:cursor-not-allowed rounded px-2 py-1"
        >
          {retrying && <Loader2 size={12} className="animate-spin" />}
          {retrying ? "Retrying…" : "Retry"}
        </button>
      </div>
    </div>
  );
}
