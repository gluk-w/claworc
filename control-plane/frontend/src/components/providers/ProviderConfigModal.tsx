import { useState } from "react";
import { X, Eye, EyeOff, ExternalLink } from "lucide-react";
import type { Provider } from "./providerData";

interface ProviderConfigModalProps {
  provider: Provider;
  isOpen: boolean;
  onClose: () => void;
  onSave: (apiKey: string, baseUrl?: string) => void;
  currentMaskedKey: string | null;
}

export default function ProviderConfigModal({
  provider,
  isOpen,
  onClose,
  onSave,
  currentMaskedKey,
}: ProviderConfigModalProps) {
  const [apiKey, setApiKey] = useState("");
  const [showKey, setShowKey] = useState(false);
  const [baseUrl, setBaseUrl] = useState("");
  const [testResult, setTestResult] = useState<
    "idle" | "valid" | "invalid"
  >("idle");

  if (!isOpen) return null;

  const handleSave = () => {
    const key = apiKey.trim();
    if (!key) return;
    onSave(key, provider.supportsBaseUrl && baseUrl.trim() ? baseUrl.trim() : undefined);
    setApiKey("");
    setBaseUrl("");
    setShowKey(false);
    setTestResult("idle");
  };

  const handleClose = () => {
    setApiKey("");
    setBaseUrl("");
    setShowKey(false);
    setTestResult("idle");
    onClose();
  };

  const handleTestConnection = () => {
    const key = apiKey.trim();
    if (!key || key.length < 8) {
      setTestResult("invalid");
    } else {
      setTestResult("valid");
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      {/* Backdrop */}
      <div
        className="absolute inset-0 bg-black/40 backdrop-blur-sm"
        onClick={handleClose}
      />

      {/* Dialog */}
      <div className="relative bg-white rounded-lg shadow-xl w-full max-w-md mx-4">
        {/* Header */}
        <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
          <h2 className="text-base font-semibold text-gray-900">
            Configure {provider.name}
          </h2>
          <button
            type="button"
            onClick={handleClose}
            className="text-gray-400 hover:text-gray-600"
          >
            <X size={18} />
          </button>
        </div>

        {/* Body */}
        <div className="px-6 py-4 space-y-4">
          {/* API Key input */}
          <div>
            <label className="block text-xs text-gray-500 mb-1">
              API Key
            </label>
            <div className="relative">
              <input
                type={showKey ? "text" : "password"}
                value={apiKey}
                onChange={(e) => {
                  setApiKey(e.target.value);
                  setTestResult("idle");
                }}
                className="w-full px-3 py-1.5 pr-10 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                placeholder="Enter API key"
              />
              <button
                type="button"
                onClick={() => setShowKey(!showKey)}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
              >
                {showKey ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
            </div>
            {currentMaskedKey && (
              <p className="mt-1 text-xs text-gray-400">
                Current key: <span className="font-mono">{currentMaskedKey}</span>
              </p>
            )}
          </div>

          {/* Base URL input (conditional) */}
          {provider.supportsBaseUrl && (
            <div>
              <label className="block text-xs text-gray-500 mb-1">
                Base URL <span className="text-gray-400">(optional)</span>
              </label>
              <input
                type="text"
                value={baseUrl}
                onChange={(e) => setBaseUrl(e.target.value)}
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                placeholder="https://your-proxy.example.com/v1"
              />
              <p className="mt-1 text-xs text-amber-600">
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
            className="inline-flex items-center gap-1 text-xs text-blue-600 hover:text-blue-800"
          >
            <ExternalLink size={12} />
            Get an API key from {provider.name}
          </a>

          {/* Test result feedback */}
          {testResult === "valid" && (
            <p className="text-xs text-green-600">Key format looks valid.</p>
          )}
          {testResult === "invalid" && (
            <p className="text-xs text-red-600">
              Key seems too short. Please check and try again.
            </p>
          )}
        </div>

        {/* Footer */}
        <div className="flex items-center justify-between px-6 py-4 border-t border-gray-200">
          <button
            type="button"
            onClick={handleTestConnection}
            disabled={!apiKey.trim()}
            className="px-3 py-1.5 text-xs font-medium text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            Test Connection
          </button>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={handleClose}
              className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleSave}
              disabled={!apiKey.trim()}
              className="px-3 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              Save
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
