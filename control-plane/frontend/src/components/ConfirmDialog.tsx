import { useState, useEffect, useRef, useCallback } from "react";
import { AlertTriangle } from "lucide-react";

const STORAGE_KEY = "claworc-confirm-dialog-suppress";

interface ConfirmDialogProps {
  title: string;
  message: string;
  onConfirm: () => void;
  onCancel: () => void;
  confirmLabel?: string;
  cancelLabel?: string;
  storageId?: string;
}

/** Read suppression state from localStorage for a given dialog id */
function isSuppressed(storageId: string): boolean {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return false;
    const map: Record<string, boolean> = JSON.parse(raw);
    return !!map[storageId];
  } catch {
    return false;
  }
}

/** Persist suppression preference */
function setSuppressed(storageId: string, value: boolean) {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    const map: Record<string, boolean> = raw ? JSON.parse(raw) : {};
    if (value) {
      map[storageId] = true;
    } else {
      delete map[storageId];
    }
    localStorage.setItem(STORAGE_KEY, JSON.stringify(map));
  } catch {
    // Ignore storage errors
  }
}

export { isSuppressed, STORAGE_KEY };

export default function ConfirmDialog({
  title,
  message,
  onConfirm,
  onCancel,
  confirmLabel = "Delete",
  cancelLabel = "Cancel",
  storageId,
}: ConfirmDialogProps) {
  const [dontAskAgain, setDontAskAgain] = useState(false);
  const dialogRef = useRef<HTMLDivElement>(null);
  const cancelBtnRef = useRef<HTMLButtonElement>(null);

  // Focus the cancel button on mount for safe-by-default behavior
  useEffect(() => {
    cancelBtnRef.current?.focus();
  }, []);

  // Trap focus within the dialog
  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onCancel();
        return;
      }

      if (e.key === "Tab") {
        const dialog = dialogRef.current;
        if (!dialog) return;
        const focusable = dialog.querySelectorAll<HTMLElement>(
          'button, input, [tabindex]:not([tabindex="-1"])',
        );
        if (focusable.length === 0) return;
        const first = focusable[0]!;
        const last = focusable[focusable.length - 1]!;

        if (e.shiftKey) {
          if (document.activeElement === first) {
            e.preventDefault();
            last.focus();
          }
        } else {
          if (document.activeElement === last) {
            e.preventDefault();
            first.focus();
          }
        }
      }
    },
    [onCancel],
  );

  const handleConfirm = () => {
    if (dontAskAgain && storageId) {
      setSuppressed(storageId, true);
    }
    onConfirm();
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center"
      role="dialog"
      aria-modal="true"
      aria-labelledby="confirm-dialog-title"
      aria-describedby="confirm-dialog-message"
      onKeyDown={handleKeyDown}
    >
      <div
        className="fixed inset-0 bg-black/50"
        onClick={onCancel}
        data-testid="confirm-dialog-backdrop"
      />
      <div
        ref={dialogRef}
        className="relative bg-white rounded-lg shadow-lg p-6 max-w-sm w-full mx-4"
      >
        <div className="flex items-start gap-3 mb-4">
          <div className="flex-shrink-0 flex items-center justify-center w-10 h-10 rounded-full bg-red-100">
            <AlertTriangle size={20} className="text-red-600" aria-hidden="true" />
          </div>
          <div>
            <h3
              id="confirm-dialog-title"
              className="text-lg font-semibold text-gray-900"
            >
              {title}
            </h3>
            <p
              id="confirm-dialog-message"
              className="text-sm text-gray-600 mt-1"
            >
              {message}
            </p>
          </div>
        </div>

        {storageId && (
          <label className="flex items-center gap-2 mb-4 cursor-pointer text-sm text-gray-500">
            <input
              type="checkbox"
              checked={dontAskAgain}
              onChange={(e) => setDontAskAgain(e.target.checked)}
              className="rounded-md border-gray-300 text-blue-600 focus:ring-2 focus:ring-blue-500"
              data-testid="dont-ask-again-checkbox"
            />
            Don&apos;t ask again
          </label>
        )}

        <div className="flex justify-end gap-3">
          <button
            ref={cancelBtnRef}
            onClick={onCancel}
            className="px-4 py-2 text-sm font-medium text-gray-700 bg-white border border-gray-300 rounded-md hover:bg-gray-50 transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500"
            data-testid="confirm-dialog-cancel"
          >
            {cancelLabel}
          </button>
          <button
            onClick={handleConfirm}
            className="px-4 py-2 text-sm font-medium text-white bg-red-600 rounded-md hover:bg-red-700 transition-colors focus:outline-none focus:ring-2 focus:ring-blue-500"
            data-testid="confirm-dialog-confirm"
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
