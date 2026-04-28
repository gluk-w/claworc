import { useEffect, useMemo, useRef, useState } from "react";
import { Trash2 } from "lucide-react";

// Keep in sync with ReservedEnvVarNames in control-plane/internal/handlers/envvars.go.
const RESERVED = new Set([
  "OPENCLAW_GATEWAY_TOKEN",
  "CLAWORC_INSTANCE_ID",
  "OPENCLAW_INITIAL_MODELS",
  "OPENCLAW_INITIAL_PROVIDERS",
]);

const NAME_REGEX = /^[A-Z_][A-Z0-9_]*$/;

export interface EnvVarsDelta {
  set: Record<string, string>;
  unset: string[];
}

interface Props {
  /** Current plaintext values from the API. */
  values: Record<string, string>;
  /** Section title shown in the card header. */
  title: string;
  /** Help text rendered below the title. */
  description: string;
  /**
   * Called when the admin saves. Required in managed mode (the default);
   * unused in inline mode.
   */
  onSave?: (delta: EnvVarsDelta) => Promise<void> | void;
  /** Disables the Save button; label flips to "Saving…". */
  isSaving?: boolean;
  /** Shown when values is empty and we're in display mode. */
  emptyMessage?: string;
  /**
   * When true, render the edit grid permanently (no display/edit toggle, no
   * Save/Cancel buttons) and report the current name→value map via onChange.
   * Used by forms (e.g. instance creation) where the parent owns the submit
   * action.
   */
  inline?: boolean;
  /** Inline-mode change callback; fires on every keystroke after validation. */
  onChange?: (vars: Record<string, string>) => void;
}

interface EditRow {
  id: number;
  originalName: string | null; // null when this row was added during the current edit
  originalValue: string; // value loaded from the API; used for change detection
  name: string;
  value: string;
}

let rowIdSeq = 0;
const newRowId = () => ++rowIdSeq;

function buildInitialRows(values: Record<string, string>): EditRow[] {
  const names = Object.keys(values).sort();
  const rows: EditRow[] = names.map((n) => ({
    id: newRowId(),
    originalName: n,
    originalValue: values[n] ?? "",
    name: n,
    value: values[n] ?? "",
  }));
  // Always a trailing empty row — the user never has to click "Add".
  rows.push({ id: newRowId(), originalName: null, originalValue: "", name: "", value: "" });
  return rows;
}

export default function EnvVarsEditor({
  values,
  title,
  description,
  onSave,
  isSaving,
  emptyMessage = "No environment variables configured.",
  inline = false,
  onChange,
}: Props) {
  const [mode, setMode] = useState<"display" | "editing">(inline ? "editing" : "display");
  const [rows, setRows] = useState<EditRow[]>(() =>
    inline ? buildInitialRows(values) : [],
  );
  const [error, setError] = useState<string | null>(null);

  // In inline mode, report the current valid map upward whenever rows change.
  // We keep the previous emit in a ref to avoid pushing identical updates that
  // would re-trigger parent state churn.
  const lastEmitRef = useRef<string>("");
  useEffect(() => {
    if (!inline || !onChange) return;
    const map: Record<string, string> = {};
    for (const r of rows) {
      if (r.name === "" && r.value === "") continue;
      if (!NAME_REGEX.test(r.name)) continue;
      if (RESERVED.has(r.name)) continue;
      if (map[r.name] !== undefined) continue; // duplicate; skip
      map[r.name] = r.value;
    }
    const serialized = JSON.stringify(map);
    if (serialized !== lastEmitRef.current) {
      lastEmitRef.current = serialized;
      onChange(map);
    }
  }, [rows, inline, onChange]);

  const beginEdit = () => {
    setRows(buildInitialRows(values));
    setError(null);
    setMode("editing");
  };

  const cancel = () => {
    setMode("display");
    setRows([]);
    setError(null);
  };

  const updateRow = (id: number, patch: Partial<EditRow>) => {
    setError(null);
    setRows((prev) => {
      const next = prev.map((r) => (r.id === id ? { ...r, ...patch } : r));
      // Auto-append a new trailing empty row whenever the current trailing row
      // gains content. This keeps the "always an empty row to type into" promise.
      const last = next[next.length - 1];
      if (last && (last.name !== "" || last.value !== "")) {
        next.push({ id: newRowId(), originalName: null, originalValue: "", name: "", value: "" });
      }
      return next;
    });
  };

  const deleteRow = (id: number) => {
    setError(null);
    setRows((prev) => {
      const next = prev.filter((r) => r.id !== id);
      // Guarantee at least one trailing empty row after delete.
      if (next.length === 0 || next[next.length - 1]!.name !== "" || next[next.length - 1]!.value !== "") {
        next.push({ id: newRowId(), originalName: null, originalValue: "", name: "", value: "" });
      }
      return next;
    });
  };

  // Compute the delta from the current row state against `values`.
  // Returns either a valid delta or an error message suitable for display.
  const computeDelta = (): { delta: EnvVarsDelta } | { errorMessage: string } => {
    const set: Record<string, string> = {};
    const unset: string[] = [];
    const seenNames = new Set<string>();

    // Ignore rows that are completely empty — they're the trailing placeholder
    // or an abandoned row the user cleared out.
    const liveRows = rows.filter((r) => r.name !== "" || r.value !== "");

    for (const row of liveRows) {
      if (row.name === "") {
        return { errorMessage: "Enter a name for every variable with a value." };
      }
      if (!NAME_REGEX.test(row.name)) {
        return { errorMessage: `Invalid name "${row.name}": must match [A-Z_][A-Z0-9_]*.` };
      }
      if (RESERVED.has(row.name)) {
        return { errorMessage: `"${row.name}" is reserved for internal use.` };
      }
      if (seenNames.has(row.name)) {
        return { errorMessage: `Duplicate name "${row.name}".` };
      }
      seenNames.add(row.name);

      const renamed = row.originalName !== null && row.originalName !== row.name;
      const isNew = row.originalName === null;
      const valueChanged = row.value !== row.originalValue;

      if ((isNew || renamed || valueChanged) && row.value === "") {
        return { errorMessage: `Enter a value for "${row.name}".` };
      }
      if (renamed) {
        unset.push(row.originalName!);
      }
      if (isNew || renamed || valueChanged) {
        set[row.name] = row.value;
      }
    }

    // Removed rows: originalNames present in `values` but gone from liveRows
    // (and not renamed — renames already unset the old name above).
    const finalNames = new Set(liveRows.map((r) => r.name));
    for (const original of Object.keys(values)) {
      const stillPresent =
        liveRows.some((r) => r.originalName === original && r.name === original) ||
        finalNames.has(original);
      if (!stillPresent && !unset.includes(original)) {
        unset.push(original);
      }
    }

    return { delta: { set, unset } };
  };

  const handleSave = async () => {
    if (!onSave) return;
    const result = computeDelta();
    if ("errorMessage" in result) {
      setError(result.errorMessage);
      return;
    }
    const { delta } = result;
    const hasChanges = Object.keys(delta.set).length > 0 || delta.unset.length > 0;
    if (!hasChanges) {
      // Nothing to save — just exit edit mode without hitting the server.
      cancel();
      return;
    }
    try {
      await onSave(delta);
      setMode("display");
      setRows([]);
      setError(null);
    } catch (err) {
      // The parent mutation usually fires its own error toast; keep a local
      // message so the admin stays in edit mode with their pending changes.
      const msg = err instanceof Error ? err.message : "Failed to save environment variables.";
      setError(msg);
    }
  };

  const valueKeys = useMemo(() => Object.keys(values).sort(), [values]);

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-medium text-gray-900">{title}</h3>
        {!inline && mode === "display" && (
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

      {mode === "display" ? (
        valueKeys.length === 0 ? (
          <p className="text-sm text-gray-400 italic">{emptyMessage}</p>
        ) : (
          <div className="divide-y divide-gray-100">
            {valueKeys.map((k) => (
              <div key={k} className="py-2 flex items-center justify-between gap-4">
                <span className="text-sm font-mono text-gray-900">{k}</span>
                <span className="text-xs font-mono text-gray-500 truncate">{values[k]}</span>
              </div>
            ))}
          </div>
        )
      ) : (
        <div>
          {/* Grid: name | value | fixed-width delete-button column so every row
              has identical column widths regardless of whether the trash icon
              is shown. The trailing empty row hides its button with
              `invisible` so alignment stays pixel-perfect. */}
          <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_1.75rem] gap-2 items-center mb-1">
            <span className="text-xs text-gray-500">Name</span>
            <span className="text-xs text-gray-500">Value</span>
            <span />
          </div>
          <div className="space-y-2">
            {rows.map((row) => {
              const isTrailingEmpty =
                row === rows[rows.length - 1] && row.name === "" && row.value === "";
              return (
                <div
                  key={row.id}
                  className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_1.75rem] gap-2 items-center"
                >
                  <input
                    type="text"
                    value={row.name}
                    onChange={(e) => updateRow(row.id, { name: e.target.value.toUpperCase() })}
                    placeholder="NAME"
                    className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                  <input
                    type="text"
                    value={row.value}
                    onChange={(e) => updateRow(row.id, { value: e.target.value })}
                    placeholder="value"
                    className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                  <button
                    type="button"
                    onClick={() => deleteRow(row.id)}
                    className={`p-1 text-gray-400 hover:text-red-600 transition-colors ${
                      isTrailingEmpty ? "invisible" : ""
                    }`}
                    title="Delete"
                    tabIndex={isTrailingEmpty ? -1 : 0}
                  >
                    <Trash2 size={14} />
                  </button>
                </div>
              );
            })}
          </div>

          {error && <p className="text-xs text-red-600 mt-3">{error}</p>}

          {!inline && (
            <div className="flex justify-end gap-3 mt-4">
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
