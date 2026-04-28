import { useState } from "react";
import { Trash2 } from "lucide-react";
import type { Toleration } from "@/types/instance";

interface Row extends Toleration {
  _id: number;
}

let seq = 0;
const nextId = () => ++seq;

const emptyRow = (): Row => ({
  _id: nextId(),
  key: "",
  operator: "Equal",
  value: "",
  effect: "",
});

function buildRows(values: Toleration[]): Row[] {
  const rows: Row[] = values.map((t) => ({ ...t, _id: nextId(), effect: t.effect ?? "" }));
  rows.push(emptyRow());
  return rows;
}

interface Props {
  values: Toleration[];
  onSave?: (next: Toleration[]) => Promise<void> | void;
  isSaving?: boolean;
}

export default function TolerationsEditor({ values, onSave, isSaving }: Props) {
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
      const next = prev.map((r) => (r._id === id ? { ...r, ...patch } : r));
      const last = next[next.length - 1]!;
      if (last.key !== "" || last.operator !== "Equal" || last.value !== "" || last.effect !== "") {
        next.push(emptyRow());
      }
      return next;
    });
  };

  const deleteRow = (id: number) => {
    setError(null);
    setRows((prev) => {
      const next = prev.filter((r) => r._id !== id);
      const last = next[next.length - 1];
      if (!last || last.key !== "" || last.operator !== "Equal" || last.value !== "" || last.effect !== "") {
        next.push(emptyRow());
      }
      return next;
    });
  };

  const handleSave = async () => {
    if (!onSave) return;
    const live = rows.filter(
      (r) => r.key !== "" || r.operator !== "Equal" || r.value !== "" || r.effect !== ""
    );
    const result: Toleration[] = [];
    for (const row of live) {
      if (row.operator === "Equal" && row.value === "") {
        setError(`Enter a value for the toleration with key "${row.key || "(empty)"}" (operator Equal requires a value).`);
        return;
      }
      const t: Toleration = { operator: row.operator };
      if (row.key) t.key = row.key;
      if (row.value && row.operator === "Equal") t.value = row.value;
      if (row.effect) t.effect = row.effect as Toleration["effect"];
      result.push(t);
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

  const selectClass =
    "px-2 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white";
  const inputClass =
    "w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500";

  return (
    <div>
      <div className="flex items-center justify-between mb-2">
        <span className="text-xs font-medium text-gray-700">Tolerations</span>
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

      {!editing ? (
        values.length === 0 ? (
          <p className="text-sm text-gray-400 italic">No tolerations configured.</p>
        ) : (
          <div className="divide-y divide-gray-100">
            {values.map((t, i) => (
              <div key={i} className="py-2 flex flex-wrap gap-x-6 gap-y-1 text-sm font-mono text-gray-700">
                {t.key && <span><span className="text-gray-400">key=</span>{t.key}</span>}
                <span><span className="text-gray-400">op=</span>{t.operator}</span>
                {t.value && <span><span className="text-gray-400">value=</span>{t.value}</span>}
                {t.effect && <span><span className="text-gray-400">effect=</span>{t.effect}</span>}
              </div>
            ))}
          </div>
        )
      ) : (
        <div>
          <div className="grid grid-cols-[minmax(0,1fr)_7rem_minmax(0,1fr)_10rem_1.75rem] gap-2 items-center mb-1">
            <span className="text-xs text-gray-500">Key</span>
            <span className="text-xs text-gray-500">Operator</span>
            <span className="text-xs text-gray-500">Value</span>
            <span className="text-xs text-gray-500">Effect</span>
            <span />
          </div>
          <div className="space-y-2">
            {rows.map((row) => {
              const isTrailing =
                row === rows[rows.length - 1] &&
                row.key === "" &&
                row.operator === "Equal" &&
                row.value === "" &&
                row.effect === "";
              return (
                <div
                  key={row._id}
                  className="grid grid-cols-[minmax(0,1fr)_7rem_minmax(0,1fr)_10rem_1.75rem] gap-2 items-center"
                >
                  <input
                    type="text"
                    value={row.key ?? ""}
                    onChange={(e) => updateRow(row._id, { key: e.target.value })}
                    placeholder="node.kubernetes.io/…"
                    className={inputClass}
                  />
                  <select
                    value={row.operator}
                    onChange={(e) =>
                      updateRow(row._id, {
                        operator: e.target.value as "Equal" | "Exists",
                        ...(e.target.value === "Exists" ? { value: "" } : {}),
                      })
                    }
                    className={selectClass}
                  >
                    <option value="Equal">Equal</option>
                    <option value="Exists">Exists</option>
                  </select>
                  <input
                    type="text"
                    value={row.value ?? ""}
                    onChange={(e) => updateRow(row._id, { value: e.target.value })}
                    placeholder="value"
                    disabled={row.operator === "Exists"}
                    className={`${inputClass} disabled:bg-gray-50 disabled:text-gray-400`}
                  />
                  <select
                    value={row.effect ?? ""}
                    onChange={(e) => updateRow(row._id, { effect: e.target.value as Toleration["effect"] })}
                    className={selectClass}
                  >
                    <option value="">Any effect</option>
                    <option value="NoSchedule">NoSchedule</option>
                    <option value="PreferNoSchedule">PreferNoSchedule</option>
                    <option value="NoExecute">NoExecute</option>
                  </select>
                  <button
                    type="button"
                    onClick={() => deleteRow(row._id)}
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
