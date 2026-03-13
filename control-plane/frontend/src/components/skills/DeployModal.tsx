import { useState } from "react";
import { CheckCircle, XCircle, Loader2 } from "lucide-react";
import { useInstances } from "@/hooks/useInstances";
import { deploySkill } from "@/api/skills";
import type { DeployResult } from "@/types/skills";
import { successToast, errorToast } from "@/utils/toast";

interface Props {
  slug: string;
  displayName: string;
  description?: string;
  source: "library" | "clawhub";
  version?: string;
  onClose: () => void;
}

type InstanceStatus = "idle" | "deploying" | "ok" | "error";

interface InstanceState {
  status: InstanceStatus;
  error?: string;
}

export default function DeployModal({
  slug,
  displayName,
  description,
  source,
  version,
  onClose,
}: Props) {
  const { data: instances } = useInstances();
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [instanceStates, setInstanceStates] = useState<
    Record<number, InstanceState>
  >({});
  const [isDeploying, setIsDeploying] = useState(false);
  const [isDone, setIsDone] = useState(false);

  const toggleInstance = (id: number) => {
    if (isDeploying) return;
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  const handleDeploy = async () => {
    if (selected.size === 0) return;
    setIsDeploying(true);

    const ids = Array.from(selected);
    const initial: Record<number, InstanceState> = {};
    ids.forEach((id) => (initial[id] = { status: "deploying" }));
    setInstanceStates(initial);

    try {
      const res = await deploySkill(slug, ids, source, version);
      const next: Record<number, InstanceState> = {};
      let allOk = true;
      res.results.forEach((r: DeployResult) => {
        next[r.instance_id] = {
          status: r.status,
          error: r.error,
        };
        if (r.status !== "ok") allOk = false;
      });
      setInstanceStates(next);
      setIsDone(true);
      if (allOk) {
        successToast(`Deployed ${displayName} to ${ids.length} instance${ids.length !== 1 ? "s" : ""}`);
        setTimeout(onClose, 1500);
      }
    } catch (err) {
      errorToast("Deploy failed", err);
      setIsDeploying(false);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Escape") onClose();
    if (e.key === "Enter" && !isDeploying && selected.size > 0 && !isDone) {
      handleDeploy();
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onKeyDown={handleKeyDown}
      tabIndex={-1}
    >
      <div className="bg-white rounded-xl shadow-xl w-full max-w-md mx-4 flex flex-col max-h-[80vh]">
        <div className="px-6 py-4 border-b border-gray-200">
          <h2 className="text-base font-semibold text-gray-900">
            Deploy <span className="text-blue-600">{displayName}</span> to instances
          </h2>
          {description && <p className="text-sm text-gray-500 mt-1">{description}</p>}
        </div>

        <div className="overflow-y-auto flex-1 px-6 py-4 flex flex-col gap-2">
          {!instances || instances.length === 0 ? (
            <p className="text-sm text-gray-500">No instances available.</p>
          ) : (
            instances.map((inst) => {
              const state = instanceStates[inst.id];
              const checked = selected.has(inst.id);
              const running = inst.status === "running";

              return (
                <div
                  key={inst.id}
                  className={`flex items-center gap-3 px-3 py-2.5 rounded-lg border transition-colors ${
                    running ? "cursor-pointer" : "opacity-40 cursor-not-allowed"
                  } ${
                    checked
                      ? "border-blue-300 bg-blue-50"
                      : "border-gray-200 hover:bg-gray-50"
                  } ${isDeploying ? "cursor-default" : ""}`}
                  onClick={() => toggleInstance(inst.id)}
                >
                  <input
                    type="checkbox"
                    checked={checked}
                    onChange={() => toggleInstance(inst.id)}
                    onClick={(e) => e.stopPropagation()}
                    disabled={isDeploying}
                    className="h-4 w-4 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                  />
                  <span className="flex-1 text-sm font-medium text-gray-800">
                    {inst.display_name}
                  </span>
                  {state?.status === "deploying" && (
                    <Loader2 size={14} className="animate-spin text-blue-500 shrink-0" />
                  )}
                  {state?.status === "ok" && (
                    <CheckCircle size={14} className="text-green-500 shrink-0" />
                  )}
                  {state?.status === "error" && (
                    <span className="flex items-center gap-1">
                      <XCircle size={14} className="text-red-500 shrink-0" />
                      <span className="text-xs text-red-600 truncate max-w-[120px]" title={state.error}>
                        {state.error}
                      </span>
                    </span>
                  )}
                </div>
              );
            })
          )}
        </div>

        <div className="px-6 py-4 border-t border-gray-200 flex items-center justify-end gap-3">
          <button
            onClick={onClose}
            className="px-4 py-2 text-sm font-medium text-gray-700 hover:text-gray-900 transition-colors"
          >
            {isDone ? "Close" : "Cancel"}
          </button>
          {!isDone && (
            <button
              onClick={handleDeploy}
              disabled={selected.size === 0 || isDeploying}
              className="px-4 py-2 text-sm font-medium text-white bg-blue-600 rounded-lg hover:bg-blue-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              {isDeploying ? (
                <span className="flex items-center gap-2">
                  <Loader2 size={14} className="animate-spin" />
                  Deploying…
                </span>
              ) : (
                `Deploy to ${selected.size} instance${selected.size !== 1 ? "s" : ""}`
              )}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
