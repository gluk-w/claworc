import { useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { CheckCircle, Loader2, XCircle } from "lucide-react";
import { useInstances } from "@/hooks/useInstances";
import { useSettings } from "@/hooks/useSettings";
import { deploySkill } from "@/api/skills";
import { updateInstance } from "@/api/instances";
import type { DeployResult } from "@/types/skills";
import { successToast, errorToast } from "@/utils/toast";

interface Props {
  slug: string;
  displayName: string;
  description?: string;
  source: "library" | "clawhub";
  version?: string;
  /** Env var names the skill declares it needs (from SKILL.md frontmatter). */
  requiredEnvVars?: string[];
  onClose: () => void;
}

type InstanceStatus = "idle" | "deploying" | "ok" | "error";

interface InstanceState {
  status: InstanceStatus;
  error?: string;
  missingEnvVars?: string[];
}

export default function DeployModal({
  slug,
  displayName,
  description,
  source,
  version,
  requiredEnvVars = [],
  onClose,
}: Props) {
  const { data: instances } = useInstances();
  const { data: settings } = useSettings();
  const qc = useQueryClient();
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [instanceStates, setInstanceStates] = useState<
    Record<number, InstanceState>
  >({});
  // envInputs[instanceId][varName] = value entered by the admin for that
  // instance's missing env var. Saved per-instance via PUT /instances/{id}
  // right before the deploy call.
  const [envInputs, setEnvInputs] = useState<
    Record<number, Record<string, string>>
  >({});
  const [isDeploying, setIsDeploying] = useState(false);
  const [isDone, setIsDone] = useState(false);

  const globalEnvNames = useMemo(
    () => new Set(Object.keys(settings?.default_env_vars ?? {})),
    [settings],
  );

  // Per-instance list of required env vars that are neither in globals nor in
  // the instance's own overrides. Computed before deploy so the admin can fix
  // values without hitting the server.
  const missingEnvPreview = useMemo(() => {
    const map: Record<number, string[]> = {};
    if (requiredEnvVars.length === 0 || !instances) return map;
    for (const inst of instances) {
      const instKeys = new Set(Object.keys(inst.env_vars ?? {}));
      const missing = requiredEnvVars.filter(
        (name) => !globalEnvNames.has(name) && !instKeys.has(name),
      );
      if (missing.length > 0) map[inst.id] = missing;
    }
    return map;
  }, [requiredEnvVars, instances, globalEnvNames]);

  const setEnvInput = (instanceID: number, name: string, value: string) => {
    setEnvInputs((prev) => ({
      ...prev,
      [instanceID]: { ...(prev[instanceID] ?? {}), [name]: value },
    }));
  };

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

    // Persist any env var values the admin filled in for each selected
    // instance. Empty/whitespace entries are skipped; the deploy still
    // proceeds even if some instances still have missing vars.
    const envSavePromises = ids.map(async (id) => {
      const entries = envInputs[id] ?? {};
      const toSet: Record<string, string> = {};
      for (const [name, value] of Object.entries(entries)) {
        const trimmed = value.trim();
        if (trimmed !== "") toSet[name] = trimmed;
      }
      if (Object.keys(toSet).length === 0) return;
      try {
        await updateInstance(id, { env_vars_set: toSet });
      } catch (err) {
        errorToast(`Failed to save env vars for instance ${id}`, err);
      }
    });
    await Promise.allSettled(envSavePromises);
    qc.invalidateQueries({ queryKey: ["instances"] });

    try {
      const res = await deploySkill(slug, ids, source, version);
      const next: Record<number, InstanceState> = {};
      let allOk = true;
      res.results.forEach((r: DeployResult) => {
        next[r.instance_id] = {
          status: r.status,
          error: r.error,
          missingEnvVars: r.missing_env_vars,
        };
        if (r.status !== "ok") allOk = false;
      });
      setInstanceStates(next);
      setIsDone(true);
      if (allOk) {
        const anyMissing = res.results.some(
          (r) => (r.missing_env_vars?.length ?? 0) > 0,
        );
        successToast(
          `Deployed ${displayName} to ${ids.length} instance${ids.length !== 1 ? "s" : ""}`,
          anyMissing
            ? "Some instances are missing required env vars — see details."
            : undefined,
        );
        if (!anyMissing) setTimeout(onClose, 1500);
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
          {requiredEnvVars.length > 0 && (
            <p className="text-xs text-gray-500 mt-2">
              Required env vars:{" "}
              {requiredEnvVars.map((n, i) => (
                <span key={n}>
                  <span className="font-mono">{n}</span>
                  {i < requiredEnvVars.length - 1 ? ", " : ""}
                </span>
              ))}
            </p>
          )}
        </div>

        <div className="overflow-y-auto flex-1 px-6 py-4 flex flex-col gap-2">
          {!instances || instances.length === 0 ? (
            <p className="text-sm text-gray-500">No instances available.</p>
          ) : (
            instances.map((inst) => {
              const state = instanceStates[inst.id];
              const checked = selected.has(inst.id);
              const running = inst.status === "running";
              const preMissing = missingEnvPreview[inst.id];
              const postMissing = state?.missingEnvVars;

              return (
                <div
                  key={inst.id}
                  className={`flex items-start gap-3 px-3 py-2.5 rounded-lg border transition-colors ${
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
                    className="h-4 w-4 mt-1 rounded border-gray-300 text-blue-600 focus:ring-blue-500"
                  />
                  <div className="flex-1 min-w-0">
                    <span className="text-sm font-medium text-gray-800">
                      {inst.display_name}
                    </span>
                    {checked && !isDone && preMissing && preMissing.length > 0 && (
                      <div
                        className="mt-1.5 flex flex-col gap-1"
                        onClick={(e) => e.stopPropagation()}
                      >
                        {preMissing.map((name) => (
                          <div key={name} className="flex items-center gap-2">
                            <label
                              htmlFor={`envvar-${inst.id}-${name}`}
                              className="text-[11px] font-mono text-gray-600 min-w-[120px] truncate"
                              title={name}
                            >
                              {name}
                            </label>
                            <input
                              id={`envvar-${inst.id}-${name}`}
                              type="text"
                              value={envInputs[inst.id]?.[name] ?? ""}
                              onChange={(e) =>
                                setEnvInput(inst.id, name, e.target.value)
                              }
                              disabled={isDeploying}
                              placeholder="value"
                              className="flex-1 min-w-0 text-xs px-2 py-1 border border-gray-300 rounded-md focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent disabled:bg-gray-100"
                            />
                          </div>
                        ))}
                      </div>
                    )}
                    {isDone && postMissing && postMissing.length > 0 && (
                      <p className="text-[11px] text-amber-700 mt-0.5 truncate">
                        Set these to run: {postMissing.join(", ")}
                      </p>
                    )}
                  </div>
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
