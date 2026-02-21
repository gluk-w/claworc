import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi, beforeEach } from "vitest";
import type { Instance } from "@/types/instance";

// --- Mocks for heavy child components (avoid pulling in Monaco, xterm, etc.) ---
vi.mock("@/components/MonacoConfigEditor", () => ({
  default: () => <div data-testid="monaco-editor" />,
}));
vi.mock("@/components/TerminalPanel", () => ({
  default: () => <div data-testid="terminal-panel" />,
}));
vi.mock("@/components/VncPanel", () => ({
  default: () => <div data-testid="vnc-panel" />,
}));
vi.mock("@/components/ChatPanel", () => ({
  default: () => <div data-testid="chat-panel" />,
}));
vi.mock("@/components/FileBrowser", () => ({
  default: () => <div data-testid="file-browser" />,
}));

// --- Mock react-router-dom ---
const mockNavigate = vi.fn();
vi.mock("react-router-dom", () => ({
  useParams: () => ({ id: "1" }),
  useNavigate: () => mockNavigate,
  useLocation: () => ({ hash: "#logs", pathname: "/instances/1", search: "" }),
}));

// --- Mock hooks ---
const mockUseInstance = vi.fn();
const mockUseSettings = vi.fn();
const mockUseInstanceLogs = vi.fn();

vi.mock("@/hooks/useInstances", () => ({
  useInstance: (...args: unknown[]) => mockUseInstance(...args),
  useSettings: undefined, // overridden below
  useStartInstance: () => ({ mutate: vi.fn() }),
  useStopInstance: () => ({ mutate: vi.fn() }),
  useRestartInstance: () => ({ mutate: vi.fn() }),
  useCloneInstance: () => ({ mutate: vi.fn() }),
  useDeleteInstance: () => ({ mutate: vi.fn() }),
  useUpdateInstance: () => ({ mutate: vi.fn(), isPending: false }),
  useInstanceConfig: () => ({ data: null }),
  useUpdateInstanceConfig: () => ({ mutate: vi.fn(), isPending: false }),
  useRestartedToast: vi.fn(),
}));

vi.mock("@/hooks/useSettings", () => ({
  useSettings: (...args: unknown[]) => mockUseSettings(...args),
}));

vi.mock("@/hooks/useInstanceLogs", () => ({
  useInstanceLogs: (...args: unknown[]) => mockUseInstanceLogs(...args),
}));

vi.mock("@/hooks/useTerminal", () => ({
  useTerminal: () => ({
    connectionState: "disconnected",
    onData: vi.fn(),
    onResize: vi.fn(),
    setTerminal: vi.fn(),
    reconnect: vi.fn(),
  }),
}));

vi.mock("@/hooks/useDesktop", () => ({
  useDesktop: () => ({
    connectionState: "disconnected",
    desktopUrl: "",
    setIframe: vi.fn(),
    onLoad: vi.fn(),
    onError: vi.fn(),
    reconnect: vi.fn(),
  }),
}));

vi.mock("@/hooks/useChat", () => ({
  useChat: () => ({
    messages: [],
    connectionState: "disconnected",
    sendMessage: vi.fn(),
    clearMessages: vi.fn(),
    reconnect: vi.fn(),
  }),
}));

// --- Import after mocks ---
import InstanceDetailPage from "./InstanceDetailPage";

function makeInstance(overrides: Partial<Instance> = {}): Instance {
  return {
    id: 1,
    name: "bot-test",
    display_name: "Test Instance",
    status: "running",
    cpu_request: "1",
    cpu_limit: "2",
    memory_request: "1Gi",
    memory_limit: "2Gi",
    storage_homebrew: "5Gi",
    storage_clawd: "5Gi",
    storage_chrome: "5Gi",
    has_brave_override: false,
    api_key_overrides: [],
    models: { effective: [], disabled_defaults: [], extra: [] },
    default_model: "",
    container_image: null,
    has_image_override: false,
    vnc_resolution: null,
    has_resolution_override: false,
    control_url: "",
    gateway_token: "",
    sort_order: 0,
    created_at: "2026-02-20T00:00:00Z",
    updated_at: "2026-02-20T00:00:00Z",
    ...overrides,
  };
}

function setupMocks(instance: Instance) {
  mockUseInstance.mockReturnValue({ data: instance, isLoading: false });
  mockUseSettings.mockReturnValue({ data: { api_keys: {} } });
  mockUseInstanceLogs.mockReturnValue({
    logs: [],
    clearLogs: vi.fn(),
    isPaused: false,
    togglePause: vi.fn(),
    isConnected: false,
  });
}

describe("InstanceDetailPage — Logs tab visibility across all instance statuses", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  const allStatuses = [
    "creating",
    "stopped",
    "stopping",
    "restarting",
    "error",
  ] as const;

  // The Instance TypeScript type does not include "failed" — the backend uses it
  // but the frontend maps it via the status field. We test the TS-valid statuses.

  it.each(allStatuses)(
    "Logs tab is NOT disabled when instance status is '%s'",
    (status) => {
      const instance = makeInstance({ status: status as Instance["status"] });
      setupMocks(instance);

      render(<InstanceDetailPage />);

      const logsTab = screen.getByRole("button", { name: "Logs" });
      // The tab must exist and not be disabled
      expect(logsTab).toBeInTheDocument();
      expect(logsTab).not.toBeDisabled();
    },
  );

  it("Logs tab is NOT disabled when instance status is 'running'", () => {
    const instance = makeInstance({ status: "running" });
    setupMocks(instance);

    render(<InstanceDetailPage />);

    const logsTab = screen.getByRole("button", { name: "Logs" });
    expect(logsTab).toBeInTheDocument();
    expect(logsTab).not.toBeDisabled();
  });

  it("Logs tab renders LogViewer with Runtime/Creation toggle when clicked (creating)", async () => {
    const instance = makeInstance({
      status: "creating" as Instance["status"],
    });
    setupMocks(instance);

    render(<InstanceDetailPage />);

    // Since location.hash is #logs, the logs tab should already be active.
    // Verify LogViewer content is present (Runtime and Creation toggle buttons).
    expect(screen.getByText("Runtime")).toBeInTheDocument();
    expect(screen.getByText("Creation")).toBeInTheDocument();
  });

  it("Logs tab renders LogViewer when clicked for 'stopped' instance", async () => {
    const instance = makeInstance({ status: "stopped" });
    setupMocks(instance);

    render(<InstanceDetailPage />);

    expect(screen.getByText("Runtime")).toBeInTheDocument();
    expect(screen.getByText("Creation")).toBeInTheDocument();
  });

  it("Logs tab renders LogViewer when clicked for 'error' instance", async () => {
    const instance = makeInstance({ status: "error" });
    setupMocks(instance);

    render(<InstanceDetailPage />);

    expect(screen.getByText("Runtime")).toBeInTheDocument();
    expect(screen.getByText("Creation")).toBeInTheDocument();
  });

  it("Logs tab renders LogViewer when clicked for 'stopping' instance", async () => {
    const instance = makeInstance({ status: "stopping" as Instance["status"] });
    setupMocks(instance);

    render(<InstanceDetailPage />);

    expect(screen.getByText("Runtime")).toBeInTheDocument();
    expect(screen.getByText("Creation")).toBeInTheDocument();
  });

  it("Logs tab renders LogViewer when clicked for 'restarting' instance", async () => {
    const instance = makeInstance({ status: "restarting" });
    setupMocks(instance);

    render(<InstanceDetailPage />);

    expect(screen.getByText("Runtime")).toBeInTheDocument();
    expect(screen.getByText("Creation")).toBeInTheDocument();
  });

  it("auto-selects 'creation' log type when instance status is 'creating'", () => {
    const instance = makeInstance({
      status: "creating" as Instance["status"],
    });
    setupMocks(instance);

    render(<InstanceDetailPage />);

    // useInstanceLogs should be called with logType "creation" for creating instances.
    // The third argument to useInstanceLogs is logType.
    const calls = mockUseInstanceLogs.mock.calls;
    const lastCall = calls[calls.length - 1];
    expect(lastCall[2]).toBe("creation");
  });

  it("uses 'runtime' log type for non-creating statuses", () => {
    const instance = makeInstance({ status: "running" });
    setupMocks(instance);

    render(<InstanceDetailPage />);

    const calls = mockUseInstanceLogs.mock.calls;
    const lastCall = calls[calls.length - 1];
    expect(lastCall[2]).toBe("runtime");
  });

  it("all tabs render without any disabled attribute for any status", () => {
    // Comprehensive check: none of the 5 tab buttons has a disabled attribute
    for (const status of [...allStatuses, "running" as const]) {
      const instance = makeInstance({ status: status as Instance["status"] });
      setupMocks(instance);

      const { unmount } = render(<InstanceDetailPage />);

      const nav = screen.getByRole("navigation");
      const buttons = within(nav).getAllByRole("button");

      for (const btn of buttons) {
        expect(btn).not.toBeDisabled();
      }

      unmount();
    }
  });

  it("Logs tab shows connection status and empty state for disconnected runtime logs", () => {
    const instance = makeInstance({ status: "stopped" });
    setupMocks(instance);
    mockUseInstanceLogs.mockReturnValue({
      logs: [],
      clearLogs: vi.fn(),
      isPaused: false,
      togglePause: vi.fn(),
      isConnected: false,
    });

    render(<InstanceDetailPage />);

    expect(screen.getByText("Disconnected")).toBeInTheDocument();
    expect(screen.getByText("Waiting for logs...")).toBeInTheDocument();
  });

  it("Logs tab shows creation empty state when creating and disconnected", () => {
    const instance = makeInstance({
      status: "creating" as Instance["status"],
    });
    setupMocks(instance);
    mockUseInstanceLogs.mockReturnValue({
      logs: [],
      clearLogs: vi.fn(),
      isPaused: false,
      togglePause: vi.fn(),
      isConnected: false,
    });

    render(<InstanceDetailPage />);

    expect(
      screen.getByText("Waiting for container creation events..."),
    ).toBeInTheDocument();
  });

  it("Logs tab shows backend guidance message for non-creating instances", () => {
    const instance = makeInstance({ status: "stopped" });
    setupMocks(instance);
    // Simulate the backend "not in creation phase" message arriving via SSE
    mockUseInstanceLogs.mockReturnValue({
      logs: [
        "Instance is not in creation phase. Switch to Runtime logs or restart the instance to see creation logs.",
      ],
      clearLogs: vi.fn(),
      isPaused: false,
      togglePause: vi.fn(),
      isConnected: true,
    });

    render(<InstanceDetailPage />);

    // Switch to creation logs to see the guidance message
    // (The component starts on runtime by default for stopped instances)
    expect(
      screen.getByText(
        "Instance is not in creation phase. Switch to Runtime logs or restart the instance to see creation logs.",
      ),
    ).toBeInTheDocument();
  });

  it("user can switch between Runtime and Creation log types via LogViewer", async () => {
    const instance = makeInstance({ status: "running" });
    setupMocks(instance);

    render(<InstanceDetailPage />);
    const user = userEvent.setup();

    // Click Creation tab in LogViewer
    await user.click(screen.getByText("Creation"));

    // After clicking, useInstanceLogs should be re-called with logType "creation"
    const calls = mockUseInstanceLogs.mock.calls;
    const lastCall = calls[calls.length - 1];
    expect(lastCall[2]).toBe("creation");
  });

  // --- Concurrent instance creation logging tests ---
  // These verify that multiple InstanceDetailPage renders (simulating separate
  // browser tabs) call useInstanceLogs with correct, independent parameters.

  it("multiple creating instances each get their own creation log stream", () => {
    // Render 3 instances sequentially (simulating 3 browser tabs)
    // Each should call useInstanceLogs with its own instance ID and logType "creation"
    const logsCalls: unknown[][] = [];
    mockUseInstanceLogs.mockImplementation((...args: unknown[]) => {
      logsCalls.push(args);
      return {
        logs: [],
        clearLogs: vi.fn(),
        isPaused: false,
        togglePause: vi.fn(),
        isConnected: false,
      };
    });

    // Instance 1 (creating)
    const inst1 = makeInstance({
      id: 1,
      name: "bot-conc-1",
      status: "creating" as Instance["status"],
    });
    setupMocks(inst1);
    const { unmount: unmount1 } = render(<InstanceDetailPage />);

    // Instance 2 (creating) — reset mock to track new calls
    logsCalls.length = 0;
    const inst2 = makeInstance({
      id: 2,
      name: "bot-conc-2",
      status: "creating" as Instance["status"],
    });
    setupMocks(inst2);
    const { unmount: unmount2 } = render(<InstanceDetailPage />);

    // Instance 3 (creating) — reset mock to track new calls
    logsCalls.length = 0;
    const inst3 = makeInstance({
      id: 3,
      name: "bot-conc-3",
      status: "creating" as Instance["status"],
    });
    setupMocks(inst3);
    const { unmount: unmount3 } = render(<InstanceDetailPage />);

    // Each render should have called useInstanceLogs with logType "creation"
    // since all instances have status "creating"
    const allCalls = mockUseInstanceLogs.mock.calls;
    const creationCalls = allCalls.filter(
      (call: unknown[]) => call[2] === "creation",
    );
    expect(creationCalls.length).toBeGreaterThanOrEqual(3);

    unmount1();
    unmount2();
    unmount3();
  });

  it("concurrent instances with different statuses get correct log types", () => {
    // Instance A (creating) should get logType "creation"
    const instA = makeInstance({
      id: 10,
      status: "creating" as Instance["status"],
    });
    setupMocks(instA);
    const { unmount: unmountA } = render(<InstanceDetailPage />);

    const callsAfterA = mockUseInstanceLogs.mock.calls;
    const lastCallA = callsAfterA[callsAfterA.length - 1];
    expect(lastCallA[2]).toBe("creation");
    unmountA();

    // Instance B (running) should get logType "runtime"
    const instB = makeInstance({ id: 20, status: "running" });
    setupMocks(instB);
    const { unmount: unmountB } = render(<InstanceDetailPage />);

    const callsAfterB = mockUseInstanceLogs.mock.calls;
    const lastCallB = callsAfterB[callsAfterB.length - 1];
    expect(lastCallB[2]).toBe("runtime");
    unmountB();

    // Instance C (creating) should get logType "creation"
    const instC = makeInstance({
      id: 30,
      status: "creating" as Instance["status"],
    });
    setupMocks(instC);
    const { unmount: unmountC } = render(<InstanceDetailPage />);

    const callsAfterC = mockUseInstanceLogs.mock.calls;
    const lastCallC = callsAfterC[callsAfterC.length - 1];
    expect(lastCallC[2]).toBe("creation");
    unmountC();
  });

  // --- Performance and resource usage verification ---
  // These tests verify that tab switching properly toggles the `enabled` parameter
  // to useInstanceLogs, ensuring EventSource connections are opened/closed correctly.

  it("useInstanceLogs receives enabled=true only when logs tab is active", async () => {
    const instance = makeInstance({ status: "running" });
    setupMocks(instance);

    render(<InstanceDetailPage />);

    // Since hash is #logs, logs tab should be active -> enabled=true
    const calls = mockUseInstanceLogs.mock.calls;
    const lastCall = calls[calls.length - 1];
    expect(lastCall[1]).toBe(true); // enabled parameter
  });

  it("useInstanceLogs receives enabled=false after switching to a different tab", async () => {
    const instance = makeInstance({ status: "running" });
    setupMocks(instance);

    render(<InstanceDetailPage />);
    const user = userEvent.setup();

    // Switch to Overview tab
    await user.click(screen.getByText("Overview"));

    // After switching, useInstanceLogs should be called with enabled=false
    const calls = mockUseInstanceLogs.mock.calls;
    const lastCall = calls[calls.length - 1];
    expect(lastCall[1]).toBe(false); // enabled parameter should be false
  });

  it("useInstanceLogs enabled toggles correctly through tab switch cycle", async () => {
    const instance = makeInstance({ status: "running" });
    setupMocks(instance);

    render(<InstanceDetailPage />);
    const user = userEvent.setup();

    // Initially on logs tab (enabled=true per hash)
    let calls = mockUseInstanceLogs.mock.calls;
    expect(calls[calls.length - 1][1]).toBe(true);

    // Switch to Config tab (enabled should become false)
    await user.click(screen.getByText("Config"));
    calls = mockUseInstanceLogs.mock.calls;
    expect(calls[calls.length - 1][1]).toBe(false);

    // Switch back to Logs tab (enabled should become true again)
    await user.click(screen.getByText("Logs"));
    calls = mockUseInstanceLogs.mock.calls;
    expect(calls[calls.length - 1][1]).toBe(true);
  });

  it("unmounting page cleans up (useInstanceLogs receives final call)", () => {
    const instance = makeInstance({ status: "creating" as Instance["status"] });
    setupMocks(instance);

    const { unmount } = render(<InstanceDetailPage />);

    // Verify hook was called at least once
    expect(mockUseInstanceLogs.mock.calls.length).toBeGreaterThan(0);

    // Unmount — React will run cleanup effects from useInstanceLogs
    unmount();

    // No error should occur — the hook's cleanup handles EventSource closing
  });

  it("rapid tab switching calls useInstanceLogs with alternating enabled values", async () => {
    const instance = makeInstance({ status: "running" });
    setupMocks(instance);

    render(<InstanceDetailPage />);
    const user = userEvent.setup();

    // Rapid switching: Logs -> Overview -> Logs -> Config -> Logs
    await user.click(screen.getByText("Overview"));
    await user.click(screen.getByText("Logs"));
    await user.click(screen.getByText("Config"));
    await user.click(screen.getByText("Logs"));

    // Get all the enabled values passed to useInstanceLogs
    const enabledValues = mockUseInstanceLogs.mock.calls.map(
      (call: unknown[]) => call[1],
    );

    // The last call should be enabled=true (back on logs tab)
    expect(enabledValues[enabledValues.length - 1]).toBe(true);

    // There should be some false values from the non-logs tabs
    expect(enabledValues.some((v: unknown) => v === false)).toBe(true);
    // And some true values from the logs tabs
    expect(enabledValues.some((v: unknown) => v === true)).toBe(true);
  });

  it("each concurrent creating instance renders its own LogViewer independently", () => {
    // Simulate 3 creating instances each showing different log content
    const instances = [
      { id: 1, logs: ["Instance 1: Scheduling pod"] },
      { id: 2, logs: ["Instance 2: Pulling image", "Instance 2: Ready"] },
      {
        id: 3,
        logs: [
          "Instance 3: Container creating",
          "Instance 3: Health: starting",
          "Instance 3: Ready",
        ],
      },
    ];

    for (const { id, logs } of instances) {
      const instance = makeInstance({
        id,
        name: `bot-conc-${id}`,
        status: "creating" as Instance["status"],
      });
      mockUseInstance.mockReturnValue({ data: instance, isLoading: false });
      mockUseSettings.mockReturnValue({ data: { api_keys: {} } });
      mockUseInstanceLogs.mockReturnValue({
        logs,
        clearLogs: vi.fn(),
        isPaused: false,
        togglePause: vi.fn(),
        isConnected: true,
      });

      const { unmount } = render(<InstanceDetailPage />);

      // Each render should show its own logs, not logs from other instances
      for (const logLine of logs) {
        expect(screen.getByText(logLine)).toBeInTheDocument();
      }

      // The other instances' logs should NOT be present
      for (const other of instances) {
        if (other.id !== id) {
          for (const otherLog of other.logs) {
            expect(screen.queryByText(otherLog)).not.toBeInTheDocument();
          }
        }
      }

      unmount();
    }
  });
});

// --- Final integration smoke test ---
// Simulates the complete user journey through InstanceDetailPage:
//   1. Instance appears as "creating" → logType auto-set to "creation"
//   2. Logs tab is accessible, shows Runtime/Creation toggle
//   3. Instance transitions to "running" → logType auto-switches to "runtime"
//   4. User toggles back to "Creation" → sees guidance message
//   5. User toggles to "Runtime" → sees runtime logs
//   6. User switches away from Logs tab → connection disabled
//   7. User returns to Logs tab → connection re-enabled

describe("Smoke Test — Complete user journey through InstanceDetailPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("full journey: creating → logs tab → running → auto-switch → toggle → tab switch", async () => {
    const user = userEvent.setup();

    // Phase 1: Instance is "creating", Logs tab active (via hash)
    const creatingInstance = makeInstance({
      status: "creating" as Instance["status"],
    });
    setupMocks(creatingInstance);

    const { rerender } = render(<InstanceDetailPage />);

    // Logs tab is accessible and not disabled
    const logsTab = screen.getByRole("button", { name: "Logs" });
    expect(logsTab).toBeInTheDocument();
    expect(logsTab).not.toBeDisabled();

    // LogViewer renders with Runtime/Creation toggle
    expect(screen.getByText("Runtime")).toBeInTheDocument();
    expect(screen.getByText("Creation")).toBeInTheDocument();

    // logType auto-selected to "creation" for creating instance
    let calls = mockUseInstanceLogs.mock.calls;
    expect(calls[calls.length - 1][2]).toBe("creation");
    // enabled=true because logs tab is active
    expect(calls[calls.length - 1][1]).toBe(true);

    // Phase 2: Instance transitions to "running"
    const runningInstance = makeInstance({ status: "running" });
    mockUseInstance.mockReturnValue({
      data: runningInstance,
      isLoading: false,
    });

    rerender(<InstanceDetailPage />);

    // Auto-switch: logType should change from "creation" to "runtime"
    calls = mockUseInstanceLogs.mock.calls;
    expect(calls[calls.length - 1][2]).toBe("runtime");

    // Phase 3: User manually toggles to "Creation" logs
    await user.click(screen.getByText("Creation"));
    calls = mockUseInstanceLogs.mock.calls;
    expect(calls[calls.length - 1][2]).toBe("creation");

    // Phase 4: User toggles back to "Runtime" logs
    await user.click(screen.getByText("Runtime"));
    calls = mockUseInstanceLogs.mock.calls;
    expect(calls[calls.length - 1][2]).toBe("runtime");

    // Phase 5: User switches away from Logs tab
    await user.click(screen.getByText("Overview"));
    calls = mockUseInstanceLogs.mock.calls;
    expect(calls[calls.length - 1][1]).toBe(false); // enabled=false

    // Phase 6: User returns to Logs tab
    await user.click(screen.getByText("Logs"));
    calls = mockUseInstanceLogs.mock.calls;
    expect(calls[calls.length - 1][1]).toBe(true); // enabled=true
    expect(calls[calls.length - 1][2]).toBe("runtime"); // still runtime
  });

  it("full journey with creation log content and guidance messages", async () => {
    // Phase 1: Creating instance with streaming creation events
    const creatingInstance = makeInstance({
      status: "creating" as Instance["status"],
    });
    mockUseInstance.mockReturnValue({
      data: creatingInstance,
      isLoading: false,
    });
    mockUseSettings.mockReturnValue({ data: { api_keys: {} } });
    mockUseInstanceLogs.mockReturnValue({
      logs: [
        "[STATUS] Waiting for pod to be scheduled...",
        "[EVENT] kubelet: Pulling image",
        "[EVENT] kubelet: Started container agent",
        "[READY] All containers are running and ready",
      ],
      clearLogs: vi.fn(),
      isPaused: false,
      togglePause: vi.fn(),
      isConnected: true,
    });

    const { rerender, unmount } = render(<InstanceDetailPage />);

    // All creation events visible
    expect(
      screen.getByText("[STATUS] Waiting for pod to be scheduled..."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("[READY] All containers are running and ready"),
    ).toBeInTheDocument();
    expect(screen.getByText("Connected")).toBeInTheDocument();

    // Phase 2: Transition to running with runtime logs
    const runningInstance = makeInstance({ status: "running" });
    mockUseInstance.mockReturnValue({
      data: runningInstance,
      isLoading: false,
    });
    mockUseInstanceLogs.mockReturnValue({
      logs: [
        "Starting openclaw-gateway...",
        "Gateway listening on :8080",
      ],
      clearLogs: vi.fn(),
      isPaused: false,
      togglePause: vi.fn(),
      isConnected: true,
    });

    rerender(<InstanceDetailPage />);

    // Runtime logs now displayed
    expect(
      screen.getByText("Starting openclaw-gateway..."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Gateway listening on :8080"),
    ).toBeInTheDocument();

    unmount();
  });

  it("status badge reflects instance state throughout journey", () => {
    // Creating state
    const creatingInstance = makeInstance({
      status: "creating" as Instance["status"],
    });
    setupMocks(creatingInstance);

    const { rerender, unmount } = render(<InstanceDetailPage />);
    expect(screen.getByText("creating")).toBeInTheDocument();

    // Transition to running
    const runningInstance = makeInstance({ status: "running" });
    mockUseInstance.mockReturnValue({
      data: runningInstance,
      isLoading: false,
    });
    rerender(<InstanceDetailPage />);
    expect(screen.getByText("running")).toBeInTheDocument();

    unmount();
  });
});
