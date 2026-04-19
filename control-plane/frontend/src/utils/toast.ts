import { createElement } from "react";
import toast from "react-hot-toast";
import AppToast from "@/components/AppToast";

type ToastStatus = "success" | "error" | "info" | "loading";

function extractErrorDetail(error: unknown): string | undefined {
  if (!error) return undefined;
  if (typeof error === "string") return error;
  if (typeof error === "object") {
    // Axios error
    const axiosDetail = (error as any).response?.data?.detail ?? (error as any).response?.data?.error;
    if (typeof axiosDetail === "string") return axiosDetail;
    // Standard Error
    if (error instanceof Error && error.message) return error.message;
  }
  return undefined;
}

function showToast(
  title: string,
  description: string | undefined,
  status: ToastStatus,
  duration: number,
) {
  toast.custom(
    (t) =>
      createElement(AppToast, {
        title,
        description,
        status,
        toastId: t.id,
      }),
    { duration },
  );
}

export function successToast(title: string, description?: string) {
  showToast(title, description, "success", 3000);
}

export function errorToast(title: string, error?: unknown) {
  showToast(title, extractErrorDetail(error), "error", 5000);
}

export function infoToast(title: string, description?: string) {
  showToast(title, description, "info", 3000);
}

// envVarRestartToast shows a persistent loading toast while an instance is
// being recreated to apply env var changes. The toast id is stable per
// instance so repeated saves collapse into one, and useRestartedToast
// dismisses it when the instance transitions back to "running".
export function envVarRestartToast(instanceId: number, displayName: string) {
  const id = `env-restart-${instanceId}`;
  toast.custom(
    createElement(AppToast, {
      title: `Restarting ${displayName}`,
      description: "Setting environment variables",
      status: "loading",
      toastId: id,
    }),
    { id, duration: Infinity },
  );
}
