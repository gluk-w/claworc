import { useState } from "react";
import { AlertTriangle } from "lucide-react";
import { migrateBrowser } from "@/api/browser";
import { successToast, errorToast } from "@/utils/toast";

interface Props {
  instanceId: number;
}

// LegacyBrowserBanner is rendered at the top of the instance detail page for
// instances still using the combined glukw/openclaw-vnc-* image. It exposes a
// one-shot migration action that flips the agent over to the slim
// glukw/claworc-agent image and provisions an on-demand browser pod for
// CDP/VNC traffic. The migration runs as a TaskBrowserMigrate task so progress
// surfaces through the existing toast/SSE infrastructure.
export default function LegacyBrowserBanner({ instanceId }: Props) {
  const [busy, setBusy] = useState(false);

  const onClick = async () => {
    setBusy(true);
    try {
      const { task_id } = await migrateBrowser(instanceId);
      successToast(
        "Migration started",
        `Task ${task_id.slice(0, 8)}… — progress shown in the toast bar.`,
      );
    } catch (err) {
      errorToast("Migration failed", err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mb-4 flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3">
      <AlertTriangle className="h-5 w-5 flex-shrink-0 text-amber-600" />
      <div className="flex-1">
        <p className="text-sm font-medium text-amber-900">
          Legacy browser layout
        </p>
        <p className="mt-1 text-sm text-amber-800">
          This instance still runs Chromium and noVNC inside the agent
          container. Migrating moves them to a separate, on-demand browser pod
          saves resources when idle.
        </p>
      </div>
      <button
        type="button"
        onClick={onClick}
        disabled={busy}
        className="flex-shrink-0 rounded-md bg-amber-600 px-3 py-1.5 text-sm font-medium text-white shadow-sm hover:bg-amber-700 disabled:opacity-60"
      >
        {busy ? "Starting…" : "Migrate to on-demand browser"}
      </button>
    </div>
  );
}
