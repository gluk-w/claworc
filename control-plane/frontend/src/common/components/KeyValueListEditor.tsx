import { useState } from "react";
import { Eye, EyeOff, Pencil, Plus, Trash2 } from "lucide-react";

// Keep in sync with ReservedEnvVarNames in control-plane/internal/handlers/envvars.go.
// These names are set by the control plane at container start and cannot be
// overridden by user-defined env vars.
const RESERVED_ENV_VAR_NAMES = new Set([
  "OPENCLAW_GATEWAY_TOKEN",
  "CLAWORC_INSTANCE_ID",
  "OPENCLAW_INITIAL_MODELS",
  "OPENCLAW_INITIAL_PROVIDERS",
]);

const NAME_REGEX = /^[A-Z_][A-Z0-9_]*$/;

interface Props {
  /** Existing values from the API, keyed by name. Values are already masked (e.g. "****abcd"). */
  values: Record<string, string>;
  /** Names queued for deletion but not yet saved. They appear struck-through. */
  pendingUnset?: Set<string>;
  /** New plaintext values queued for save, keyed by name. Used to render the pending indicator. */
  pendingSet?: Record<string, string>;
  /** Called when the admin commits a new plaintext value for `name`. */
  onSet: (name: string, value: string) => void;
  /** Called when the admin removes `name`. */
  onUnset: (name: string) => void;
  /** Short text rendered as empty-state placeholder. */
  emptyMessage?: string;
}

function validateName(name: string): string | null {
  if (name === "") return "Name is required.";
  if (!NAME_REGEX.test(name)) {
    return "Name must match [A-Z_][A-Z0-9_]*.";
  }
  if (RESERVED_ENV_VAR_NAMES.has(name)) {
    return `"${name}" is reserved for internal use.`;
  }
  return null;
}

export default function KeyValueListEditor({
  values,
  pendingUnset,
  pendingSet,
  onSet,
  onUnset,
  emptyMessage = "No variables set.",
}: Props) {
  const [editingName, setEditingName] = useState<string | null>(null);
  const [editingValue, setEditingValue] = useState("");
  const [showEditing, setShowEditing] = useState(false);

  const [newName, setNewName] = useState("");
  const [newValue, setNewValue] = useState("");
  const [showNew, setShowNew] = useState(false);
  const [showNewValue, setShowNewValue] = useState(false);
  const [addError, setAddError] = useState<string | null>(null);

  const allNames = Array.from(
    new Set([...Object.keys(values), ...Object.keys(pendingSet ?? {})])
  ).sort();

  const beginEdit = (name: string) => {
    setEditingName(name);
    setEditingValue("");
    setShowEditing(false);
  };
  const cancelEdit = () => {
    setEditingName(null);
    setEditingValue("");
  };
  const commitEdit = () => {
    if (!editingName) return;
    onSet(editingName, editingValue);
    cancelEdit();
  };

  const commitAdd = () => {
    const err = validateName(newName);
    if (err) {
      setAddError(err);
      return;
    }
    if (allNames.includes(newName)) {
      setAddError(`"${newName}" already exists — use Edit to change its value.`);
      return;
    }
    onSet(newName, newValue);
    setNewName("");
    setNewValue("");
    setShowNew(false);
    setShowNewValue(false);
    setAddError(null);
  };

  return (
    <div>
      {allNames.length === 0 ? (
        <p className="text-xs text-gray-400 italic mb-4">{emptyMessage}</p>
      ) : (
        <div className="divide-y divide-gray-100 mb-4">
          {allNames.map((name) => {
            const markedForDelete = pendingUnset?.has(name) ?? false;
            const pendingValue = pendingSet?.[name];
            const displayValue =
              pendingValue !== undefined
                ? pendingValue
                  ? "****" + pendingValue.slice(-4)
                  : "(not set)"
                : values[name] ?? "(not set)";
            const isEditing = editingName === name;
            return (
              <div key={name} className="py-2 flex items-center gap-2">
                <div className={`min-w-0 flex-1 ${markedForDelete ? "opacity-50" : ""}`}>
                  <div className="flex items-center gap-2">
                    <span
                      className={`text-sm font-mono ${
                        markedForDelete ? "line-through text-gray-400" : "text-gray-900"
                      }`}
                    >
                      {name}
                    </span>
                    {pendingValue !== undefined && !markedForDelete && (
                      <span className="text-[10px] uppercase tracking-wide text-amber-600 bg-amber-50 px-1.5 py-0.5 rounded">
                        pending
                      </span>
                    )}
                    {markedForDelete && (
                      <span className="text-[10px] uppercase tracking-wide text-red-600 bg-red-50 px-1.5 py-0.5 rounded">
                        pending delete
                      </span>
                    )}
                  </div>
                  {isEditing ? (
                    <div className="flex gap-2 mt-1.5">
                      <div className="relative flex-1">
                        <input
                          type={showEditing ? "text" : "password"}
                          value={editingValue}
                          onChange={(e) => setEditingValue(e.target.value)}
                          onKeyDown={(e) => {
                            if (e.key === "Enter") commitEdit();
                            if (e.key === "Escape") cancelEdit();
                          }}
                          autoFocus
                          placeholder="New value"
                          className="w-full px-3 py-1 pr-9 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                        />
                        <button
                          type="button"
                          onClick={() => setShowEditing((s) => !s)}
                          className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
                        >
                          {showEditing ? <EyeOff size={14} /> : <Eye size={14} />}
                        </button>
                      </div>
                      <button
                        type="button"
                        onClick={commitEdit}
                        className="px-3 py-1 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
                      >
                        Save
                      </button>
                      <button
                        type="button"
                        onClick={cancelEdit}
                        className="px-3 py-1 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
                      >
                        Cancel
                      </button>
                    </div>
                  ) : (
                    <p className="text-xs font-mono text-gray-500 mt-0.5">{displayValue}</p>
                  )}
                </div>
                {!isEditing && (
                  <div className="shrink-0 flex gap-1">
                    <button
                      type="button"
                      onClick={() => beginEdit(name)}
                      className="p-1 text-gray-400 hover:text-gray-600 rounded"
                      title="Edit value"
                      disabled={markedForDelete}
                    >
                      <Pencil size={14} />
                    </button>
                    <button
                      type="button"
                      onClick={() => onUnset(name)}
                      className="p-1 text-gray-400 hover:text-red-600 rounded"
                      title="Remove"
                      disabled={markedForDelete}
                    >
                      <Trash2 size={14} />
                    </button>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {showNew ? (
        <div className="bg-gray-50 border border-gray-200 rounded-md p-3">
          <div className="grid grid-cols-[1fr_1fr_auto_auto] gap-2 items-start">
            <div>
              <input
                type="text"
                value={newName}
                onChange={(e) => {
                  setNewName(e.target.value.toUpperCase());
                  if (addError) setAddError(null);
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter") commitAdd();
                  if (e.key === "Escape") {
                    setShowNew(false);
                    setShowNewValue(false);
                    setAddError(null);
                    setNewName("");
                    setNewValue("");
                  }
                }}
                placeholder="NAME"
                autoFocus
                className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <div className="relative">
              <input
                type={showNewValue ? "text" : "password"}
                value={newValue}
                onChange={(e) => setNewValue(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") commitAdd();
                }}
                placeholder="value"
                className="w-full px-3 py-1.5 pr-9 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
              <button
                type="button"
                onClick={() => setShowNewValue((s) => !s)}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
              >
                {showNewValue ? <EyeOff size={14} /> : <Eye size={14} />}
              </button>
            </div>
            <button
              type="button"
              onClick={commitAdd}
              className="px-3 py-1.5 text-xs font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700"
            >
              Add
            </button>
            <button
              type="button"
              onClick={() => {
                setShowNew(false);
                setShowNewValue(false);
                setAddError(null);
                setNewName("");
                setNewValue("");
              }}
              className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
            >
              Cancel
            </button>
          </div>
          {addError && <p className="text-xs text-red-600 mt-2">{addError}</p>}
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setShowNew(true)}
          className="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
        >
          <Plus size={12} />
          Add Variable
        </button>
      )}
    </div>
  );
}
