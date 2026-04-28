import { useCallback, useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { setInstanceBrowserActive } from "@/api/instances";
import type { Instance } from "@/types/instance";

export type ChatViewMode = "chat-browser" | "chat-only";

const STORAGE_KEY_PREFIX = "claworc.browserActive.";

export function storageKey(instanceId: number) {
  return `${STORAGE_KEY_PREFIX}${instanceId}`;
}

function toMode(active: boolean): ChatViewMode {
  return active ? "chat-browser" : "chat-only";
}

// Per-instance "browser pane visible" toggle. Source of truth is
// `Instance.browser_active` on the server; localStorage mirrors it so other
// open tabs in the same browser flip instantly via the `storage` event
// without waiting for a refetch.
export function useChatViewMode(
  instanceId: number,
  serverActive: boolean | undefined,
): [ChatViewMode, (m: ChatViewMode) => void] {
  const queryClient = useQueryClient();

  // Hydrate from server (default to true until the instance loads).
  const initial: ChatViewMode = toMode(serverActive ?? true);
  const [mode, setMode] = useState<ChatViewMode>(initial);

  // Re-sync whenever the server value changes (e.g. on initial fetch or
  // refetch after another tab updated it and our query revalidated).
  useEffect(() => {
    if (serverActive === undefined) return;
    setMode(toMode(serverActive));
  }, [serverActive]);

  // Cross-tab sync via storage event, scoped to this instance's key.
  useEffect(() => {
    const key = storageKey(instanceId);
    const onStorage = (e: StorageEvent) => {
      if (e.key !== key || e.storageArea !== localStorage) return;
      const next: ChatViewMode = e.newValue === "chat-only" ? "chat-only" : "chat-browser";
      setMode(next);
      queryClient.setQueryData<Instance | undefined>(["instances", instanceId], (prev) =>
        prev ? { ...prev, browser_active: next === "chat-browser" } : prev,
      );
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, [instanceId, queryClient]);

  const mutation = useMutation({
    mutationFn: (next: ChatViewMode) =>
      setInstanceBrowserActive(instanceId, next === "chat-browser"),
    onSuccess: (data) => {
      queryClient.setQueryData<Instance | undefined>(["instances", instanceId], (prev) =>
        prev ? { ...prev, browser_active: data.browser_active } : prev,
      );
    },
  });

  const update = useCallback(
    (next: ChatViewMode) => {
      setMode(next);
      try {
        localStorage.setItem(storageKey(instanceId), next);
      } catch {
        // localStorage unavailable (private mode, quota); UI keeps working.
      }
      // Optimistic cache patch so re-renders during the in-flight PATCH
      // already reflect the new value.
      queryClient.setQueryData<Instance | undefined>(["instances", instanceId], (prev) =>
        prev ? { ...prev, browser_active: next === "chat-browser" } : prev,
      );
      mutation.mutate(next);
    },
    [instanceId, mutation, queryClient],
  );

  return [mode, update];
}
