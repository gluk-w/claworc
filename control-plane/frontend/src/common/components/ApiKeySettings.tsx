import { useState } from "react";
import { Eye, EyeOff } from "lucide-react";

interface ApiKeyFieldProps {
  label: string;
  maskedValue: string;
  onChange: (value: string) => void;
}

function ApiKeyField({ label, maskedValue, onChange }: ApiKeyFieldProps) {
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState("");
  const [show, setShow] = useState(false);

  return (
    <div>
      <label className="block text-sm font-medium text-gray-700 mb-1">
        {label}
      </label>
      {editing ? (
        <div className="flex gap-2">
          <div className="relative flex-1">
            <input
              type={show ? "text" : "password"}
              value={value}
              onChange={(e) => {
                setValue(e.target.value);
                onChange(e.target.value);
              }}
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
              setEditing(false);
              setValue("");
              onChange("");
            }}
            className="px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Cancel
          </button>
        </div>
      ) : (
        <div className="flex items-center gap-2">
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
        </div>
      )}
    </div>
  );
}

interface ApiKeySettingsProps {
  anthropicKey: string;
  openaiKey: string;
  braveKey: string;
  onAnthropicChange: (v: string) => void;
  onOpenaiChange: (v: string) => void;
  onBraveChange: (v: string) => void;
}

export default function ApiKeySettings({
  anthropicKey,
  openaiKey,
  braveKey,
  onAnthropicChange,
  onOpenaiChange,
  onBraveChange,
}: ApiKeySettingsProps) {
  return (
    <div className="space-y-4">
      <h3 className="text-sm font-medium text-gray-900">API Keys</h3>
      <ApiKeyField
        label="Anthropic API Key"
        maskedValue={anthropicKey}
        onChange={onAnthropicChange}
      />
      <ApiKeyField
        label="OpenAI API Key"
        maskedValue={openaiKey}
        onChange={onOpenaiChange}
      />
      <ApiKeyField
        label="Brave API Key"
        maskedValue={braveKey}
        onChange={onBraveChange}
      />
    </div>
  );
}
