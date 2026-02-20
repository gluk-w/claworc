import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { useInstanceLogs } from "./useInstanceLogs";

// Mock EventSource
class MockEventSource {
  static instances: MockEventSource[] = [];
  url: string;
  onopen: ((ev: Event) => void) | null = null;
  onmessage: ((ev: MessageEvent) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  readyState = 0;
  closed = false;

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  close() {
    this.closed = true;
    this.readyState = 2;
  }

  // Test helper: simulate connection open
  simulateOpen() {
    this.readyState = 1;
    this.onopen?.(new Event("open"));
  }

  // Test helper: simulate receiving a message
  simulateMessage(data: string) {
    this.onmessage?.(new MessageEvent("message", { data }));
  }

  // Test helper: simulate an error
  simulateError() {
    this.onerror?.(new Event("error"));
  }
}

describe("useInstanceLogs", () => {
  beforeEach(() => {
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // --- URL selection ---

  it("connects to creation-logs endpoint when logType is 'creation'", () => {
    renderHook(() => useInstanceLogs(42, true, "creation"));
    expect(MockEventSource.instances).toHaveLength(1);
    expect(MockEventSource.instances[0].url).toBe(
      "/api/v1/instances/42/creation-logs",
    );
  });

  it("connects to runtime logs endpoint when logType is 'runtime'", () => {
    renderHook(() => useInstanceLogs(42, true, "runtime"));
    expect(MockEventSource.instances).toHaveLength(1);
    expect(MockEventSource.instances[0].url).toBe(
      "/api/v1/instances/42/logs?tail=100&follow=true",
    );
  });

  it("defaults to runtime logType when not specified", () => {
    renderHook(() => useInstanceLogs(42, true));
    expect(MockEventSource.instances).toHaveLength(1);
    expect(MockEventSource.instances[0].url).toBe(
      "/api/v1/instances/42/logs?tail=100&follow=true",
    );
  });

  // --- Connection lifecycle ---

  it("does not create EventSource when disabled", () => {
    renderHook(() => useInstanceLogs(42, false, "creation"));
    expect(MockEventSource.instances).toHaveLength(0);
  });

  it("sets isConnected to true on EventSource open", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    expect(result.current.isConnected).toBe(false);

    act(() => {
      MockEventSource.instances[0].simulateOpen();
    });
    expect(result.current.isConnected).toBe(true);
  });

  it("sets isConnected to false on EventSource error", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));

    act(() => {
      MockEventSource.instances[0].simulateOpen();
    });
    expect(result.current.isConnected).toBe(true);

    act(() => {
      MockEventSource.instances[0].simulateError();
    });
    expect(result.current.isConnected).toBe(false);
  });

  it("closes EventSource when disabled after being enabled", () => {
    const { rerender } = renderHook(
      ({ enabled }) => useInstanceLogs(42, enabled, "creation"),
      { initialProps: { enabled: true } },
    );
    const es = MockEventSource.instances[0];
    expect(es.closed).toBe(false);

    rerender({ enabled: false });
    expect(es.closed).toBe(true);
  });

  it("closes EventSource on unmount", () => {
    const { unmount } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];
    expect(es.closed).toBe(false);

    unmount();
    expect(es.closed).toBe(true);
  });

  // --- Message handling ---

  it("appends creation log messages to logs array", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("[2026-02-20 14:23:01] [STATUS] Waiting for pod creation...");
      es.simulateMessage("[2026-02-20 14:23:03] [EVENT] kubelet: Pulling image");
      es.simulateMessage("[2026-02-20 14:23:46] [STATUS] Pod is running and ready");
    });

    expect(result.current.logs).toHaveLength(3);
    expect(result.current.logs[0]).toContain("Waiting for pod creation...");
    expect(result.current.logs[1]).toContain("Pulling image");
    expect(result.current.logs[2]).toContain("Pod is running and ready");
  });

  it("does not append messages when paused", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("First event");
    });
    expect(result.current.logs).toHaveLength(1);

    act(() => {
      result.current.togglePause();
    });
    expect(result.current.isPaused).toBe(true);

    act(() => {
      es.simulateMessage("Missed event");
    });
    expect(result.current.logs).toHaveLength(1);
  });

  // --- Log type switching ---

  it("clears logs when switching from creation to runtime", () => {
    const { result, rerender } = renderHook(
      ({ logType }) => useInstanceLogs(42, true, logType),
      { initialProps: { logType: "creation" as const } },
    );
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("Creation event 1");
      es.simulateMessage("Creation event 2");
    });
    expect(result.current.logs).toHaveLength(2);

    rerender({ logType: "runtime" as const });

    // Logs should be cleared on type switch
    expect(result.current.logs).toHaveLength(0);
  });

  it("clears logs when switching from runtime to creation", () => {
    const { result, rerender } = renderHook(
      ({ logType }) => useInstanceLogs(42, true, logType),
      { initialProps: { logType: "runtime" as const } },
    );
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("Runtime log 1");
    });
    expect(result.current.logs).toHaveLength(1);

    rerender({ logType: "creation" as const });
    expect(result.current.logs).toHaveLength(0);
  });

  it("creates new EventSource with correct URL when log type changes", () => {
    const { rerender } = renderHook(
      ({ logType }) => useInstanceLogs(42, true, logType),
      { initialProps: { logType: "creation" as const } },
    );
    expect(MockEventSource.instances).toHaveLength(1);
    expect(MockEventSource.instances[0].url).toContain("creation-logs");

    // Old EventSource should be closed when switching
    const oldEs = MockEventSource.instances[0];

    rerender({ logType: "runtime" as const });

    expect(oldEs.closed).toBe(true);
    // New EventSource should be created
    const newEs = MockEventSource.instances[MockEventSource.instances.length - 1];
    expect(newEs.url).toContain("/logs?tail=100&follow=true");
  });

  // --- Clear and pause ---

  it("clearLogs empties the logs array", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("Event 1");
      es.simulateMessage("Event 2");
    });
    expect(result.current.logs).toHaveLength(2);

    act(() => {
      result.current.clearLogs();
    });
    expect(result.current.logs).toHaveLength(0);
  });

  it("togglePause toggles the isPaused state", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    expect(result.current.isPaused).toBe(false);

    act(() => {
      result.current.togglePause();
    });
    expect(result.current.isPaused).toBe(true);

    act(() => {
      result.current.togglePause();
    });
    expect(result.current.isPaused).toBe(false);
  });

  // --- Instance ID changes ---

  it("reconnects with correct URL when instance ID changes", () => {
    const { rerender } = renderHook(
      ({ id }) => useInstanceLogs(id, true, "creation"),
      { initialProps: { id: 1 } },
    );
    expect(MockEventSource.instances[0].url).toBe(
      "/api/v1/instances/1/creation-logs",
    );

    rerender({ id: 2 });
    const latest = MockEventSource.instances[MockEventSource.instances.length - 1];
    expect(latest.url).toBe("/api/v1/instances/2/creation-logs");
  });

  // --- Concurrent instances ---

  it("supports independent log streams for different instance IDs", () => {
    const hook1 = renderHook(() => useInstanceLogs(1, true, "creation"));
    const hook2 = renderHook(() => useInstanceLogs(2, true, "creation"));

    const es1 = MockEventSource.instances[0];
    const es2 = MockEventSource.instances[1];

    expect(es1.url).toBe("/api/v1/instances/1/creation-logs");
    expect(es2.url).toBe("/api/v1/instances/2/creation-logs");

    act(() => {
      es1.simulateOpen();
      es1.simulateMessage("Instance 1: Pod scheduling");
    });

    act(() => {
      es2.simulateOpen();
      es2.simulateMessage("Instance 2: Image pulling");
    });

    expect(hook1.result.current.logs).toHaveLength(1);
    expect(hook1.result.current.logs[0]).toBe("Instance 1: Pod scheduling");

    expect(hook2.result.current.logs).toHaveLength(1);
    expect(hook2.result.current.logs[0]).toBe("Instance 2: Image pulling");
  });
});
