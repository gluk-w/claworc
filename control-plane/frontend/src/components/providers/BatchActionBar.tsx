import { useState } from "react";
import { Trash2, Download, FlaskConical, Loader2, CheckCircle2, XCircle } from "lucide-react";
import type { Provider } from "./providerData";
import { testProviderKey } from "@/api/settings";
import type { TestProviderKeyResponse } from "@/api/settings";

export interface BatchTestResult {
  provider: Provider;
  result: TestProviderKeyResponse;
}

interface BatchActionBarProps {
  selectedProviders: Provider[];
  /** Map from envVarName to masked key string for configured providers */
  configuredKeys: Record<string, string>;
  onDeleteSelected: () => void;
  onClearSelection: () => void;
}

export default function BatchActionBar({
  selectedProviders,
  configuredKeys,
  onDeleteSelected,
  onClearSelection,
}: BatchActionBarProps) {
  const [isTesting, setIsTesting] = useState(false);
  const [testResults, setTestResults] = useState<BatchTestResult[] | null>(null);

  const configuredSelected = selectedProviders.filter(
    (p) => !!configuredKeys[p.envVarName],
  );

  const handleExportKeys = () => {
    const lines = ["# LLM Provider API Keys", `# Exported ${new Date().toISOString()}`, ""];
    for (const provider of selectedProviders) {
      const maskedKey = configuredKeys[provider.envVarName];
      if (maskedKey) {
        lines.push(`# ${provider.name}`);
        lines.push(`${provider.envVarName}=${maskedKey}`);
        lines.push("");
      } else {
        lines.push(`# ${provider.name} (not configured)`);
        lines.push(`# ${provider.envVarName}=`);
        lines.push("");
      }
    }

    const blob = new Blob([lines.join("\n")], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "provider-keys.env";
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  const handleTestAll = async () => {
    if (configuredSelected.length === 0) return;
    setIsTesting(true);
    setTestResults(null);

    try {
      const results = await Promise.all(
        configuredSelected.map(async (provider) => {
          try {
            const result = await testProviderKey({
              provider: provider.id,
              api_key: configuredKeys[provider.envVarName]!,
            });
            return { provider, result };
          } catch {
            return {
              provider,
              result: {
                success: false,
                message: "Connection test failed",
                details: "Network error or server unreachable",
              },
            };
          }
        }),
      );
      setTestResults(results);
    } finally {
      setIsTesting(false);
    }
  };

  const dismissResults = () => {
    setTestResults(null);
  };

  const successCount = testResults?.filter((r) => r.result.success).length ?? 0;
  const failureCount = testResults ? testResults.length - successCount : 0;

  return (
    <div className="space-y-3" data-testid="batch-action-bar">
      <div className="flex flex-wrap items-center gap-3 p-3 bg-blue-50 border border-blue-200 rounded-lg">
        <span className="text-sm font-medium text-blue-800" data-testid="batch-selection-count">
          {selectedProviders.length} provider{selectedProviders.length !== 1 ? "s" : ""} selected
        </span>

        <div className="flex items-center gap-2 ml-auto">
          <button
            type="button"
            onClick={handleTestAll}
            disabled={isTesting || configuredSelected.length === 0}
            data-testid="batch-test-all"
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-blue-700 bg-white border border-blue-300 rounded-md hover:bg-blue-50 transition-colors disabled:opacity-50 disabled:cursor-not-allowed focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            {isTesting ? (
              <Loader2 size={14} className="animate-spin" />
            ) : (
              <FlaskConical size={14} />
            )}
            {isTesting ? "Testing..." : "Test All"}
          </button>

          <button
            type="button"
            onClick={handleExportKeys}
            data-testid="batch-export-keys"
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-blue-700 bg-white border border-blue-300 rounded-md hover:bg-blue-50 transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            <Download size={14} />
            Export Keys
          </button>

          <button
            type="button"
            onClick={onDeleteSelected}
            data-testid="batch-delete-selected"
            className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-red-700 bg-white border border-red-300 rounded-md hover:bg-red-50 transition-colors focus:outline-none focus:ring-2 focus:ring-red-500"
          >
            <Trash2 size={14} />
            Delete Selected
          </button>

          <button
            type="button"
            onClick={onClearSelection}
            data-testid="batch-clear-selection"
            className="px-3 py-1.5 text-xs font-medium text-gray-600 hover:text-gray-800 transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500 rounded-md"
          >
            Clear
          </button>
        </div>
      </div>

      {/* Test results summary */}
      {testResults && (
        <div
          className="p-3 border rounded-lg space-y-2"
          data-testid="batch-test-results"
          style={{
            borderColor: failureCount > 0 ? "#fca5a5" : "#86efac",
            backgroundColor: failureCount > 0 ? "#fef2f2" : "#f0fdf4",
          }}
        >
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium text-gray-900" data-testid="batch-test-summary">
              {successCount} passed, {failureCount} failed
            </span>
            <button
              type="button"
              onClick={dismissResults}
              data-testid="batch-test-dismiss"
              className="text-xs text-gray-500 hover:text-gray-700 focus:outline-none focus:ring-2 focus:ring-blue-500 rounded-md px-1"
            >
              Dismiss
            </button>
          </div>
          <ul className="space-y-1">
            {testResults.map((r) => (
              <li
                key={r.provider.id}
                className="flex items-center gap-2 text-xs"
                data-testid={`batch-test-result-${r.provider.id}`}
              >
                {r.result.success ? (
                  <CheckCircle2 size={14} className="text-green-600 flex-shrink-0" />
                ) : (
                  <XCircle size={14} className="text-red-600 flex-shrink-0" />
                )}
                <span className="font-medium text-gray-800">{r.provider.name}:</span>
                <span className="text-gray-600 truncate">{r.result.message}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
