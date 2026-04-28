// TaskToasts mounts once at the app root. It subscribes to the global task
// stream (SSE-backed) and renders one persistent loading toast per running
// task; on terminal transitions the same toast id flips to success/error/
// info so it animates in place and auto-dismisses.

import { createElement, useEffect, useRef } from "react";
import toast from "react-hot-toast";
import { useQueryClient } from "@tanstack/react-query";
import AppToast from "@/components/AppToast";
import { useTaskStream } from "@/hooks/useTaskStream";
import { cancelTask, type Task, type TaskState } from "@/api/tasks";
import { setInstanceBrowserActive } from "@/api/instances";
import { storageKey as browserActiveStorageKey } from "@/hooks/useChatViewMode";
import type { Instance } from "@/types/instance";
import { errorToast } from "@/utils/toast";

function durationFor(state: TaskState): number {
  switch (state) {
    case "running":
      return Infinity;
    case "succeeded":
      return 4000;
    case "failed":
      return 8000;
    case "canceled":
      return 4000;
  }
}

function statusFor(state: TaskState): "loading" | "success" | "error" | "info" {
  switch (state) {
    case "running":
      return "loading";
    case "succeeded":
      return "success";
    case "failed":
      return "error";
    case "canceled":
      return "info";
  }
}

// Cancelling a browser.spawn task should also flip the instance's Browser
// toggle off; otherwise useDesktop stays mounted and immediately re-triggers
// EnsureSession, defeating the cancel.
function maybeHideBrowserPane(t: Task, queryClient: ReturnType<typeof useQueryClient>) {
  if (t.type !== "browser.spawn" || !t.instance_id) return;
  const id = t.instance_id;
  try {
    localStorage.setItem(browserActiveStorageKey(id), "chat-only");
  } catch {
    // localStorage unavailable; the API call below still updates server state.
  }
  queryClient.setQueryData<Instance | undefined>(["instances", id], (prev) =>
    prev ? { ...prev, browser_active: false } : prev,
  );
  setInstanceBrowserActive(id, false).catch((err) => {
    errorToast("Failed to hide browser", err);
  });
}

export default function TaskToasts() {
  const { tasks } = useTaskStream();
  const queryClient = useQueryClient();
  // Track which terminal tasks we've already emitted the final toast for, so
  // the toast doesn't re-fire if the task object is replayed (e.g. SSE
  // reconnect re-seeds with terminal state for tasks still in retention).
  const finalizedRef = useRef<Set<string>>(new Set());

  // Track tasks where the user has already clicked Cancel so we don't fire
  // duplicate POST /cancel requests if the SSE stream re-emits "running"
  // before the server flips state.
  const cancelingRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    for (const t of tasks.values()) {
      if (t.state === "running") {
        const onCancel = t.cancellable && !cancelingRef.current.has(t.id)
          ? () => {
              cancelingRef.current.add(t.id);
              cancelTask(t.id).catch((err) => {
                cancelingRef.current.delete(t.id);
                errorToast("Failed to cancel", err);
              });
              maybeHideBrowserPane(t, queryClient);
            }
          : undefined;
        // Persistent loading toast. Calling toast.custom with the same id
        // updates in place rather than stacking.
        toast.custom(
          createElement(AppToast, {
            title: t.title,
            description: t.message,
            status: "loading",
            toastId: t.id,
            onCancel,
          }),
          { id: t.id, duration: Infinity },
        );
        continue;
      }
      // Terminal: emit once.
      if (finalizedRef.current.has(t.id)) continue;
      finalizedRef.current.add(t.id);
      toast.custom(
        createElement(AppToast, {
          title: t.title,
          description: t.state === "failed" ? t.message : undefined,
          status: statusFor(t.state),
          toastId: t.id,
        }),
        { id: t.id, duration: durationFor(t.state) },
      );
    }
  }, [tasks]);

  return null;
}
