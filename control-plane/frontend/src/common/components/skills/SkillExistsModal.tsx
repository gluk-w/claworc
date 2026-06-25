import { X } from "lucide-react";

interface Props {
  slug: string;
  pending?: boolean;
  onUseExisting: () => void;
  onCreateNew: () => void;
  onClose: () => void;
}

export default function SkillExistsModal({
  slug,
  pending = false,
  onUseExisting,
  onCreateNew,
  onClose,
}: Props) {
  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onClose();
    if (e.key === "Enter" && !pending) onUseExisting();
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onKeyDown={handleKeyDown}
      tabIndex={-1}
    >
      <div className="bg-white rounded-xl shadow-xl w-full max-w-md mx-4">
        <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
          <h2 className="text-base font-semibold text-gray-900">Skill already in library</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600">
            <X size={18} />
          </button>
        </div>

        <div className="px-6 py-6">
          <p className="text-sm text-gray-600">
            A skill named <strong>{slug}</strong> already exists in your library. Edit the
            existing copy, or download a new copy to edit separately?
          </p>
        </div>

        <div className="px-6 pb-5 flex items-center justify-end gap-3">
          <button
            onClick={onCreateNew}
            disabled={pending}
            className="px-4 py-2 text-sm font-medium text-gray-700 hover:text-gray-900 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            {pending ? "Downloading…" : "Create new copy"}
          </button>
          <button
            onClick={onUseExisting}
            disabled={pending}
            className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
          >
            Edit existing
          </button>
        </div>
      </div>
    </div>
  );
}
