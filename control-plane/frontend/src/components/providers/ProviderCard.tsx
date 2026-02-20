import { ExternalLink, Trash2, Check } from "lucide-react";
import type { Provider } from "./providerData";

interface ProviderCardProps {
  provider: Provider;
  isConfigured: boolean;
  maskedKey: string | null;
  onConfigure: () => void;
  onDelete: () => void;
}

export default function ProviderCard({
  provider,
  isConfigured,
  maskedKey,
  onConfigure,
  onDelete,
}: ProviderCardProps) {
  return (
    <div
      className={`bg-white rounded-lg border p-4 flex flex-col gap-3 ${
        isConfigured
          ? "border-green-500 bg-green-50/30"
          : "border-gray-200"
      }`}
    >
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          {isConfigured && (
            <span className="inline-flex items-center justify-center w-4 h-4 rounded-full bg-green-500">
              <Check size={10} className="text-white" />
            </span>
          )}
          <span className="text-sm font-medium text-gray-900">
            {provider.name}
          </span>
        </div>
        <a
          href={provider.docsUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="text-gray-400 hover:text-gray-600"
          title="API key documentation"
        >
          <ExternalLink size={14} />
        </a>
      </div>

      {/* Body */}
      <p className="text-xs text-gray-500 leading-relaxed">
        {provider.description}
      </p>

      {/* Footer */}
      <div className="flex items-center justify-between mt-auto pt-2 border-t border-gray-100">
        <div className="flex items-center gap-2">
          {maskedKey && (
            <span className="inline-flex items-center px-2 py-0.5 text-xs font-mono text-gray-600 bg-gray-100 rounded">
              {maskedKey}
            </span>
          )}
        </div>
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={onConfigure}
            className="px-2.5 py-1 text-xs font-medium text-blue-600 border border-blue-300 rounded-md hover:bg-blue-50"
          >
            {isConfigured ? "Update" : "Configure"}
          </button>
          {isConfigured && (
            <button
              type="button"
              onClick={onDelete}
              className="p-1 text-gray-400 hover:text-red-500 rounded"
              title="Remove API key"
            >
              <Trash2 size={14} />
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
