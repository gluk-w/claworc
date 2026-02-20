import { useState, useEffect, useRef, useCallback } from "react";
import { X, Eye, EyeOff, ExternalLink, Loader2 } from "lucide-react";
import toast from "react-hot-toast";
import type { Provider } from "./providerData";
import { validateApiKey } from "./validateApiKey";
import { testProviderKey } from "@/api/settings";

interface ProviderConfigModalProps {
  provider: Provider;
  isOpen: boolean;
  onClose: () => void;
  onSave: (apiKey: string, baseUrl?: string) => void;
  currentMaskedKey: string | null;
  isSaving?: boolean;
}

export default function ProviderConfigModal({
  provider,
  isOpen,
  onClose,
  onSave,
  currentMaskedKey,
  isSaving = false,
}: ProviderConfigModalProps) {
  const [apiKey, setApiKey] = useState("");
  const [showKey, setShowKey] = useState(false);
  const [baseUrl, setBaseUrl] = useState("");
  const [testResult, setTestResult] = useState<
    "idle" | "valid" | "invalid"
  >("idle");
  const [isTesting, setIsTesting] = useState(false);
  const [testErrorDetails, setTestErrorDetails] = useState<string | null>(null);

  const dialogRef = useRef<HTMLDivElement>(null);
  const apiKeyInputRef = useRef<HTMLInputElement>(null);

  const handleClose = useCallback(() => {
    setApiKey("");
    setBaseUrl("");
    setShowKey(false);
    setTestResult("idle");
    setIsTesting(false);
    setTestErrorDetails(null);
    onClose();
  }, [onClose]);

  const handleSave = useCallback(() => {
    const key = apiKey.trim();
    if (!key) return;
    onSave(key, provider.supportsBaseUrl && baseUrl.trim() ? baseUrl.trim() : undefined);
    setApiKey("");
    setBaseUrl("");
    setShowKey(false);
    setTestResult("idle");
  }, [apiKey, baseUrl, onSave, provider.supportsBaseUrl]);

  // Focus the API key input when the modal opens
  useEffect(() => {
    if (isOpen) {
      // Use a short timeout to ensure the DOM is ready
      const timer = setTimeout(() => {
        apiKeyInputRef.current?.focus();
      }, 0);
      return () => clearTimeout(timer);
    }
  }, [isOpen]);

  // Focus trap and keyboard handling
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        handleClose();
        return;
      }

      // Focus trap: Tab and Shift+Tab
      if (e.key === "Tab") {
        const dialog = dialogRef.current;
        if (!dialog) return;

        const focusable = dialog.querySelectorAll<HTMLElement>(
          'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
        );
        if (focusable.length === 0) return;

        const first = focusable[0]!;
        const last = focusable[focusable.length - 1]!;

        if (e.shiftKey) {
          if (document.activeElement === first) {
            e.preventDefault();
            last.focus();
          }
        } else {
          if (document.activeElement === last) {
            e.preventDefault();
            first.focus();
          }
        }
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isOpen, handleClose]);

  if (!isOpen) return null;

  const handleTestConnection = async () => {
    const key = apiKey.trim();
    if (!key) return;

    // First do a quick client-side format check
    const formatResult = validateApiKey(provider, key);
    if (!formatResult.valid) {
      setTestResult("invalid");
      setTestErrorDetails(null);
      toast.error(formatResult.message);
      return;
    }

    setIsTesting(true);
    setTestErrorDetails(null);

    try {
      const result = await testProviderKey({
        provider: provider.id,
        api_key: key,
        base_url: provider.supportsBaseUrl && baseUrl.trim() ? baseUrl.trim() : undefined,
      });

      setTestResult(result.success ? "valid" : "invalid");
      setTestErrorDetails(result.success ? null : result.details ?? null);

      if (result.success) {
        toast.success(result.message);
      } else {
        toast.error(result.message);
      }
    } catch {
      setTestResult("invalid");
      setTestErrorDetails("Could not reach the server. Check your connection.");
      toast.error("Connection test failed");
    } finally {
      setIsTesting(false);
    }
  };

  const saveDisabled = !apiKey.trim() || testResult === "invalid" || isSaving;

  const handleKeyDownOnInput = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !saveDisabled) {
      e.preventDefault();
      handleSave();
    }
  };

  const apiKeyDescribedBy = [
    currentMaskedKey ? "api-key-current" : null,
    testResult === "valid" ? "api-key-valid" : null,
    testResult === "invalid" ? "api-key-error" : null,
  ]
    .filter(Boolean)
    .join(" ") || undefined;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-black/40 backdrop-blur-sm"
        onClick={handleClose}
        aria-hidden="true"
      />

      {/* Dialog */}
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="provider-modal-title"
        className="relative bg-white rounded-lg shadow-xl w-full max-w-md mx-4"
      >
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
          <h2 id="provider-modal-title" className="text-base font-semibold text-gray-900">
            Configure {provider.name}
          </h2>
          <button
            type="button"
            onClick={handleClose}
            aria-label="Close dialog"
            className="text-gray-400 hover:text-gray-600 transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-blue-500 rounded-md"
          >
            <X size={18} />
          </button>
        </div>

        {/* Body */}
        <div className="px-6 py-4 space-y-4">
          {/* API Key input */}
          <div>
            <label htmlFor="api-key-input" className="block text-xs text-gray-500 mb-1">
              API Key
            </label>
            <div className="relative">
              <input
                ref={apiKeyInputRef}
                id="api-key-input"
                type={showKey ? "text" : "password"}
                value={apiKey}
                onChange={(e) => {
                  setApiKey(e.target.value);
                  setTestResult("idle");
                  setTestErrorDetails(null);
                }}
                onKeyDown={handleKeyDownOnInput}
                aria-describedby={apiKeyDescribedBy}
                className="w-full px-3 py-1.5 pr-10 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                placeholder={provider.apiKeyPlaceholder ?? "Enter API key"}
              />
              <button
                type="button"
                onClick={() => setShowKey(!showKey)}
                aria-label={showKey ? "Hide API key" : "Show API key"}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600 transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-blue-500 rounded-md"
              >
                {showKey ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
            </div>
            {currentMaskedKey && (
              <p id="api-key-current" className="mt-1 text-xs text-gray-400">
                Current key: <span className="font-mono">{currentMaskedKey}</span>
              </p>
            )}
          </div>

          {/* Base URL input (conditional) */}
          {provider.supportsBaseUrl && (
            <div>
              <label htmlFor="base-url-input" className="block text-xs text-gray-500 mb-1">
                Base URL <span className="text-gray-400">(optional)</span>
              </label>
              <input
                id="base-url-input"
                type="text"
                value={baseUrl}
                onChange={(e) => setBaseUrl(e.target.value)}
                onKeyDown={handleKeyDownOnInput}
                aria-describedby="base-url-note"
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                placeholder={provider.baseUrlPlaceholder ?? "https://your-proxy.example.com/v1"}
              />
              <p id="base-url-note" className="mt-1 text-xs text-amber-600">
                Note: Base URL configuration will be added in a future update.
                For now, only the API key will be saved.
              </p>
            </div>
          )}

          {/* Documentation link */}
          <a
            href={provider.docsUrl}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-xs text-blue-600 hover:text-blue-800 transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-blue-500 rounded-md"
          >
            <ExternalLink size={12} />
            Get an API key from {provider.name}
          </a>

          {/* Test result feedback */}
          {testResult === "valid" && (
            <p id="api-key-valid" role="status" className="text-xs text-green-600">
              API key verified successfully.
            </p>
          )}
          {testResult === "invalid" && (
            <div id="api-key-error" role="alert" className="space-y-0.5">
              <p className="text-xs text-red-600">
                Connection test failed.
              </p>
              {testErrorDetails && (
                <p className="text-xs text-red-500">
                  {testErrorDetails}
                </p>
              )}
            </div>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between px-6 py-4 border-t border-gray-200">
          <button
            type="button"
            onClick={handleTestConnection}
            disabled={!apiKey.trim() || isTesting}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50 transition-colors duration-200 disabled:opacity-50 disabled:cursor-not-allowed focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            {isTesting && <Loader2 size={12} className="animate-spin" />}
            {isTesting ? "Testing..." : "Test Connection"}
          </button>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={handleClose}
              className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50 transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleSave}
              disabled={saveDisabled}
              className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 transition-colors duration-200 disabled:opacity-50 disabled:cursor-not-allowed focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              {isSaving && <Loader2 size={12} className="animate-spin" />}
              {isSaving ? "Saving..." : "Save"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
