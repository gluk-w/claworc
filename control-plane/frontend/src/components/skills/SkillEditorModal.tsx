import { useEffect, useMemo, useState } from "react";
import MonacoConfigEditor from "@/components/MonacoConfigEditor";
import { useSkillFile, useSkillFiles, useSaveSkillFile } from "@/hooks/useSkills";
import type { Skill } from "@/types/skills";

interface SkillEditorModalProps {
  skill: Skill;
  onClose: () => void;
}

const LANGUAGE_BY_EXT: Record<string, string> = {
  md: "markdown",
  markdown: "markdown",
  py: "python",
  js: "javascript",
  jsx: "javascript",
  ts: "typescript",
  tsx: "typescript",
  json: "json",
  sh: "shell",
  bash: "shell",
  yaml: "yaml",
  yml: "yaml",
  html: "html",
  css: "css",
  toml: "ini",
  ini: "ini",
  txt: "plaintext",
};

function languageFor(path: string): string {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  return LANGUAGE_BY_EXT[ext] ?? "plaintext";
}

export default function SkillEditorModal({ skill, onClose }: SkillEditorModalProps) {
  const { data: files, isLoading: filesLoading } = useSkillFiles(skill.slug);
  const [selected, setSelected] = useState<string | null>(null);
  const [edits, setEdits] = useState<Record<string, string>>({});
  const save = useSaveSkillFile(skill.slug);

  // Auto-select the first non-binary file (or first file) when the list arrives.
  useEffect(() => {
    if (selected || !files || files.length === 0) return;
    const firstText = files.find((f) => !f.binary);
    setSelected((firstText ?? files[0]).path);
  }, [files, selected]);

  const selectedEntry = useMemo(
    () => files?.find((f) => f.path === selected) ?? null,
    [files, selected],
  );

  const { data: fileContent, isLoading: contentLoading } = useSkillFile(
    selectedEntry && !selectedEntry.binary ? skill.slug : null,
    selectedEntry && !selectedEntry.binary ? selectedEntry.path : null,
  );

  const currentValue =
    selected && selected in edits
      ? edits[selected]
      : (fileContent?.content ?? "");
  const isDirty = !!(selected && selected in edits && edits[selected] !== fileContent?.content);
  const dirtyPaths = Object.keys(edits).filter((p) => edits[p] !== undefined);
  const hasAnyDirty = dirtyPaths.length > 0;

  const closeWithConfirm = () => {
    if (hasAnyDirty && !confirm("You have unsaved changes. Discard them?")) return;
    onClose();
  };

  const handleSaveCurrent = () => {
    if (!selected || !isDirty || !selectedEntry || selectedEntry.binary) return;
    const content = edits[selected];
    save.mutate(
      { path: selected, content },
      {
        onSuccess: () => {
          setEdits((prev) => {
            const next = { ...prev };
            delete next[selected];
            return next;
          });
        },
      },
    );
  };

  // Esc closes; Cmd/Ctrl+S saves.
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        closeWithConfirm();
      } else if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        handleSaveCurrent();
      }
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected, edits, fileContent, selectedEntry, hasAnyDirty]);

  const sortedFiles = useMemo(
    () => (files ? [...files].sort((a, b) => a.path.localeCompare(b.path)) : []),
    [files],
  );

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="bg-white rounded-xl shadow-xl w-full max-w-6xl mx-4 flex flex-col max-h-[85vh]">
        {/* Header */}
        <div className="px-6 py-4 border-b border-gray-200">
          <h2 className="text-base font-semibold text-gray-900">Edit Skill — {skill.name}</h2>
          <p className="text-sm text-gray-500 mt-1 font-mono">{skill.slug}</p>
        </div>

        {/* Body */}
        <div className="flex-1 overflow-hidden flex min-h-[60vh]">
          {/* File list */}
          <div className="w-72 border-r border-gray-200 overflow-y-auto py-2">
            {filesLoading ? (
              <p className="text-xs text-gray-400 px-4 py-2">Loading...</p>
            ) : sortedFiles.length === 0 ? (
              <p className="text-xs text-gray-400 italic px-4 py-2">No files.</p>
            ) : (
              sortedFiles.map((f) => {
                const isSelected = f.path === selected;
                const isFileDirty = f.path in edits && edits[f.path] !== undefined;
                return (
                  <button
                    key={f.path}
                    onClick={() => setSelected(f.path)}
                    className={`w-full text-left px-4 py-1.5 text-xs font-mono truncate ${
                      isSelected
                        ? "bg-blue-50 text-blue-700"
                        : "text-gray-700 hover:bg-gray-50"
                    }`}
                    title={f.path}
                  >
                    {isFileDirty && <span className="text-amber-500 mr-1">*</span>}
                    {f.path}
                  </button>
                );
              })
            )}
          </div>

          {/* Editor pane */}
          <div className="flex-1 flex flex-col">
            {selected ? (
              <>
                <div className="px-4 py-2 border-b border-gray-200 text-xs font-mono text-gray-600 flex items-center justify-between">
                  <span>
                    {isDirty && <span className="text-amber-500 mr-1">*</span>}
                    {selected}
                  </span>
                  {selectedEntry?.binary && (
                    <span className="text-gray-400">binary</span>
                  )}
                </div>
                {selectedEntry?.binary ? (
                  <div className="flex-1 flex items-center justify-center text-sm text-gray-400 italic">
                    Binary file — not editable
                  </div>
                ) : contentLoading ? (
                  <div className="flex-1 flex items-center justify-center text-sm text-gray-400">
                    Loading...
                  </div>
                ) : (
                  <div className="flex-1">
                    <MonacoConfigEditor
                      key={selected}
                      path={selected}
                      language={languageFor(selected)}
                      value={currentValue}
                      onChange={(val) =>
                        setEdits((prev) => ({ ...prev, [selected]: val ?? "" }))
                      }
                      height="100%"
                    />
                  </div>
                )}
              </>
            ) : (
              <div className="flex-1 flex items-center justify-center text-sm text-gray-400 italic">
                Select a file to edit.
              </div>
            )}
          </div>
        </div>

        {/* Footer */}
        <div className="px-6 py-4 border-t border-gray-200 flex items-center justify-end gap-3">
          <button
            type="button"
            onClick={closeWithConfirm}
            className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50"
          >
            Close
          </button>
          <button
            type="button"
            onClick={handleSaveCurrent}
            disabled={!isDirty || save.isPending || !!selectedEntry?.binary}
            className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-md hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {save.isPending ? "Saving..." : "Save"}
          </button>
        </div>
      </div>
    </div>
  );
}
