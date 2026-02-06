import { useState } from "react";
import { Eye, EyeOff } from "lucide-react";
import { LLM_API_KEY_OPTIONS } from "@/components/DynamicApiKeyEditor";

interface ProviderTableProps {
  globalApiKeys: Record<string, string>;
  instanceOverrides: string[];
  disabledProviders: string[];
  defaultModel: string;
  pendingNewKeys: Record<string, string>;
  pendingRemovals: Record<string, true>;
  onToggleEnabled: (key: string) => void;
  onDefaultModelChange: (key: string) => void;
  onAddKey: (key: string, value: string) => void;
  onRemoveKey: (key: string) => void;
  onUndoRemove: (key: string) => void;
  onUndoAdd: (key: string) => void;
  editable: boolean;
}

export default function ProviderTable({
  globalApiKeys,
  instanceOverrides,
  disabledProviders,
  defaultModel,
  pendingNewKeys,
  pendingRemovals,
  onToggleEnabled,
  onDefaultModelChange,
  onAddKey,
  onRemoveKey,
  onUndoRemove,
  onUndoAdd,
  editable,
}: ProviderTableProps) {
  const [newKeyName, setNewKeyName] = useState("");
  const [newKeyValue, setNewKeyValue] = useState("");
  const [showNewValue, setShowNewValue] = useState(false);

  // Global rows: providers with keys in settings.api_keys
  const globalRows = LLM_API_KEY_OPTIONS.filter(
    (opt) => opt.value in globalApiKeys,
  );

  // Instance-specific rows: overrides not in globalApiKeys (minus pending removals) + pending new keys not in globalApiKeys
  const instanceRows = [
    ...instanceOverrides
      .filter((k) => !(k in globalApiKeys) && !(k in pendingRemovals))
      .map((k) => ({ key: k, isPending: false })),
    ...Object.keys(pendingNewKeys)
      .filter((k) => !(k in globalApiKeys) && !instanceOverrides.includes(k))
      .map((k) => ({ key: k, isPending: true })),
  ];

  // All shown provider keys (for filtering the add dropdown)
  const shownKeys = new Set([
    ...globalRows.map((r) => r.value),
    ...instanceRows.map((r) => r.key),
    // Also include overrides that are pending removal (still shown with strikethrough)
    ...Object.keys(pendingRemovals).filter((k) => !(k in globalApiKeys)),
  ]);

  const handleAdd = () => {
    const name = newKeyName.trim();
    const value = newKeyValue.trim();
    if (!name || !value) return;
    onAddKey(name, value);
    setNewKeyName("");
    setNewKeyValue("");
  };

  const getLabel = (key: string) => {
    const opt = LLM_API_KEY_OPTIONS.find((o) => o.value === key);
    return opt ? opt.label : key;
  };

  return (
    <div className="space-y-0">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-gray-200">
            <th className="text-left py-2 px-2 text-xs font-medium text-gray-500 w-16">
              Default
            </th>
            <th className="text-left py-2 px-2 text-xs font-medium text-gray-500">
              Provider
            </th>
            <th className="text-right py-2 px-2 text-xs font-medium text-gray-500 w-32">
            </th>
          </tr>
        </thead>
        <tbody>
          {/* Global rows */}
          {globalRows.map((opt) => {
            const isDisabled = disabledProviders.includes(opt.value);
            const isDefault = defaultModel === opt.value;
            const hasOverride =
              instanceOverrides.includes(opt.value) &&
              !(opt.value in pendingRemovals);
            const hasPendingNew =
              opt.value in pendingNewKeys &&
              !instanceOverrides.includes(opt.value);
            const isPendingRemoval = opt.value in pendingRemovals;

            return (
              <tr key={opt.value} className="border-b border-gray-100">
                <td className="py-2 px-2">
                  <input
                    type="radio"
                    name="default_model"
                    checked={isDefault}
                    disabled={!editable || isDisabled}
                    onChange={() => onDefaultModelChange(opt.value)}
                    onClick={() => {
                      if (isDefault && editable) onDefaultModelChange("");
                    }}
                    className="text-blue-600 focus:ring-blue-500"
                  />
                </td>
                <td className="py-2 px-2">
                  <span className="text-gray-700">{opt.label}</span>
                  {hasOverride && (
                    <span className="ml-2 text-xs text-gray-400 font-mono">
                      (key set)
                    </span>
                  )}
                  {hasPendingNew && (
                    <span className="ml-2 text-xs text-green-600 font-mono">
                      (new)
                    </span>
                  )}
                  {isPendingRemoval && (
                    <span className="ml-2 text-xs text-red-500 font-mono">
                      (removing)
                    </span>
                  )}
                </td>
                <td className="py-2 px-2 text-right">
                  <div className="flex items-center justify-end gap-3">
                    {editable && hasOverride && (
                      <button
                        type="button"
                        onClick={() => onRemoveKey(opt.value)}
                        className="text-xs text-red-500 hover:text-red-700"
                      >
                        Remove key
                      </button>
                    )}
                    {editable && isPendingRemoval && (
                      <button
                        type="button"
                        onClick={() => onUndoRemove(opt.value)}
                        className="text-xs text-blue-600 hover:text-blue-800"
                      >
                        Undo
                      </button>
                    )}
                    {editable && hasPendingNew && (
                      <button
                        type="button"
                        onClick={() => onUndoAdd(opt.value)}
                        className="text-xs text-red-500 hover:text-red-700"
                      >
                        Remove
                      </button>
                    )}
                    <label className="flex items-center gap-1 text-xs">
                      <input
                        type="checkbox"
                        checked={!isDisabled}
                        disabled={!editable}
                        onChange={() => onToggleEnabled(opt.value)}
                        className="rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                      />
                      <span className="text-gray-500">Enable</span>
                    </label>
                  </div>
                </td>
              </tr>
            );
          })}

          {/* Instance-specific rows */}
          {instanceRows.map(({ key, isPending }) => {
            const isDisabled = disabledProviders.includes(key);
            const isDefault = defaultModel === key;

            return (
              <tr key={key} className="border-b border-gray-100">
                <td className="py-2 px-2">
                  <input
                    type="radio"
                    name="default_model"
                    checked={isDefault}
                    disabled={!editable || isDisabled}
                    onChange={() => onDefaultModelChange(key)}
                    onClick={() => {
                      if (isDefault && editable) onDefaultModelChange("");
                    }}
                    className="text-blue-600 focus:ring-blue-500"
                  />
                </td>
                <td className="py-2 px-2">
                  <span className="text-gray-700">{getLabel(key)}</span>
                  <span className="ml-2 text-xs text-gray-400 font-mono">
                    {isPending ? "(new)" : "(key set)"}
                  </span>
                </td>
                <td className="py-2 px-2 text-right">
                  {editable && (
                    <button
                      type="button"
                      onClick={() =>
                        isPending ? onUndoAdd(key) : onRemoveKey(key)
                      }
                      className="text-xs text-red-500 hover:text-red-700"
                    >
                      Remove
                    </button>
                  )}
                </td>
              </tr>
            );
          })}

          {/* Pending removal rows for instance-specific keys */}
          {Object.keys(pendingRemovals)
            .filter(
              (k) =>
                !(k in globalApiKeys) &&
                instanceOverrides.includes(k),
            )
            .map((key) => {
              const isDefault = defaultModel === key;
              return (
                <tr
                  key={key}
                  className="border-b border-gray-100 opacity-50"
                >
                  <td className="py-2 px-2">
                    <input
                      type="radio"
                      name="default_model"
                      checked={isDefault}
                      disabled
                      className="text-blue-600 focus:ring-blue-500"
                    />
                  </td>
                  <td className="py-2 px-2">
                    <span className="text-gray-700 line-through">
                      {getLabel(key)}
                    </span>
                    <span className="ml-2 text-xs text-red-500 font-mono">
                      (removing)
                    </span>
                  </td>
                  <td className="py-2 px-2 text-right">
                    {editable && (
                      <button
                        type="button"
                        onClick={() => onUndoRemove(key)}
                        className="text-xs text-blue-600 hover:text-blue-800"
                      >
                        Undo
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
        </tbody>
      </table>

      {/* Add row */}
      {editable && (
        <div className="flex gap-2 pt-3">
          <select
            value={newKeyName}
            onChange={(e) => setNewKeyName(e.target.value)}
            className="w-[160px] px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white"
          >
            <option value="" disabled>
              Select provider...
            </option>
            {LLM_API_KEY_OPTIONS.filter(
              (opt) => !shownKeys.has(opt.value),
            ).map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
          <div className="relative flex-1">
            <input
              type={showNewValue ? "text" : "password"}
              value={newKeyValue}
              onChange={(e) => setNewKeyValue(e.target.value)}
              placeholder="API key value"
              className="w-full px-3 py-1.5 pr-10 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
            <button
              type="button"
              onClick={() => setShowNewValue(!showNewValue)}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
            >
              {showNewValue ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
          </div>
          <button
            type="button"
            onClick={handleAdd}
            disabled={!newKeyName.trim() || !newKeyValue.trim()}
            className="px-3 py-1.5 text-sm font-medium text-blue-600 border border-blue-300 rounded-md hover:bg-blue-50 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            Add
          </button>
        </div>
      )}
    </div>
  );
}
