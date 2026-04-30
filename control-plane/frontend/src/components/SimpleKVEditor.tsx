import { useState } from "react";
import { Trash2 } from "lucide-react";

interface Props {
  values: Record<string, string>;
  title: string;
  description: string;
  onSave?: (next: Record<string, string>) => Promise<void> | void;
  isSaving?: boolean;
  emptyMessage?: string;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}

interface Row {
  id: number;
  key: string;
  value: string;
}

let seq = 0;
const nextId = () => ++seq;

function buildRows(values: Record<string, string>): Row[] {
  const rows: Row[] = Object.entries(values)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([k, v]) => ({ id: nextId(), key: k, value: v }));
  rows.push({ id: nextId(), key: "", value: "" });
  return rows;
}

export default function SimpleKVEditor({
  values,
  title,
  description,
  onSave,
  isSaving,
  emptyMessage = "None configured.",
  keyPlaceholder = "key",
  valuePlaceholder = "value",
}: Props) {
  const [editing, setEditing] = useState(false);
  const [rows, setRows] = useState<Row[]>([]);
  const [error, setError] = useState<string | null>(null);

  const beginEdit = () => {
    setRows(buildRows(values));
    setError(null);
    setEditing(true);
  };

  const cancel = () => {
    setEditing(false);
    setRows([]);
    setError(null);
  };

  const updateRow = (id: number, patch: Partial<Row>) => {
    setError(null);
    setRows((prev) => {
      const next = prev.map((r) => (r.id === id ? { ...r, ...patch } : r));
      const last = next[next.length - 1];
      if (last && (last.key !== "" || last.value !== "")) {
        next.push({ id: nextId(), key: "", value: "" });
      }
      return next;
    });
  };

  const deleteRow = (id: number) => {
    setError(null);
    setRows((prev) => {
      const next = prev.filter((r) => r.id !== id);
      const last = next[next.length - 1];
      if (!last || last.key !== "" || last.value !== "") {
        next.push({ id: nextId(), key: "", value: "" });
      }
      return next;
    });
  };

  const handleSave = async () => {
    if (!onSave) return;
    const live = rows.filter((r) => r.key !== "" || r.value !== "");
    const result: Record<string, string> = {};
    const seen = new Set<string>();
    for (const row of live) {
      if (row.key === "") {
        setError("Enter a key for every row with a value.");
        return;
      }
      if (row.value === "") {
        setError(`Enter a value for "${row.key}".`);
        return;
      }
      if (seen.has(row.key)) {
        setError(`Duplicate key "${row.key}".`);
        return;
      }
      seen.add(row.key);
      result[row.key] = row.value;
    }
    try {
      await onSave(result);
      setEditing(false);
      setRows([]);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to save.");
    }
  };

  const displayKeys = Object.keys(values).sort();

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
        displayKeys.length === 0 ? (
          <p className="text-sm text-gray-400 italic">{emptyMessage}</p>
        ) : (
          <div className="divide-y divide-gray-100">
            {displayKeys.map((k) => (
              <div key={k} className="py-2 flex items-center justify-between gap-4">
                <span className="text-sm font-mono text-gray-900">{k}</span>
                <span className="text-xs font-mono text-gray-500 truncate">{values[k]}</span>
              </div>
            ))}
          </div>
        )
      ) : (
        <div>
          <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_1.75rem] gap-2 items-center mb-1">
            <span className="text-xs text-gray-500">Key</span>
            <span className="text-xs text-gray-500">Value</span>
            <span />
          </div>
          <div className="space-y-2">
            {rows.map((row) => {
              const isTrailing =
                row === rows[rows.length - 1] && row.key === "" && row.value === "";
              return (
                <div
                  key={row.id}
                  className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_1.75rem] gap-2 items-center"
                >
                  <input
                    type="text"
                    value={row.key}
                    onChange={(e) => updateRow(row.id, { key: e.target.value })}
                    placeholder={keyPlaceholder}
                    className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                  <input
                    type="text"
                    value={row.value}
                    onChange={(e) => updateRow(row.id, { value: e.target.value })}
                    placeholder={valuePlaceholder}
                    className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                  <button
                    type="button"
                    onClick={() => deleteRow(row.id)}
                    className={`p-1 text-gray-400 hover:text-red-600 transition-colors ${
                      isTrailing ? "invisible" : ""
                    }`}
                    title="Delete"
                    tabIndex={isTrailing ? -1 : 0}
                  >
                    <Trash2 size={14} />
                  </button>
                </div>
              );
            })}
          </div>

          {error && <p className="text-xs text-red-600 mt-3">{error}</p>}

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
        </div>
      )}
    </div>
  );
}
