import { CheckCircle2, XCircle, Info, Loader2, X } from "lucide-react";
import toast from "react-hot-toast";

interface AppToastProps {
  title: string;
  description?: string;
  status: "success" | "error" | "info" | "loading";
  toastId: string;
  /**
   * If provided, renders a "Cancel" link next to the dismiss button. Used by
   * cancellable TaskManager tasks (e.g. instance clone) so the user can abort
   * a long-running action straight from the toast. The handler is responsible
   * for actually issuing the cancel request.
   */
  onCancel?: () => void;
}

const iconMap = {
  success: <CheckCircle2 size={18} className="text-green-500 shrink-0" />,
  error: <XCircle size={18} className="text-red-500 shrink-0" />,
  info: <Info size={18} className="text-blue-500 shrink-0" />,
  loading: <Loader2 size={18} className="text-blue-500 animate-spin shrink-0" />,
};

export default function AppToast({ title, description, status, toastId, onCancel }: AppToastProps) {
  return (
    <div className="flex items-start gap-3 min-w-[250px] max-w-[360px] bg-white rounded-lg shadow-lg px-4 py-3">
      <div className="mt-0.5">{iconMap[status]}</div>
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-gray-900 break-words whitespace-pre-wrap">{title}</p>
        {description && (
          <p className="text-xs text-gray-500 break-words whitespace-pre-wrap">{description}</p>
        )}
        {onCancel && (
          <button
            type="button"
            onClick={onCancel}
            className="mt-1 text-xs text-blue-600 hover:text-blue-800 font-medium"
          >
            Cancel
          </button>
        )}
      </div>
      <button
        type="button"
        onClick={() => toast.dismiss(toastId)}
        className="text-gray-400 hover:text-gray-600 shrink-0 mt-0.5"
        aria-label="Dismiss"
      >
        <X size={14} />
      </button>
    </div>
  );
}
