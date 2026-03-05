import { useState } from "react";
import { X, Plus, BookOpen } from "lucide-react";
import ModelCatalogPicker from "@/components/ModelCatalogPicker";

interface ModelListEditorProps {
  models: string[];
  onChange: (models: string[]) => void;
  placeholder?: string;
  showCatalog?: boolean;
}

export default function ModelListEditor({
  models,
  onChange,
  placeholder = "provider/model-name",
  showCatalog = false,
}: ModelListEditorProps) {
  const [input, setInput] = useState("");
  const [catalogOpen, setCatalogOpen] = useState(false);

  const handleAdd = () => {
    const trimmed = input.trim();
    if (!trimmed) return;
    if (!trimmed.includes("/")) return;
    if (models.includes(trimmed)) return;
    onChange([...models, trimmed]);
    setInput("");
  };

  const handleRemove = (index: number) => {
    onChange(models.filter((_, i) => i !== index));
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") {
      e.preventDefault();
      handleAdd();
    }
  };

  const handleCatalogSelect = (id: string) => {
    if (models.includes(id)) return;
    onChange([...models, id]);
  };

  return (
    <div className="space-y-2">
      {models.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {models.map((model, i) => (
            <span
              key={model}
              className="inline-flex items-center gap-1 px-2.5 py-1 bg-blue-50 text-blue-700 text-sm rounded-md border border-blue-200"
            >
              {model}
              <button
                type="button"
                onClick={() => handleRemove(i)}
                className="text-blue-400 hover:text-blue-600"
              >
                <X size={14} />
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="flex gap-2">
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={placeholder}
          className="flex-1 px-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
        <button
          type="button"
          onClick={handleAdd}
          disabled={!input.trim() || !input.includes("/")}
          className="inline-flex items-center gap-1 px-3 py-1.5 text-sm font-medium text-blue-600 border border-blue-300 rounded-md hover:bg-blue-50 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <Plus size={14} />
          Add
        </button>
        {showCatalog && (
          <button
            type="button"
            onClick={() => setCatalogOpen(true)}
            className="inline-flex items-center gap-1 px-3 py-1.5 text-sm font-medium text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
          >
            <BookOpen size={14} />
            Browse
          </button>
        )}
      </div>
      {catalogOpen && (
        <ModelCatalogPicker
          selectedModels={models}
          onSelect={handleCatalogSelect}
          onClose={() => setCatalogOpen(false)}
        />
      )}
    </div>
  );
}
