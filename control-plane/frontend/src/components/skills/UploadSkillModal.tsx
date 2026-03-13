import { useRef, useState } from "react";
import { AlertTriangle, Upload, X } from "lucide-react";
import { useUploadSkill } from "@/hooks/useSkills";

interface Props {
  onClose: () => void;
  onUploaded: () => void;
}

export default function UploadSkillModal({ onClose, onUploaded }: Props) {
  const [dragging, setDragging] = useState(false);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [conflictSlug, setConflictSlug] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const { mutate: upload, isPending } = useUploadSkill();

  const handleFile = (file: File) => {
    if (!file.name.endsWith(".zip")) return;
    setSelectedFile(file);
    setConflictSlug(null);
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragging(false);
    const file = e.dataTransfer.files[0];
    if (file) handleFile(file);
  };

  const doUpload = (overwrite: boolean) => {
    if (!selectedFile || isPending) return;
    upload(
      { file: selectedFile, overwrite },
      {
        onSuccess: () => {
          onUploaded();
          onClose();
        },
        onError: (error) => {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          if ((error as any)?.response?.status === 409) {
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            const detail: string = (error as any)?.response?.data?.detail ?? "";
            const match = detail.match(/Skill '(.+)' already exists/);
            setConflictSlug(match ? match[1] : selectedFile.name.replace(".zip", ""));
          }
        },
      },
    );
  };

  const handleSubmit = () => doUpload(false);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      if (conflictSlug) {
        setConflictSlug(null);
      } else {
        onClose();
      }
    }
    if (e.key === "Enter" && selectedFile && !isPending && !conflictSlug) handleSubmit();
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onKeyDown={handleKeyDown}
      tabIndex={-1}
    >
      <div className="bg-white rounded-xl shadow-xl w-full max-w-md mx-4">
        <div className="flex items-center justify-between px-6 py-4 border-b border-gray-200">
          <h2 className="text-base font-semibold text-gray-900">Upload Skill</h2>
          <button onClick={onClose} className="text-gray-400 hover:text-gray-600">
            <X size={18} />
          </button>
        </div>

        <div className="px-6 py-6 flex flex-col gap-4">
          {conflictSlug ? (
            <div className="rounded-lg border border-amber-200 bg-amber-50 p-4 flex gap-3">
              <AlertTriangle size={18} className="text-amber-500 mt-0.5 shrink-0" />
              <p className="text-sm text-amber-800">
                A skill named <strong>{conflictSlug}</strong> already exists. Overwrite it?
              </p>
            </div>
          ) : (
            <div
              onDragOver={(e) => { e.preventDefault(); setDragging(true); }}
              onDragLeave={() => setDragging(false)}
              onDrop={handleDrop}
              onClick={() => inputRef.current?.click()}
              className={`border-2 border-dashed rounded-lg p-8 text-center cursor-pointer transition-colors ${
                dragging
                  ? "border-blue-400 bg-blue-50"
                  : "border-gray-300 hover:border-gray-400"
              }`}
            >
              <Upload size={24} className="mx-auto mb-3 text-gray-400" />
              {selectedFile ? (
                <p className="text-sm font-medium text-gray-800">{selectedFile.name}</p>
              ) : (
                <>
                  <p className="text-sm font-medium text-gray-700">
                    Drop a .zip file here or click to browse
                  </p>
                  <p className="text-xs text-gray-400 mt-1">
                    Zip must contain a SKILL.md with valid frontmatter
                  </p>
                </>
              )}
              <input
                ref={inputRef}
                type="file"
                accept=".zip"
                className="hidden"
                onChange={(e) => {
                  const file = e.target.files?.[0];
                  if (file) handleFile(file);
                }}
              />
            </div>
          )}
        </div>

        <div className="px-6 pb-5 flex items-center justify-end gap-3">
          {conflictSlug ? (
            <>
              <button
                onClick={() => setConflictSlug(null)}
                className="px-4 py-2 text-sm font-medium text-gray-700 hover:text-gray-900 transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={() => doUpload(true)}
                disabled={isPending}
                className="px-4 py-2 text-sm font-medium text-white bg-amber-500 rounded-lg hover:bg-amber-600 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {isPending ? "Overwriting…" : "Overwrite"}
              </button>
            </>
          ) : (
            <>
              <button
                onClick={onClose}
                className="px-4 py-2 text-sm font-medium text-gray-700 hover:text-gray-900 transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={handleSubmit}
                disabled={!selectedFile || isPending}
                className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
              >
                {isPending ? "Uploading…" : "Upload"}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
