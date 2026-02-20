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
});
