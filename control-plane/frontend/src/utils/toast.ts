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
