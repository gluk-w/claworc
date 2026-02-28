import { CheckCircle2, XCircle, Info, Loader2, X } from "lucide-react";
import toast from "react-hot-toast";

interface AppToastProps {
  title: string;
  description?: string;
  status: "success" | "error" | "info" | "loading";
  toastId: string;
}

const iconMap = {
  success: <CheckCircle2 size={18} className="text-green-500 shrink-0" />,
  error: <XCircle size={18} className="text-red-500 shrink-0" />,
  info: <Info size={18} className="text-blue-500 shrink-0" />,
  loading: <Loader2 size={18} className="text-blue-500 animate-spin shrink-0" />,
};

export default function AppToast({ title, description, status, toastId }: AppToastProps) {
  return (
    <div className="flex items-center gap-3 min-w-[250px] max-w-[300px] bg-white rounded-lg shadow-lg px-4 py-3">
      {iconMap[status]}
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-gray-900 truncate">{title}</p>
        {description && (
          <p className="text-xs text-gray-500 truncate">{description}</p>
        )}
      </div>
      <button
        onClick={() => toast.dismiss(toastId)}
        className="text-gray-400 hover:text-gray-600 shrink-0"
      >
        <X size={14} />
      </button>
    </div>
  );
}
