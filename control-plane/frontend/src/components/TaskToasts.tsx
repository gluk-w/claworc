// TaskToasts mounts once at the app root. It subscribes to the global task
// stream (SSE-backed) and renders one persistent loading toast per running
// task; on terminal transitions the same toast id flips to success/error/
// info so it animates in place and auto-dismisses.

import { createElement, useEffect, useRef } from "react";
import toast from "react-hot-toast";
import AppToast from "@/components/AppToast";
import { useTaskStream } from "@/hooks/useTaskStream";
import type { Task, TaskState, TaskType } from "@/api/tasks";

const TYPE_LABEL: Record<TaskType, string> = {
  "instance.create": "Creating instance",
  "instance.restart": "Restarting instance",
  "instance.image_update": "Updating image",
  "instance.clone": "Cloning instance",
  "backup.create": "Backing up",
  "skill.deploy": "Deploying skill",
};

function titleFor(t: Task, state: TaskState): string {
  const verb = TYPE_LABEL[t.type] ?? t.type;
  const subject = t.resource_name || (t.instance_id ? `instance ${t.instance_id}` : "");
  const base = subject ? `${verb}: ${subject}` : verb;
  switch (state) {
    case "running":
      return base;
    case "succeeded":
      return base.replace(/^(Creating|Restarting|Updating|Cloning|Backing up|Deploying)/, (m) => {
        switch (m) {
          case "Creating":
            return "Created";
          case "Restarting":
            return "Restarted";
          case "Updating":
            return "Updated";
          case "Cloning":
            return "Cloned";
          case "Backing up":
            return "Backed up";
          case "Deploying":
            return "Deployed";
        }
        return m;
      });
    case "failed":
      return `${base} — failed`;
    case "canceled":
      return `${base} — canceled`;
  }
}

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

export default function TaskToasts() {
  const { tasks } = useTaskStream();
  // Track which terminal tasks we've already emitted the final toast for, so
  // the toast doesn't re-fire if the task object is replayed (e.g. SSE
  // reconnect re-seeds with terminal state for tasks still in retention).
  const finalizedRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    for (const t of tasks.values()) {
      if (t.state === "running") {
        // Persistent loading toast. Calling toast.custom with the same id
        // updates in place rather than stacking.
        toast.custom(
          createElement(AppToast, {
            title: titleFor(t, "running"),
            description: t.message,
            status: "loading",
            toastId: t.id,
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
          title: titleFor(t, t.state),
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
