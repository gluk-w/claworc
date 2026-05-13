import { createElement, useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";
import toast from "react-hot-toast";
import { useHealth } from "@/hooks/useHealth";
import { reinitializeOrchestrator, type OrchestratorStatus } from "@/api/health";
import OrchestratorDownToast from "@/components/OrchestratorDownToast";
import { successToast } from "@/utils/toast";

const TOAST_ID = "orchestrator-unavailable";

function isAvailable(status: OrchestratorStatus | undefined, fallback: boolean): boolean {
  if (status) return status.available;
  return fallback;
}

function recoverySuccessTitle(status: OrchestratorStatus | undefined): string {
  if (status?.backend === "docker") return "Docker connected";
  if (status?.backend === "kubernetes") return "Kubernetes connected";
  return "Container backend connected";
}

/**
 * Watches the /health poll and surfaces a persistent red toast (with Retry)
 * whenever the container backend is unavailable. On recovery, dismisses the
 * persistent toast and shows a success toast that auto-closes.
 */
export function useOrchestratorWatcher() {
  const { data } = useHealth();
  const queryClient = useQueryClient();
  const wasAvailableRef = useRef<boolean | null>(null);

  useEffect(() => {
    if (!data) return;

    const status = data.orchestrator_status;
    const available = isAvailable(status, data.orchestrator === "connected");

    const handleRetry = async () => {
      try {
        await reinitializeOrchestrator();
      } finally {
        await queryClient.invalidateQueries({ queryKey: ["health"] });
      }
    };

    if (!available) {
      // Show or update the persistent red toast. No close button is rendered
      // by the component itself; duration: Infinity prevents auto-dismiss.
      toast.custom(
        () => createElement(OrchestratorDownToast, { status, onRetry: handleRetry }),
        { id: TOAST_ID, duration: Infinity },
      );
    } else {
      // If we previously showed the down toast, dismiss it and announce recovery.
      if (wasAvailableRef.current === false) {
        toast.dismiss(TOAST_ID);
        successToast(recoverySuccessTitle(status));
      }
    }

    wasAvailableRef.current = available;
  }, [data, queryClient]);
}
