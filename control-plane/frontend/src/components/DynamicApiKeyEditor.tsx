import { useState } from "react";
import { Eye, EyeOff, X, Plus } from "lucide-react";

export const LLM_API_KEY_OPTIONS = [
  { value: "ANTHROPIC_API_KEY", label: "Anthropic" },
  { value: "OPENAI_API_KEY", label: "OpenAI" },
  { value: "GOOGLE_API_KEY", label: "Google (Gemini)" },
  { value: "MISTRAL_API_KEY", label: "Mistral" },
  { value: "GROQ_API_KEY", label: "Groq" },
  { value: "DEEPSEEK_API_KEY", label: "DeepSeek" },
  { value: "XAI_API_KEY", label: "xAI (Grok)" },
  { value: "COHERE_API_KEY", label: "Cohere" },
  { value: "TOGETHER_API_KEY", label: "Together AI" },
  { value: "FIREWORKS_API_KEY", label: "Fireworks AI" },
  { value: "CEREBRAS_API_KEY", label: "Cerebras" },
  { value: "PERPLEXITY_API_KEY", label: "Perplexity" },
  { value: "OPENROUTER_API_KEY", label: "OpenRouter" },
];

interface DynamicApiKeyEditorProps {
  /** Current keys with masked values (e.g. { "ANTHROPIC_API_KEY": "****abcd" }) */
  keys: Record<string, string>;
  /** Called with key name → new value for changes, or key name in deletions */
  onUpdate: (keyName: string, value: string) => void;
  onDelete: (keyName: string) => void;
}

function KeyRow({
  keyName,
  maskedValue,
  onUpdate,
  onDelete,
}: {
  keyName: string;
  maskedValue: string;
  onUpdate: (value: string) => void;
  onDelete: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState("");
  const [show, setShow] = useState(false);

  return (
    <div className="flex items-center gap-3">
      <span className="text-sm font-mono text-gray-700 min-w-[180px]">
        {keyName}
      </span>
      {editing ? (
        <div className="flex gap-2 flex-1">
          <div className="relative flex-1">
            <input
              type={show ? "text" : "password"}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              className="w-full px-3 py-1.5 pr-10 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              placeholder="Enter new key"
            />
            <button
              type="button"
              onClick={() => setShow(!show)}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-gray-400 hover:text-gray-600"
            >
              {show ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
          </div>
          <button
            type="button"
            onClick={() => {
              if (value.trim()) onUpdate(value.trim());
              setEditing(false);
              setValue("");
            }}
            disabled={!value.trim()}
            className="px-3 py-1.5 text-xs text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50"
          >
            Save
          </button>
          <button
            type="button"
            onClick={() => {
              setEditing(false);
              setValue("");
            }}
            className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Cancel
          </button>
        </div>
      ) : (
        <div className="flex items-center gap-2 flex-1">
          <span className="text-sm text-gray-500 font-mono">
            {maskedValue || "(not set)"}
          </span>
          <button
            type="button"
            onClick={() => setEditing(true)}
            className="text-xs text-blue-600 hover:text-blue-800"
          >
            Change
          </button>
          <button
            type="button"
            onClick={onDelete}
            className="text-xs text-red-500 hover:text-red-700"
          >
            <X size={14} />
          </button>
        </div>
      )}
    </div>
  );
}

export default function DynamicApiKeyEditor({
  keys,
  onUpdate,
  onDelete,
}: DynamicApiKeyEditorProps) {
  const [newKeyName, setNewKeyName] = useState("");
  const [newKeyValue, setNewKeyValue] = useState("");
  const [showNewValue, setShowNewValue] = useState(false);

  const handleAdd = () => {
    const name = newKeyName.trim();
    const value = newKeyValue.trim();
    if (!name || !value) return;
    onUpdate(name, value);
    setNewKeyName("");
    setNewKeyValue("");
  };

  const keyNames = Object.keys(keys).sort();

  return (
    <div className="space-y-3">
      {keyNames.map((keyName) => (
        <KeyRow
          key={keyName}
          keyName={keyName}
          maskedValue={keys[keyName] ?? ""}
          onUpdate={(v) => onUpdate(keyName, v)}
          onDelete={() => onDelete(keyName)}
        />
      ))}

      <div className="border-t border-gray-200 pt-3">
        <div className="flex gap-2">
          <select
            value={newKeyName}
            onChange={(e) => setNewKeyName(e.target.value)}
            className="w-[180px] px-3 py-1.5 border border-gray-300 rounded-md text-sm font-mono focus:outline-none focus:ring-2 focus:ring-blue-500 bg-white"
          >
            <option value="" disabled>Select provider...</option>
            {LLM_API_KEY_OPTIONS
              .filter((opt) => !(opt.value in keys))
              .map((opt) => (
                <option key={opt.value} value={opt.value}>{opt.label}</option>
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
            className="inline-flex items-center gap-1 px-3 py-1.5 text-sm font-medium text-blue-600 border border-blue-300 rounded-md hover:bg-blue-50 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <Plus size={14} />
            Add
          </button>
        </div>
      </div>
    </div>
  );
}
