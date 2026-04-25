// useTaskStream subscribes to the global TaskManager SSE feed and exposes
// the live task map to React components. There is exactly one EventSource
// per page (singleton store); all hook consumers share the same data.

import { useSyncExternalStore } from "react";
import { listTasks, type Task, type TaskEvent } from "@/api/tasks";

type State = {
  tasks: Map<string, Task>;
  connected: boolean;
};

let state: State = { tasks: new Map(), connected: false };
const listeners = new Set<() => void>();

function emit() {
  for (const l of listeners) l();
}

function setState(updater: (s: State) => State) {
  state = updater(state);
  emit();
}

let started = false;
let es: EventSource | null = null;
let stopped = false;
let backoffMs = 1000;
let retryTimer: ReturnType<typeof setTimeout> | undefined;

async function seedActiveTasks() {
  try {
    const active = await listTasks({ only_active: true });
    setState((s) => {
      const next = new Map(s.tasks);
      for (const t of active) next.set(t.id, t);
      return { ...s, tasks: next };
    });
  } catch {
    // Non-fatal: SSE will still deliver new events as they happen.
  }
}

function applyEvent(ev: TaskEvent) {
  setState((s) => {
    const next = new Map(s.tasks);
    next.set(ev.task.id, ev.task);
    return { ...s, tasks: next };
  });
}

function connect() {
  if (stopped) return;

  es = new EventSource("/api/v1/tasks/events");

  es.onopen = () => {
    backoffMs = 1000;
    setState((s) => ({ ...s, connected: true }));
    seedActiveTasks();
  };

  es.onmessage = (event) => {
    try {
      const ev = JSON.parse(event.data as string) as TaskEvent;
      applyEvent(ev);
    } catch {
      // Ignore malformed messages.
    }
  };

  es.onerror = () => {
    setState((s) => ({ ...s, connected: false }));
    es?.close();
    es = null;
    if (!stopped) {
      retryTimer = setTimeout(connect, backoffMs);
      backoffMs = Math.min(backoffMs * 2, 16000);
    }
  };
}

function start() {
  if (started) return;
  started = true;
  stopped = false;
  // Seed before the first connect attempt finishes so the UI fills in fast.
  seedActiveTasks();
  connect();
}

function stop() {
  stopped = true;
  started = false;
  clearTimeout(retryTimer);
  retryTimer = undefined;
  es?.close();
  es = null;
  setState((s) => ({ ...s, connected: false }));
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  start();
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) stop();
  };
}

function getSnapshot(): State {
  return state;
}

export function useTaskStream(): { tasks: Map<string, Task>; connected: boolean } {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

// Allow non-hook callers (e.g. event handlers right after mutations) to peek
// at the current task set without triggering a subscription.
export function getTasksSnapshot(): Map<string, Task> {
  return state.tasks;
}
