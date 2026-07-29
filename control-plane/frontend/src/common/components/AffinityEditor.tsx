import { useState } from "react";

interface Props {
  value: string;
  title: string;
  description: string;
  onSave?: (next: string) => Promise<void> | void;
  isSaving?: boolean;
  emptyMessage?: string;
}

export default function AffinityEditor({
  value,
  title,
  description,
  onSave,
  isSaving,
  emptyMessage = "No affinity configured.",
}: Props) {
  const [editing, setEditing] = useState(false);
  const [pending, setPending] = useState("");
  const [error, setError] = useState<string | null>(null);

  const beginEdit = () => {
    setPending(value ?? "");
    setError(null);
    setEditing(true);
  };

  const cancel = () => {
    setEditing(false);
    setError(null);
  };

  const handleSave = async () => {
    if (!onSave) return;
    const trimmed = pending.trim();
    if (trimmed !== "") {
      try {
        JSON.parse(trimmed);
      } catch {
        setError("Invalid JSON — check syntax and try again.");
        return;
      }
    }
    try {
      await onSave(trimmed);
      setEditing(false);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save.");
    }
  };

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-medium text-gray-900">{title}</h3>
        {!editing && (
          <button
            type="button"
            onClick={beginEdit}
            className="text-xs text-blue-600 hover:text-blue-800"
          >
            Edit
          </button>
        )}
      </div>
      <p className="text-xs text-gray-500 mb-4">{description}</p>

      {!editing ? (
        value ? (
          <pre className="text-xs font-mono text-gray-700 bg-gray-50 rounded p-3 overflow-x-auto whitespace-pre-wrap break-all">
            {(() => {
              try {
                return JSON.stringify(JSON.parse(value), null, 2);
              } catch {
                return value;
              }
            })()}
          </pre>
        ) : (
          <p className="text-sm text-gray-400 italic">{emptyMessage}</p>
        )
      ) : (
        <div>
          <textarea
            value={pending}
            onChange={(e) => {
              setPending(e.target.value);
              setError(null);
            }}
            rows={8}
            placeholder={'{\n  "nodeAffinity": {\n    ...\n  }\n}'}
            className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500 resize-y"
          />
          {error && <p className="text-xs text-red-600 mt-1">{error}</p>}
          <div className="flex justify-end gap-3 mt-3">
            <button
              type="button"
              onClick={cancel}
              disabled={isSaving}
              className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={handleSave}
              disabled={isSaving}
              className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {isSaving ? "Saving..." : "Save"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
