import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Search, X, Loader2 } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import {
  useComposioToolkits,
  useInitiateConnection,
} from "@common/hooks/useConnections";
import { confirmConnection } from "@common/api/connections";
import { successToast, errorToast, infoToast } from "@common/utils/toast";
import type { Toolkit } from "@common/types/connection";

type Phase = "select" | "connecting" | "waiting";

export default function ConnectionModal({
  open,
  instanceId,
  onClose,
}: {
  open: boolean;
  instanceId: number;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const { data: toolkits = [], isLoading, error } = useComposioToolkits(open);
  const initiate = useInitiateConnection(instanceId);

  const [search, setSearch] = useState("");
  const [phase, setPhase] = useState<Phase>("select");
  const [selected, setSelected] = useState<Toolkit | null>(null);
  const popupRef = useRef<Window | null>(null);
  // Pending connection lives only in browser memory until it is confirmed
  // ACTIVE and persisted — nothing is written to the DB before that.
  const accountIdRef = useRef<string | null>(null);
  const toolkitRef = useRef<Toolkit | null>(null);

  // Reset state whenever the modal is (re)opened.
  useEffect(() => {
    if (open) {
      setSearch("");
      setPhase("select");
      setSelected(null);
      accountIdRef.current = null;
      toolkitRef.current = null;
    }
  }, [open]);

  const finishSuccess = () => {
    successToast("Connection added");
    qc.invalidateQueries({ queryKey: ["connections", instanceId] });
    onClose();
  };

  // While waiting for OAuth, confirm once the browser callback fires. The popup
  // closing is used only as a fallback trigger (in case the message is missed).
  useEffect(() => {
    if (phase !== "waiting") return;
    let stopped = false;
    let confirming = false;

    // Asks the control plane to confirm the connection is ACTIVE and persist it.
    // Returns true if a terminal outcome was reached.
    const confirm = async (): Promise<boolean> => {
      if (stopped || confirming) return false;
      const toolkit = toolkitRef.current;
      const accountId = accountIdRef.current;
      if (!toolkit || !accountId) return false;
      confirming = true;
      try {
        const res = await confirmConnection(instanceId, toolkit, accountId);
        if (res.status === "ACTIVE") {
          stopped = true;
          finishSuccess();
          return true;
        }
      } catch {
        /* transient — a later trigger will retry */
      } finally {
        confirming = false;
      }
      return false;
    };

    const onMessage = (e: MessageEvent) => {
      if (e.origin !== window.location.origin) return;
      if (e.data?.type === "composio-connected") confirm();
    };
    window.addEventListener("message", onMessage);

    // Fallback: if the popup closes without us seeing the message, try once
    // (with a short grace delay) before assuming the user abandoned the flow.
    const closeWatch = window.setInterval(() => {
      if (stopped || !popupRef.current?.closed) return;
      window.clearInterval(closeWatch);
      window.setTimeout(async () => {
        if (stopped) return;
        if (await confirm()) return;
        if (stopped) return;
        infoToast("Authorization window closed before finishing.");
        setPhase("select");
      }, 600);
    }, 250);

    return () => {
      stopped = true;
      window.removeEventListener("message", onMessage);
      window.clearInterval(closeWatch);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [phase, instanceId]);

  if (!open) return null;

  const handleSelect = (toolkit: Toolkit) => {
    setSelected(toolkit);
    setPhase("connecting");
    const callbackUrl = `${window.location.origin}/connections/callback`;
    initiate.mutate(
      { toolkit, callbackUrl },
      {
        onSuccess: (res) => {
          accountIdRef.current = res.connected_account_id;
          toolkitRef.current = toolkit;
          popupRef.current = window.open(
            res.redirect_url,
            "composio-oauth",
            "width=600,height=750",
          );
          setPhase("waiting");
        },
        onError: () => setPhase("select"),
      },
    );
  };

  const filtered = toolkits.filter(
    (t) =>
      t.name.toLowerCase().includes(search.toLowerCase()) ||
      t.slug.toLowerCase().includes(search.toLowerCase()),
  );

  return (
    <div
      className="fixed inset-0 bg-black/40 z-50 flex items-center justify-center"
      onKeyDown={(e) => {
        if (e.key === "Escape") onClose();
      }}
    >
      <div className="bg-white rounded-lg shadow-xl p-6 w-full mx-4 max-w-md h-[50vh] flex flex-col">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-base font-semibold text-gray-900">
            Add connection
          </h2>
          <button
            type="button"
            onClick={onClose}
            className="text-gray-400 hover:text-gray-600"
          >
            <X size={18} />
          </button>
        </div>

        {phase === "select" && (
          <>
            <div className="relative mb-3">
              <Search
                size={14}
                className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400"
              />
              <input
                autoFocus
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search services…"
                className="w-full pl-9 pr-3 py-1.5 border border-gray-300 rounded-md text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              />
            </div>
            <div className="overflow-y-auto -mx-2 px-2 divide-y divide-gray-100">
              {isLoading && (
                <p className="text-sm text-gray-400 py-6 text-center">
                  Loading services…
                </p>
              )}
              {error && (
                <p className="text-sm text-red-600 py-6 text-center">
                  Failed to load services. Is the Composio API key set in{" "}
                  <Link
                    to="/settings#composio-api-key"
                    onClick={onClose}
                    className="underline hover:text-red-700"
                  >
                    Settings
                  </Link>
                  ?
                </p>
              )}
              {!isLoading &&
                !error &&
                filtered.map((t) => (
                  <button
                    key={t.slug}
                    type="button"
                    onClick={() => handleSelect(t)}
                    className="w-full flex items-center gap-3 py-2.5 text-left hover:bg-gray-50 rounded px-2 -mx-2"
                  >
                    {t.logo ? (
                      <img
                        src={t.logo}
                        alt=""
                        className="w-6 h-6 rounded object-contain"
                      />
                    ) : (
                      <div className="w-6 h-6 rounded bg-gray-100" />
                    )}
                    <span className="text-sm font-medium text-gray-900">
                      {t.name}
                    </span>
                  </button>
                ))}
              {!isLoading && !error && filtered.length === 0 && (
                <p className="text-sm text-gray-400 py-6 text-center">
                  No services match “{search}”.
                </p>
              )}
            </div>
          </>
        )}

        {(phase === "connecting" || phase === "waiting") && (
          <div className="py-10 flex flex-col items-center text-center gap-3">
            <Loader2 size={28} className="animate-spin text-blue-600" />
            <p className="text-sm text-gray-700">
              {phase === "connecting"
                ? `Preparing ${selected?.name} connection…`
                : `Waiting for you to authorize ${selected?.name}…`}
            </p>
            {phase === "waiting" && (
              <p className="text-xs text-gray-500">
                Complete the sign-in in the popup window. This dialog closes
                automatically once the connection is active.
              </p>
            )}
            {phase === "waiting" && (
              <button
                type="button"
                onClick={() => {
                  popupRef.current?.close();
                  onClose();
                }}
                className="mt-2 px-3 py-1.5 text-xs text-gray-600 border border-gray-300 rounded-md hover:bg-gray-50"
              >
                Cancel
              </button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
