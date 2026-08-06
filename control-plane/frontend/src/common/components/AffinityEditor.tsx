import { useEffect, useRef, useState } from "react";

interface Props {
  value: string;
  title: string;
  description: string;
  /** Required in managed mode (the default); unused in inline mode. */
  onSave?: (next: string) => Promise<void> | void;
  isSaving?: boolean;
  emptyMessage?: string;
  /**
   * When true, render the textarea permanently (no display/edit toggle, no
   * Save/Cancel buttons) and report the current valid JSON via onChange.
   * Mirrors EnvVarsEditor/SimpleKVEditor's inline mode. Invalid JSON shows an
   * error but does not emit - the last valid value stands until fixed.
   */
  inline?: boolean;
  onChange?: (next: string) => void;
}

export default function AffinityEditor({
  value,
  title,
  description,
  onSave,
  isSaving,
  emptyMessage = "No affinity configured.",
  inline = false,
  onChange,
}: Props) {
  const [editing, setEditing] = useState(inline);
  const [pending, setPending] = useState(() => (inline ? value ?? "" : ""));
  const [error, setError] = useState<string | null>(null);

  const lastEmitRef = useRef<string>("");
  useEffect(() => {
    if (!inline || !onChange) return;
    const trimmed = pending.trim();
    if (trimmed !== "") {
      try {
        JSON.parse(trimmed);
      } catch {
        return; // invalid JSON: don't emit, error is already shown on input
      }
    }
    if (trimmed !== lastEmitRef.current) {
      lastEmitRef.current = trimmed;
      onChange(trimmed);
    }
  }, [pending, inline, onChange]);

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
        {!inline && !editing && (
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
              const next = e.target.value;
              setPending(next);
              if (inline) {
                const trimmed = next.trim();
                if (trimmed === "") {
                  setError(null);
                  return;
                }
                try {
                  JSON.parse(trimmed);
                  setError(null);
                } catch {
                  setError("Invalid JSON — check syntax and try again.");
                }
              } else {
                setError(null);
              }
            }}
            rows={8}
            placeholder={'{\n  "nodeAffinity": {\n    ...\n  }\n}'}
            className="w-full px-3 py-2 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500 resize-y"
          />
          {error && <p className="text-xs text-red-600 mt-1">{error}</p>}
          {!inline && (
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
          )}
        </div>
      )}
    </div>
  );
}
