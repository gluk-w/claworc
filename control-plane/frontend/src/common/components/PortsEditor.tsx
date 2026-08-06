import { useEffect, useRef, useState } from "react";
import { Trash2 } from "lucide-react";
import type { PortSpec } from "@common/types/instance";

interface Row extends PortSpec {
  _id: number;
}

let seq = 0;
const nextId = () => ++seq;

const emptyRow = (): Row => ({
  _id: nextId(),
  name: "",
  container_port: 0,
  service_port: undefined,
  protocol: "",
});

function isEmptyRow(r: PortSpec): boolean {
  return !r.name && !r.container_port && !r.service_port && !r.protocol;
}

function buildRows(values: PortSpec[]): Row[] {
  const rows: Row[] = values.map((p) => ({ ...p, _id: nextId() }));
  rows.push(emptyRow());
  return rows;
}

// computePorts converts live rows into PortSpec[], skipping rows with no
// container_port (incomplete - callers that need strict validation, i.e.
// handleSave below, do their own pass instead).
function computePorts(rows: Row[]): PortSpec[] {
  const live = rows.filter((r) => !isEmptyRow(r));
  const result: PortSpec[] = [];
  for (const row of live) {
    if (!row.container_port) continue;
    const p: PortSpec = { container_port: row.container_port };
    if (row.name) p.name = row.name;
    if (row.service_port) p.service_port = row.service_port;
    if (row.protocol) p.protocol = row.protocol;
    result.push(p);
  }
  return result;
}

interface Props {
  values: PortSpec[];
  title: string;
  description: string;
  /** Required in managed mode (the default); unused in inline mode. */
  onSave?: (next: PortSpec[]) => Promise<void> | void;
  isSaving?: boolean;
  /**
   * When true, render the edit grid permanently (no display/edit toggle, no
   * Save/Cancel buttons) and report the current list via onChange. Mirrors
   * TolerationsEditor/SimpleKVEditor's inline mode.
   */
  inline?: boolean;
  onChange?: (next: PortSpec[]) => void;
}

export default function PortsEditor({
  values,
  title,
  description,
  onSave,
  isSaving,
  inline = false,
  onChange,
}: Props) {
  const [editing, setEditing] = useState(inline);
  const [rows, setRows] = useState<Row[]>(() => (inline ? buildRows(values) : []));
  const [error, setError] = useState<string | null>(null);

  const lastEmitRef = useRef<string>("");
  useEffect(() => {
    if (!inline || !onChange) return;
    const list = computePorts(rows);
    const serialized = JSON.stringify(list);
    if (serialized !== lastEmitRef.current) {
      lastEmitRef.current = serialized;
      onChange(list);
    }
  }, [rows, inline, onChange]);

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
      if (!isEmptyRow(last)) {
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
      if (!last || !isEmptyRow(last)) {
        next.push(emptyRow());
      }
      return next;
    });
  };

  const handleSave = async () => {
    if (!onSave) return;
    const live = rows.filter((r) => !isEmptyRow(r));
    const result: PortSpec[] = [];
    const seenPorts = new Set<number>();
    for (const row of live) {
      if (!row.container_port) {
        setError(`Enter a container port for "${row.name || "(unnamed)"}".`);
        return;
      }
      if (seenPorts.has(row.container_port)) {
        setError(`Duplicate container port ${row.container_port}.`);
        return;
      }
      seenPorts.add(row.container_port);
      const p: PortSpec = { container_port: row.container_port };
      if (row.name) p.name = row.name;
      if (row.service_port) p.service_port = row.service_port;
      if (row.protocol) p.protocol = row.protocol;
      result.push(p);
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
        values.length === 0 ? (
          <p className="text-sm text-gray-400 italic">No ports exposed.</p>
        ) : (
          <div className="divide-y divide-gray-100">
            {values.map((p, i) => (
              <div key={i} className="py-2 flex flex-wrap gap-x-6 gap-y-1 text-sm font-mono text-gray-700">
                {p.name && <span><span className="text-gray-400">name=</span>{p.name}</span>}
                <span><span className="text-gray-400">container=</span>{p.container_port}</span>
                {p.service_port && <span><span className="text-gray-400">service=</span>{p.service_port}</span>}
                <span><span className="text-gray-400">proto=</span>{p.protocol || "TCP"}</span>
              </div>
            ))}
          </div>
        )
      ) : (
        <div>
          <p className="text-xs text-gray-500 mb-2">
            Container is the port the app listens on. Service is the port it's reached on and
            defaults to Container when left blank.
          </p>
          <div className="grid grid-cols-[minmax(0,1fr)_7rem_7rem_7rem_1.75rem] gap-2 items-center mb-1">
            <span className="text-xs text-gray-500">Name</span>
            <span className="text-xs text-gray-500">Container</span>
            <span className="text-xs text-gray-500">Service</span>
            <span className="text-xs text-gray-500">Protocol</span>
            <span />
          </div>
          <div className="space-y-2">
            {rows.map((row) => {
              const isTrailing = row === rows[rows.length - 1] && isEmptyRow(row);
              return (
                <div
                  key={row._id}
                  className="grid grid-cols-[minmax(0,1fr)_7rem_7rem_7rem_1.75rem] gap-2 items-center"
                >
                  <input
                    type="text"
                    value={row.name ?? ""}
                    onChange={(e) => updateRow(row._id, { name: e.target.value })}
                    placeholder="http"
                    className={inputClass}
                  />
                  <input
                    type="number"
                    value={row.container_port || ""}
                    onChange={(e) => updateRow(row._id, { container_port: Number(e.target.value) || 0 })}
                    placeholder="8080"
                    className={inputClass}
                  />
                  <input
                    type="number"
                    value={row.service_port ?? ""}
                    onChange={(e) =>
                      updateRow(row._id, { service_port: e.target.value ? Number(e.target.value) : undefined })
                    }
                    placeholder="optional"
                    title="Defaults to the Container port if left blank."
                    className={inputClass}
                  />
                  <select
                    value={row.protocol ?? ""}
                    onChange={(e) => updateRow(row._id, { protocol: e.target.value as PortSpec["protocol"] })}
                    className={selectClass}
                  >
                    <option value="">TCP</option>
                    <option value="UDP">UDP</option>
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
