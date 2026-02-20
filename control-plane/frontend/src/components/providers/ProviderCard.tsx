import { ExternalLink, Trash2, Check } from "lucide-react";
import type { Provider } from "./providerData";
import { PROVIDER_ICONS } from "./providerIcons";

export type CardAnimationState = "idle" | "added" | "deleted";

interface ProviderCardProps {
  provider: Provider;
  isConfigured: boolean;
  maskedKey: string | null;
  onConfigure: () => void;
  onDelete: () => void;
  animationState?: CardAnimationState;
  /** Whether selection mode is active (shows checkboxes) */
  selectionMode?: boolean;
  /** Whether this card is currently selected */
  isSelected?: boolean;
  /** Called when the selection checkbox is toggled */
  onSelect?: (selected: boolean) => void;
}

export default function ProviderCard({
  provider,
  isConfigured,
  maskedKey,
  onConfigure,
  onDelete,
  animationState = "idle",
  selectionMode = false,
  isSelected = false,
  onSelect,
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

  const IconComponent = PROVIDER_ICONS[provider.id];

  return (
    <div
      tabIndex={0}
      aria-label={`${provider.name} provider${isConfigured ? " (configured)" : ""}`}
      onKeyDown={handleCardKeyDown}
      className={`relative bg-white rounded-lg border p-4 flex flex-col gap-3 transition-all duration-200 ease-in-out hover:shadow-md hover:scale-105 focus:outline-none focus:ring-2 focus:ring-blue-500 ${
        isConfigured
          ? ""
          : "border-gray-200"
      } ${
        animationState === "added"
          ? "animate-provider-fade-in"
          : animationState === "deleted"
            ? "animate-provider-fade-out"
            : ""
      }`}
      style={isConfigured ? { borderColor: provider.brandColor, backgroundColor: `${provider.brandColor}08` } : undefined}
    >
      {/* Configured badge – top-right corner */}
      {isConfigured && (
        <span
          className="absolute -top-2 -right-2 inline-flex items-center justify-center w-6 h-6 rounded-full shadow-sm"
          aria-hidden="true"
          data-testid="configured-badge"
          style={{ backgroundColor: provider.brandColor }}
        >
          <Check size={14} className="text-white" />
        </span>
      )}

      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          {selectionMode && (
            <input
              type="checkbox"
              checked={isSelected}
              onChange={(e) => {
                e.stopPropagation();
                onSelect?.(e.target.checked);
              }}
              onClick={(e) => e.stopPropagation()}
              aria-label={`Select ${provider.name}`}
              data-testid={`select-${provider.id}`}
              className="w-4 h-4 rounded border-gray-300 text-blue-600 focus:ring-2 focus:ring-blue-500 cursor-pointer"
            />
          )}
          {IconComponent && (
            <span
              className="inline-flex items-center justify-center w-6 h-6 rounded-md"
              data-testid="provider-icon"
              style={{ backgroundColor: `${provider.brandColor}15`, color: provider.brandColor }}
              aria-hidden="true"
            >
              <IconComponent size={14} />
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
