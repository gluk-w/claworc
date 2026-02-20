import { ExternalLink, Trash2, Check } from "lucide-react";
import type { Provider } from "./providerData";

export type CardAnimationState = "idle" | "added" | "deleted";

interface ProviderCardProps {
  provider: Provider;
  isConfigured: boolean;
  maskedKey: string | null;
  onConfigure: () => void;
  onDelete: () => void;
  animationState?: CardAnimationState;
}

export default function ProviderCard({
  provider,
  isConfigured,
  maskedKey,
  onConfigure,
  onDelete,
  animationState = "idle",
}: ProviderCardProps) {
  const handleCardKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") {
      // Only trigger if the card itself is focused (not a child button/link)
      if (e.target === e.currentTarget) {
        e.preventDefault();
        onConfigure();
      }
    }
  };

  return (
    <div
      tabIndex={0}
      aria-label={`${provider.name} provider${isConfigured ? " (configured)" : ""}`}
      onKeyDown={handleCardKeyDown}
      className={`relative bg-white rounded-lg border p-4 flex flex-col gap-3 transition-all duration-200 ease-in-out hover:shadow-md hover:scale-105 focus:outline-none focus:ring-2 focus:ring-blue-500 ${
        isConfigured
          ? "border-green-500 bg-green-50/30"
          : "border-gray-200"
      } ${
        animationState === "added"
          ? "animate-provider-fade-in"
          : animationState === "deleted"
            ? "animate-provider-fade-out"
            : ""
      }`}
    >
      {/* Configured badge – top-right corner */}
      {isConfigured && (
        <span
          className="absolute -top-2 -right-2 inline-flex items-center justify-center w-6 h-6 rounded-full bg-green-500 shadow-sm"
          aria-hidden="true"
          data-testid="configured-badge"
        >
          <Check size={14} className="text-white" />
        </span>
      )}

      {/* Header */}
      <div className="flex items-center justify-between">
        <span className="text-sm font-medium text-gray-900">
          {provider.name}
        </span>
        <a
          href={provider.docsUrl}
          target="_blank"
          rel="noopener noreferrer"
          className="text-gray-400 hover:text-gray-600 focus:outline-none focus:ring-2 focus:ring-blue-500 rounded-md"
          title="API key documentation"
          aria-label={`${provider.name} API key documentation`}
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
            <span className="inline-flex items-center px-2 py-0.5 text-xs font-mono text-gray-600 bg-gray-100 rounded-md">
              {maskedKey}
            </span>
          )}
        </div>
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={onConfigure}
            className="px-2.5 py-1 text-xs font-medium text-blue-600 border border-blue-300 rounded-md hover:bg-blue-50 transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            {isConfigured ? "Update" : "Configure"}
          </button>
          {isConfigured && (
            <button
              type="button"
              onClick={onDelete}
              className="p-1 text-gray-400 hover:text-red-500 rounded-md transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-blue-500"
              title="Remove API key"
              aria-label={`Remove ${provider.name} API key`}
            >
              <Trash2 size={14} />
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
