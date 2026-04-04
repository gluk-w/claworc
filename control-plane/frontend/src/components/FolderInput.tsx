import { X } from "lucide-react";

interface Props {
  value: string[];
  onChange: (paths: string[]) => void;
}

export default function FolderInput({ value, onChange }: Props) {
  const rows = value.length === 0 || value[value.length - 1] !== "" ? [...value, ""] : value;

  const handleChange = (index: number, newVal: string) => {
    const updated = [...rows];
    updated[index] = newVal;
    // If the last row now has content, the parent re-render will add a new empty row
    // Remove trailing empty rows beyond one
    while (updated.length > 1 && updated[updated.length - 1] === "" && updated[updated.length - 2] === "") {
      updated.pop();
    }
    onChange(updated.filter((v, i) => v !== "" || i === updated.length - 1));
  };

  const handleRemove = (index: number) => {
    const updated = rows.filter((_, i) => i !== index);
    onChange(updated.filter((v) => v !== ""));
  };

  return (
    <div className="space-y-2">
      {rows.map((row, i) => (
        <div key={i} className="flex items-center gap-2">
          <input
            type="text"
            value={row}
            onChange={(e) => handleChange(i, e.target.value)}
            placeholder="e.g. HOME, Homebrew, /etc/nginx"
            className="w-full px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
          />
          {rows.filter((v) => v !== "").length > 1 && row !== "" && (
            <button
              type="button"
              onClick={() => handleRemove(i)}
              className="p-1 text-gray-400 hover:text-red-600 transition-colors"
              title="Remove"
            >
              <X size={14} />
            </button>
          )}
        </div>
      ))}
    </div>
  );
}
