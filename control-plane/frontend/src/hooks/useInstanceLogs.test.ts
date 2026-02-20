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

  // --- Docker-specific message patterns ---

  it("appends Docker creation events (container status, health, logs)", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("Waiting for container creation...");
      es.simulateMessage("Container status: created");
      es.simulateMessage("Container status: running");
      es.simulateMessage("Health: starting");
      es.simulateMessage("Health: healthy");
      es.simulateMessage("Starting services...");
      es.simulateMessage("Container is running and healthy");
    });

    expect(result.current.logs).toHaveLength(7);
    expect(result.current.logs[0]).toBe("Waiting for container creation...");
    expect(result.current.logs[1]).toBe("Container status: created");
    expect(result.current.logs[4]).toBe("Health: healthy");
    expect(result.current.logs[6]).toBe("Container is running and healthy");
  });

  it("handles Docker error events (inspect errors, timeouts)", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("Waiting for container creation...");
      es.simulateMessage("Error inspecting container: connection refused");
      es.simulateMessage("Container status: running");
      es.simulateMessage("Timed out waiting for container to become ready");
    });

    expect(result.current.logs).toHaveLength(4);
    expect(result.current.logs[1]).toBe(
      "Error inspecting container: connection refused",
    );
    expect(result.current.logs[3]).toBe(
      "Timed out waiting for container to become ready",
    );
  });

  it("clears Docker creation events when switching to runtime", () => {
    const { result, rerender } = renderHook(
      ({ logType }) => useInstanceLogs(42, true, logType),
      { initialProps: { logType: "creation" as const } },
    );
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("Container status: created");
      es.simulateMessage("Container status: running");
      es.simulateMessage("Health: healthy");
      es.simulateMessage("Container is running and healthy");
    });
    expect(result.current.logs).toHaveLength(4);

    rerender({ logType: "runtime" as const });
    expect(result.current.logs).toHaveLength(0);
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

  it("supports 3 concurrent instances with interleaved real-time events", () => {
    const hook1 = renderHook(() => useInstanceLogs(1, true, "creation"));
    const hook2 = renderHook(() => useInstanceLogs(2, true, "creation"));
    const hook3 = renderHook(() => useInstanceLogs(3, true, "creation"));

    const es1 = MockEventSource.instances[0];
    const es2 = MockEventSource.instances[1];
    const es3 = MockEventSource.instances[2];

    expect(es1.url).toBe("/api/v1/instances/1/creation-logs");
    expect(es2.url).toBe("/api/v1/instances/2/creation-logs");
    expect(es3.url).toBe("/api/v1/instances/3/creation-logs");

    // Open all connections
    act(() => {
      es1.simulateOpen();
      es2.simulateOpen();
      es3.simulateOpen();
    });

    // Interleave events across all 3 streams
    act(() => {
      es1.simulateMessage("Instance 1: Scheduling");
      es2.simulateMessage("Instance 2: Scheduling");
      es3.simulateMessage("Instance 3: Scheduling");
    });

    act(() => {
      es2.simulateMessage("Instance 2: Pulling image");
      es1.simulateMessage("Instance 1: Pulling image");
      es3.simulateMessage("Instance 3: Pulling image");
    });

    act(() => {
      es3.simulateMessage("Instance 3: Container creating");
      es1.simulateMessage("Instance 1: Ready");
      es2.simulateMessage("Instance 2: Ready");
      es3.simulateMessage("Instance 3: Ready");
    });

    // Verify each hook only has its own events
    expect(hook1.result.current.logs).toHaveLength(3);
    expect(hook1.result.current.logs).toEqual([
      "Instance 1: Scheduling",
      "Instance 1: Pulling image",
      "Instance 1: Ready",
    ]);

    expect(hook2.result.current.logs).toHaveLength(3);
    expect(hook2.result.current.logs).toEqual([
      "Instance 2: Scheduling",
      "Instance 2: Pulling image",
      "Instance 2: Ready",
    ]);

    expect(hook3.result.current.logs).toHaveLength(4);
    expect(hook3.result.current.logs).toEqual([
      "Instance 3: Scheduling",
      "Instance 3: Pulling image",
      "Instance 3: Container creating",
      "Instance 3: Ready",
    ]);
  });

  it("one instance disconnecting does not affect other concurrent streams", () => {
    const hook1 = renderHook(() => useInstanceLogs(1, true, "creation"));
    const hook2 = renderHook(() => useInstanceLogs(2, true, "creation"));
    const hook3 = renderHook(() => useInstanceLogs(3, true, "creation"));

    const es1 = MockEventSource.instances[0];
    const es2 = MockEventSource.instances[1];
    const es3 = MockEventSource.instances[2];

    act(() => {
      es1.simulateOpen();
      es2.simulateOpen();
      es3.simulateOpen();
    });

    // All get first event
    act(() => {
      es1.simulateMessage("Instance 1: Event A");
      es2.simulateMessage("Instance 2: Event A");
      es3.simulateMessage("Instance 3: Event A");
    });

    // Instance 1 errors out (disconnects)
    act(() => {
      es1.simulateError();
    });
    expect(hook1.result.current.isConnected).toBe(false);

    // Instances 2 and 3 continue receiving events unaffected
    act(() => {
      es2.simulateMessage("Instance 2: Event B");
      es3.simulateMessage("Instance 3: Event B");
    });

    act(() => {
      es2.simulateMessage("Instance 2: Event C");
      es3.simulateMessage("Instance 3: Event C");
    });

    // Instance 2 and 3 still connected with all their events
    expect(hook2.result.current.isConnected).toBe(true);
    expect(hook2.result.current.logs).toHaveLength(3);
    expect(hook2.result.current.logs).toEqual([
      "Instance 2: Event A",
      "Instance 2: Event B",
      "Instance 2: Event C",
    ]);

    expect(hook3.result.current.isConnected).toBe(true);
    expect(hook3.result.current.logs).toHaveLength(3);
    expect(hook3.result.current.logs).toEqual([
      "Instance 3: Event A",
      "Instance 3: Event B",
      "Instance 3: Event C",
    ]);

    // Instance 1 still only has 1 event from before disconnect
    expect(hook1.result.current.logs).toHaveLength(1);
  });

  it("concurrent instances with different log types maintain separate streams", () => {
    const hook1 = renderHook(() => useInstanceLogs(1, true, "creation"));
    const hook2 = renderHook(() => useInstanceLogs(2, true, "runtime"));
    const hook3 = renderHook(() => useInstanceLogs(3, true, "creation"));

    const es1 = MockEventSource.instances[0];
    const es2 = MockEventSource.instances[1];
    const es3 = MockEventSource.instances[2];

    // Verify different URLs
    expect(es1.url).toBe("/api/v1/instances/1/creation-logs");
    expect(es2.url).toBe("/api/v1/instances/2/logs?tail=100&follow=true");
    expect(es3.url).toBe("/api/v1/instances/3/creation-logs");

    act(() => {
      es1.simulateOpen();
      es2.simulateOpen();
      es3.simulateOpen();
    });

    // Interleave creation and runtime events
    act(() => {
      es1.simulateMessage("Pod scheduling");
      es2.simulateMessage("Runtime log line 1");
      es3.simulateMessage("Container status: created");
    });

    act(() => {
      es1.simulateMessage("Pod is running");
      es2.simulateMessage("Runtime log line 2");
      es3.simulateMessage("Health: healthy");
    });

    expect(hook1.result.current.logs).toEqual([
      "Pod scheduling",
      "Pod is running",
    ]);
    expect(hook2.result.current.logs).toEqual([
      "Runtime log line 1",
      "Runtime log line 2",
    ]);
    expect(hook3.result.current.logs).toEqual([
      "Container status: created",
      "Health: healthy",
    ]);
  });

  it("pausing one concurrent instance does not affect others", () => {
    const hook1 = renderHook(() => useInstanceLogs(1, true, "creation"));
    const hook2 = renderHook(() => useInstanceLogs(2, true, "creation"));

    const es1 = MockEventSource.instances[0];
    const es2 = MockEventSource.instances[1];

    act(() => {
      es1.simulateOpen();
      es2.simulateOpen();
    });

    act(() => {
      es1.simulateMessage("Instance 1: Event A");
      es2.simulateMessage("Instance 2: Event A");
    });

    // Pause instance 1
    act(() => {
      hook1.result.current.togglePause();
    });
    expect(hook1.result.current.isPaused).toBe(true);
    expect(hook2.result.current.isPaused).toBe(false);

    // Send more events - instance 1 should miss them, instance 2 should get them
    act(() => {
      es1.simulateMessage("Instance 1: Event B (missed)");
      es2.simulateMessage("Instance 2: Event B");
    });

    expect(hook1.result.current.logs).toHaveLength(1);
    expect(hook2.result.current.logs).toHaveLength(2);
    expect(hook2.result.current.logs[1]).toBe("Instance 2: Event B");
  });

  // --- Error handling and edge cases ---

  it("preserves existing logs when EventSource errors after accumulating messages", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("Event 1");
      es.simulateMessage("Event 2");
      es.simulateMessage("Event 3");
    });
    expect(result.current.logs).toHaveLength(3);

    // Connection error should not clear accumulated logs
    act(() => {
      es.simulateError();
    });
    expect(result.current.isConnected).toBe(false);
    expect(result.current.logs).toHaveLength(3);
    expect(result.current.logs).toEqual(["Event 1", "Event 2", "Event 3"]);
  });

  it("handles error messages from backend (e.g., not in creation phase)", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage(
        "Instance is not in creation phase. Switch to Runtime logs or restart the instance to see creation logs.",
      );
    });

    expect(result.current.logs).toHaveLength(1);
    expect(result.current.logs[0]).toContain("not in creation phase");
  });

  it("handles creation failure error events from the orchestrator", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("Waiting for pod creation...");
      es.simulateMessage("Pod scheduled to node worker-1");
      es.simulateMessage(
        "Error: Insufficient memory on node worker-1",
      );
      es.simulateMessage("Pod evicted: OOMKilled");
      es.simulateMessage("Instance creation failed: pod terminated with error");
    });

    expect(result.current.logs).toHaveLength(5);
    expect(result.current.logs[2]).toContain("Insufficient memory");
    expect(result.current.logs[3]).toContain("OOMKilled");
    expect(result.current.logs[4]).toContain("creation failed");
  });

  it("accumulates many log messages during long-running creation", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
    });

    // Simulate 50 log messages (long-running creation with many poll updates)
    act(() => {
      es.simulateMessage("Waiting for pod creation...");
      for (let i = 1; i <= 48; i++) {
        es.simulateMessage(`Still pulling image... (${i}/48 attempts)`);
      }
      es.simulateMessage("Pod is running and ready");
    });

    expect(result.current.logs).toHaveLength(50);
    expect(result.current.logs[0]).toBe("Waiting for pod creation...");
    expect(result.current.logs[49]).toBe("Pod is running and ready");
  });

  it("handles special characters in log messages", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("Message with <html> entities & symbols");
      es.simulateMessage('Message with "quotes" and \'single\'');
      es.simulateMessage("Message with unicode: 日本語テスト");
    });

    expect(result.current.logs).toHaveLength(3);
    expect(result.current.logs[0]).toBe(
      "Message with <html> entities & symbols",
    );
    expect(result.current.logs[2]).toBe("Message with unicode: 日本語テスト");
  });

  // --- Performance and resource usage verification ---
  // These tests verify EventSource connection lifecycle, resource cleanup,
  // and proper behavior during tab switching (simulated via enabled toggling).

  it("closes EventSource when switching away from logs tab (enabled→disabled)", () => {
    const { result, rerender } = renderHook(
      ({ enabled }) => useInstanceLogs(42, enabled, "creation"),
      { initialProps: { enabled: true } },
    );

    const es = MockEventSource.instances[0];
    act(() => {
      es.simulateOpen();
    });
    expect(result.current.isConnected).toBe(true);
    expect(es.closed).toBe(false);

    // Simulate switching to a different tab (enabled becomes false)
    rerender({ enabled: false });

    // EventSource should be closed
    expect(es.closed).toBe(true);
    expect(result.current.isConnected).toBe(false);
  });

  it("creates new EventSource when returning to logs tab (disabled→enabled)", () => {
    const { result, rerender } = renderHook(
      ({ enabled }) => useInstanceLogs(42, enabled, "creation"),
      { initialProps: { enabled: true } },
    );

    const firstEs = MockEventSource.instances[0];
    act(() => {
      firstEs.simulateOpen();
      firstEs.simulateMessage("Event before tab switch");
    });
    expect(result.current.logs).toHaveLength(1);
    expect(MockEventSource.instances).toHaveLength(1);

    // Switch away from logs tab
    rerender({ enabled: false });
    expect(firstEs.closed).toBe(true);

    // Switch back to logs tab
    rerender({ enabled: true });

    // A NEW EventSource should have been created
    expect(MockEventSource.instances).toHaveLength(2);
    const secondEs = MockEventSource.instances[1];
    expect(secondEs.closed).toBe(false);
    expect(secondEs.url).toBe("/api/v1/instances/42/creation-logs");

    // New connection should work independently
    act(() => {
      secondEs.simulateOpen();
      secondEs.simulateMessage("Event after tab return");
    });
    expect(result.current.isConnected).toBe(true);
  });

  it("rapid tab switching (enable/disable cycles) creates and closes exactly one EventSource per cycle", () => {
    const { rerender } = renderHook(
      ({ enabled }) => useInstanceLogs(42, enabled, "creation"),
      { initialProps: { enabled: true } },
    );

    // Cycle 1: enabled
    expect(MockEventSource.instances).toHaveLength(1);
    expect(MockEventSource.instances[0].closed).toBe(false);

    // Cycle 1: disabled
    rerender({ enabled: false });
    expect(MockEventSource.instances[0].closed).toBe(true);

    // Cycle 2: enabled
    rerender({ enabled: true });
    expect(MockEventSource.instances).toHaveLength(2);
    expect(MockEventSource.instances[1].closed).toBe(false);

    // Cycle 2: disabled
    rerender({ enabled: false });
    expect(MockEventSource.instances[1].closed).toBe(true);

    // Cycle 3: enabled
    rerender({ enabled: true });
    expect(MockEventSource.instances).toHaveLength(3);
    expect(MockEventSource.instances[2].closed).toBe(false);

    // Cycle 3: disabled
    rerender({ enabled: false });
    expect(MockEventSource.instances[2].closed).toBe(true);

    // Verify: exactly 3 EventSources were created and all 3 were closed
    expect(MockEventSource.instances).toHaveLength(3);
    expect(MockEventSource.instances.every((es) => es.closed)).toBe(true);
  });

  it("no leaked EventSources when unmounting mid-stream (navigation away)", () => {
    const { result, unmount } = renderHook(() =>
      useInstanceLogs(42, true, "creation"),
    );
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      es.simulateMessage("Active stream event 1");
      es.simulateMessage("Active stream event 2");
    });
    expect(result.current.isConnected).toBe(true);
    expect(result.current.logs).toHaveLength(2);

    // Simulate navigation away (component unmount)
    unmount();

    // EventSource must be closed — no leaked connections
    expect(es.closed).toBe(true);
  });

  it("paused state does not prevent cleanup on disable", () => {
    const { result, rerender } = renderHook(
      ({ enabled }) => useInstanceLogs(42, enabled, "creation"),
      { initialProps: { enabled: true } },
    );
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      // Pause the stream
      result.current.togglePause();
    });
    expect(result.current.isPaused).toBe(true);

    // Switch away from tab while paused
    rerender({ enabled: false });

    // Connection should still be properly closed despite being paused
    expect(es.closed).toBe(true);
  });

  it("paused state does not prevent cleanup on unmount", () => {
    const { result, unmount } = renderHook(() =>
      useInstanceLogs(42, true, "creation"),
    );
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
      result.current.togglePause();
    });
    expect(result.current.isPaused).toBe(true);

    unmount();
    expect(es.closed).toBe(true);
  });

  it("logs array is properly managed during reconnection cycle (no stale data leak)", () => {
    const { result, rerender } = renderHook(
      ({ enabled, logType }) => useInstanceLogs(42, enabled, logType),
      { initialProps: { enabled: true, logType: "creation" as const } },
    );

    // Accumulate some logs
    act(() => {
      MockEventSource.instances[0].simulateOpen();
      MockEventSource.instances[0].simulateMessage("Session 1 event A");
      MockEventSource.instances[0].simulateMessage("Session 1 event B");
      MockEventSource.instances[0].simulateMessage("Session 1 event C");
    });
    expect(result.current.logs).toHaveLength(3);

    // Switch tab away (disable)
    rerender({ enabled: false, logType: "creation" as const });

    // Switch tab back (re-enable) — logs should still be available (cleared on type switch, not on disable)
    rerender({ enabled: true, logType: "creation" as const });

    // New EventSource is created
    const newEs = MockEventSource.instances[MockEventSource.instances.length - 1];
    act(() => {
      newEs.simulateOpen();
      newEs.simulateMessage("Session 2 event X");
    });

    // Session 2 event is appended; session 1 events may or may not remain
    // depending on whether logType changed. Since logType didn't change,
    // old logs persist (they're cleared only on logType switch).
    expect(result.current.logs.some((l) => l === "Session 2 event X")).toBe(true);
  });

  it("switching log type during active stream closes old and opens new connection", () => {
    const { rerender } = renderHook(
      ({ logType }) => useInstanceLogs(42, true, logType),
      { initialProps: { logType: "creation" as const } },
    );

    const creationEs = MockEventSource.instances[0];
    expect(creationEs.url).toContain("creation-logs");
    expect(creationEs.closed).toBe(false);

    // Switch to runtime logs
    rerender({ logType: "runtime" as const });

    // Old EventSource must be closed
    expect(creationEs.closed).toBe(true);

    // New EventSource should be opened with runtime URL
    const runtimeEs = MockEventSource.instances[MockEventSource.instances.length - 1];
    expect(runtimeEs.url).toContain("/logs?tail=100&follow=true");
    expect(runtimeEs.closed).toBe(false);
  });

  it("high volume messages don't cause issues with log accumulation", () => {
    const { result } = renderHook(() => useInstanceLogs(42, true, "creation"));
    const es = MockEventSource.instances[0];

    act(() => {
      es.simulateOpen();
    });

    // Simulate 1000 messages (stress test for log array growth)
    act(() => {
      for (let i = 0; i < 1000; i++) {
        es.simulateMessage(`High volume event ${i}`);
      }
    });

    expect(result.current.logs).toHaveLength(1000);
    expect(result.current.logs[0]).toBe("High volume event 0");
    expect(result.current.logs[999]).toBe("High volume event 999");

    // clearLogs should free all accumulated logs
    act(() => {
      result.current.clearLogs();
    });
    expect(result.current.logs).toHaveLength(0);
  });

  it("unmounting one concurrent instance cleans up only its EventSource", () => {
    const hook1 = renderHook(() => useInstanceLogs(1, true, "creation"));
    const hook2 = renderHook(() => useInstanceLogs(2, true, "creation"));
    const hook3 = renderHook(() => useInstanceLogs(3, true, "creation"));

    const es1 = MockEventSource.instances[0];
    const es2 = MockEventSource.instances[1];
    const es3 = MockEventSource.instances[2];

    // Unmount instance 2 (simulate navigating away from that tab)
    hook2.unmount();
    expect(es2.closed).toBe(true);

    // Instances 1 and 3 EventSources should still be open
    expect(es1.closed).toBe(false);
    expect(es3.closed).toBe(false);

    // They should still receive events
    act(() => {
      es1.simulateOpen();
      es3.simulateOpen();
    });

    act(() => {
      es1.simulateMessage("Instance 1: Still streaming");
      es3.simulateMessage("Instance 3: Still streaming");
    });

    expect(hook1.result.current.logs).toHaveLength(1);
    expect(hook3.result.current.logs).toHaveLength(1);
  });

  // --- Final integration smoke test ---

  describe("Smoke Test — Complete user journey", () => {
    it("simulates full creation → runtime → toggle lifecycle", () => {
      // Phase 1: Start with creation logs (instance is "creating")
      const { result, rerender } = renderHook(
        ({ logType }: { logType: "runtime" | "creation" }) =>
          useInstanceLogs(1, true, logType),
        { initialProps: { logType: "creation" as const } },
      );

      expect(MockEventSource.instances).toHaveLength(1);
      expect(MockEventSource.instances[0].url).toBe(
        "/api/v1/instances/1/creation-logs",
      );

      const creationES = MockEventSource.instances[0];

      // Simulate connection open + creation events streaming in real-time
      act(() => {
        creationES.simulateOpen();
      });
      expect(result.current.isConnected).toBe(true);

      act(() => {
        creationES.simulateMessage(
          "[STATUS] Waiting for pod to be scheduled...",
        );
        creationES.simulateMessage(
          "[EVENT] kubelet: Successfully assigned claworc/bot-smoke to node-1",
        );
        creationES.simulateMessage(
          '[EVENT] kubelet: Pulling image "ghcr.io/example/agent:latest"',
        );
        creationES.simulateMessage(
          "[EVENT] kubelet: Successfully pulled image in 12.3s",
        );
        creationES.simulateMessage(
          "[EVENT] kubelet: Created container agent",
        );
        creationES.simulateMessage(
          "[EVENT] kubelet: Started container agent",
        );
        creationES.simulateMessage(
          "[READY] All containers are running and ready",
        );
      });

      expect(result.current.logs).toHaveLength(7);
      expect(result.current.logs[0]).toContain("Waiting for pod");
      expect(result.current.logs[6]).toContain("running and ready");

      // Phase 2: Instance becomes "running" — auto-switch to runtime logs
      rerender({ logType: "runtime" });

      // Creation EventSource should be closed
      expect(creationES.closed).toBe(true);

      // New EventSource for runtime logs
      expect(MockEventSource.instances).toHaveLength(2);
      const runtimeES = MockEventSource.instances[1];
      expect(runtimeES.url).toBe(
        "/api/v1/instances/1/logs?tail=100&follow=true",
      );

      // Logs cleared on type switch
      expect(result.current.logs).toHaveLength(0);

      // Simulate runtime events
      act(() => {
        runtimeES.simulateOpen();
      });
      expect(result.current.isConnected).toBe(true);

      act(() => {
        runtimeES.simulateMessage(
          "2026-02-20T10:00:01Z Starting openclaw-gateway...",
        );
        runtimeES.simulateMessage(
          "2026-02-20T10:00:02Z Gateway listening on :8080",
        );
        runtimeES.simulateMessage(
          "2026-02-20T10:00:03Z Connected to Chrome debugger",
        );
      });

      expect(result.current.logs).toHaveLength(3);
      expect(result.current.logs[0]).toContain("Starting openclaw-gateway");

      // Phase 3: User toggles back to creation logs
      rerender({ logType: "creation" });

      expect(runtimeES.closed).toBe(true);
      expect(MockEventSource.instances).toHaveLength(3);
      const creationES2 = MockEventSource.instances[2];
      expect(creationES2.url).toBe(
        "/api/v1/instances/1/creation-logs",
      );

      // Logs cleared again
      expect(result.current.logs).toHaveLength(0);

      // Simulate guidance message from backend (instance is now running)
      act(() => {
        creationES2.simulateOpen();
        creationES2.simulateMessage(
          "Instance is not in creation phase. Switch to Runtime logs or restart the instance to see creation logs.",
        );
      });

      expect(result.current.logs).toHaveLength(1);
      expect(result.current.logs[0]).toContain("not in creation phase");

      // Phase 4: User switches back to runtime
      rerender({ logType: "runtime" });

      expect(creationES2.closed).toBe(true);
      expect(MockEventSource.instances).toHaveLength(4);
      const runtimeES2 = MockEventSource.instances[3];
      expect(runtimeES2.url).toBe(
        "/api/v1/instances/1/logs?tail=100&follow=true",
      );

      // Logs cleared, fresh connection
      expect(result.current.logs).toHaveLength(0);
    });

    it("simulates pause, clear, and resume during creation streaming", () => {
      const { result } = renderHook(() =>
        useInstanceLogs(1, true, "creation"),
      );
      const es = MockEventSource.instances[0];

      act(() => {
        es.simulateOpen();
      });

      // Stream some events
      act(() => {
        es.simulateMessage("Event 1: Pod scheduling");
        es.simulateMessage("Event 2: Image pulling");
      });
      expect(result.current.logs).toHaveLength(2);

      // Pause — new messages should not accumulate
      act(() => {
        result.current.togglePause();
      });
      expect(result.current.isPaused).toBe(true);

      act(() => {
        es.simulateMessage("Event 3: Missed during pause");
      });
      expect(result.current.logs).toHaveLength(2); // Still 2

      // Resume
      act(() => {
        result.current.togglePause();
      });
      expect(result.current.isPaused).toBe(false);

      act(() => {
        es.simulateMessage("Event 4: After resume");
      });
      expect(result.current.logs).toHaveLength(3);

      // Clear logs
      act(() => {
        result.current.clearLogs();
      });
      expect(result.current.logs).toHaveLength(0);

      // New events still accumulate after clear
      act(() => {
        es.simulateMessage("Event 5: Fresh start");
      });
      expect(result.current.logs).toHaveLength(1);
      expect(result.current.logs[0]).toBe("Event 5: Fresh start");
    });

    it("simulates tab switch away and back (enable/disable cycle)", () => {
      const { result, rerender } = renderHook(
        ({ enabled }: { enabled: boolean }) =>
          useInstanceLogs(1, enabled, "creation"),
        { initialProps: { enabled: true } },
      );

      const es1 = MockEventSource.instances[0];

      act(() => {
        es1.simulateOpen();
        es1.simulateMessage("Creation event 1");
      });
      expect(result.current.logs).toHaveLength(1);
      expect(result.current.isConnected).toBe(true);

      // Switch away from Logs tab (disable)
      rerender({ enabled: false });
      expect(es1.closed).toBe(true);
      expect(result.current.isConnected).toBe(false);

      // Switch back to Logs tab (enable)
      rerender({ enabled: true });
      expect(MockEventSource.instances).toHaveLength(2);
      const es2 = MockEventSource.instances[1];
      expect(es2.closed).toBe(false);

      // New connection, logs persist from previous session
      act(() => {
        es2.simulateOpen();
        es2.simulateMessage("Creation event 2");
      });
      expect(result.current.isConnected).toBe(true);
      // Note: logs from previous session are still present since we didn't change logType
      expect(result.current.logs).toHaveLength(2);
    });
  });
});
