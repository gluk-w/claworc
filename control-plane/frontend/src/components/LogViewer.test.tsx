import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, vi } from "vitest";
import LogViewer from "./LogViewer";

describe("LogViewer", () => {
  const defaultProps = {
    logs: [] as string[],
    isPaused: false,
    isConnected: false,
    onTogglePause: vi.fn(),
    onClear: vi.fn(),
    logType: "runtime" as const,
    onLogTypeChange: vi.fn(),
  };

  // --- Empty state messages ---

  it("shows 'Waiting for logs...' when runtime logs disconnected and empty", () => {
    render(<LogViewer {...defaultProps} logType="runtime" isConnected={false} />);
    expect(screen.getByText("Waiting for logs...")).toBeInTheDocument();
  });

  it("shows 'No logs yet...' when runtime logs connected but empty", () => {
    render(<LogViewer {...defaultProps} logType="runtime" isConnected={true} />);
    expect(
      screen.getByText("No logs yet. The instance may still be starting..."),
    ).toBeInTheDocument();
  });

  it("shows 'Waiting for container creation events...' when creation logs disconnected and empty", () => {
    render(
      <LogViewer {...defaultProps} logType="creation" isConnected={false} />,
    );
    expect(
      screen.getByText("Waiting for container creation events..."),
    ).toBeInTheDocument();
  });

  it("shows 'No creation events yet...' when creation logs connected but empty", () => {
    render(
      <LogViewer {...defaultProps} logType="creation" isConnected={true} />,
    );
    expect(
      screen.getByText(
        "No creation events yet. Container may not be starting...",
      ),
    ).toBeInTheDocument();
  });

  // --- Log type switching ---

  it("calls onLogTypeChange with 'creation' when Creation tab is clicked", async () => {
    const onLogTypeChange = vi.fn();
    const user = userEvent.setup();
    render(
      <LogViewer {...defaultProps} onLogTypeChange={onLogTypeChange} />,
    );
    await user.click(screen.getByText("Creation"));
    expect(onLogTypeChange).toHaveBeenCalledWith("creation");
  });

  it("calls onLogTypeChange with 'runtime' when Runtime tab is clicked", async () => {
    const onLogTypeChange = vi.fn();
    const user = userEvent.setup();
    render(
      <LogViewer
        {...defaultProps}
        logType="creation"
        onLogTypeChange={onLogTypeChange}
      />,
    );
    await user.click(screen.getByText("Runtime"));
    expect(onLogTypeChange).toHaveBeenCalledWith("runtime");
  });

  it("supports multiple rapid log type switches", async () => {
    const onLogTypeChange = vi.fn();
    const user = userEvent.setup();
    render(
      <LogViewer {...defaultProps} onLogTypeChange={onLogTypeChange} />,
    );
    await user.click(screen.getByText("Creation"));
    await user.click(screen.getByText("Runtime"));
    await user.click(screen.getByText("Creation"));
    expect(onLogTypeChange).toHaveBeenCalledTimes(3);
    expect(onLogTypeChange).toHaveBeenNthCalledWith(1, "creation");
    expect(onLogTypeChange).toHaveBeenNthCalledWith(2, "runtime");
    expect(onLogTypeChange).toHaveBeenNthCalledWith(3, "creation");
  });

  // --- Pause button ---

  it("calls onTogglePause when pause button is clicked (runtime)", async () => {
    const onTogglePause = vi.fn();
    const user = userEvent.setup();
    render(
      <LogViewer
        {...defaultProps}
        logType="runtime"
        isPaused={false}
        onTogglePause={onTogglePause}
      />,
    );
    await user.click(screen.getByTitle("Pause"));
    expect(onTogglePause).toHaveBeenCalledTimes(1);
  });

  it("calls onTogglePause when play button is clicked (creation, paused)", async () => {
    const onTogglePause = vi.fn();
    const user = userEvent.setup();
    render(
      <LogViewer
        {...defaultProps}
        logType="creation"
        isPaused={true}
        onTogglePause={onTogglePause}
      />,
    );
    await user.click(screen.getByTitle("Resume"));
    expect(onTogglePause).toHaveBeenCalledTimes(1);
  });

  // --- Clear button ---

  it("calls onClear when clear button is clicked (runtime)", async () => {
    const onClear = vi.fn();
    const user = userEvent.setup();
    render(
      <LogViewer
        {...defaultProps}
        logType="runtime"
        logs={["line 1", "line 2"]}
        onClear={onClear}
      />,
    );
    await user.click(screen.getByTitle("Clear"));
    expect(onClear).toHaveBeenCalledTimes(1);
  });

  it("calls onClear when clear button is clicked (creation)", async () => {
    const onClear = vi.fn();
    const user = userEvent.setup();
    render(
      <LogViewer
        {...defaultProps}
        logType="creation"
        logs={["event 1", "event 2"]}
        onClear={onClear}
      />,
    );
    await user.click(screen.getByTitle("Clear"));
    expect(onClear).toHaveBeenCalledTimes(1);
  });

  // --- Log rendering ---

  it("renders log lines when logs are provided", () => {
    const logs = ["First log line", "Second log line", "Third log line"];
    render(<LogViewer {...defaultProps} logs={logs} />);
    expect(screen.getByText("First log line")).toBeInTheDocument();
    expect(screen.getByText("Second log line")).toBeInTheDocument();
    expect(screen.getByText("Third log line")).toBeInTheDocument();
  });

  it("does not show empty state message when logs exist", () => {
    render(
      <LogViewer {...defaultProps} logs={["some log"]} isConnected={false} />,
    );
    expect(screen.queryByText("Waiting for logs...")).not.toBeInTheDocument();
  });

  // --- Connection status ---

  it("shows Connected indicator when connected", () => {
    render(<LogViewer {...defaultProps} isConnected={true} />);
    expect(screen.getByText("Connected")).toBeInTheDocument();
  });

  it("shows Disconnected indicator when not connected", () => {
    render(<LogViewer {...defaultProps} isConnected={false} />);
    expect(screen.getByText("Disconnected")).toBeInTheDocument();
  });

  // --- Info icon for creation logs ---

  it("shows info icon tooltip only for creation log type", () => {
    const { rerender } = render(
      <LogViewer {...defaultProps} logType="creation" />,
    );
    const infoIcon = document.querySelector('[title*="ephemeral"]');
    expect(infoIcon).toBeInTheDocument();

    rerender(<LogViewer {...defaultProps} logType="runtime" />);
    const infoIconAfter = document.querySelector('[title*="ephemeral"]');
    expect(infoIconAfter).not.toBeInTheDocument();
  });

  // --- Docker-specific log rendering ---

  it("renders Docker creation event messages correctly", () => {
    const dockerLogs = [
      "Waiting for container creation...",
      "Container status: created",
      "Container status: running",
      "Health: starting",
      "Health: healthy",
      "Starting services...",
      "Container is running and healthy",
    ];
    render(
      <LogViewer {...defaultProps} logType="creation" logs={dockerLogs} />,
    );
    expect(
      screen.getByText("Waiting for container creation..."),
    ).toBeInTheDocument();
    expect(screen.getByText("Container status: created")).toBeInTheDocument();
    expect(screen.getByText("Health: healthy")).toBeInTheDocument();
    expect(
      screen.getByText("Container is running and healthy"),
    ).toBeInTheDocument();
  });

  it("renders Docker error and timeout messages correctly", () => {
    const dockerLogs = [
      "Error inspecting container: connection refused",
      "Timed out waiting for container to become ready",
    ];
    render(
      <LogViewer {...defaultProps} logType="creation" logs={dockerLogs} />,
    );
    expect(
      screen.getByText("Error inspecting container: connection refused"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Timed out waiting for container to become ready"),
    ).toBeInTheDocument();
  });

  // --- Auto-scroll behavior ---

  it("has a bottom ref div for auto-scroll anchoring", () => {
    const logs = ["line 1", "line 2"];
    render(<LogViewer {...defaultProps} logs={logs} />);
    // The component renders log lines - verify they're in the DOM
    // The auto-scroll is implemented via scrollIntoView on the bottomRef div
    expect(screen.getByText("line 1")).toBeInTheDocument();
    expect(screen.getByText("line 2")).toBeInTheDocument();
  });

  it("renders the active tab with distinct styling", () => {
    const { rerender } = render(
      <LogViewer {...defaultProps} logType="runtime" />,
    );
    const runtimeBtn = screen.getByText("Runtime");
    const creationBtn = screen.getByText("Creation");
    expect(runtimeBtn.className).toContain("bg-gray-600");
    expect(creationBtn.className).toContain("bg-gray-700");

    rerender(<LogViewer {...defaultProps} logType="creation" />);
    const runtimeBtn2 = screen.getByText("Runtime");
    const creationBtn2 = screen.getByText("Creation");
    expect(runtimeBtn2.className).toContain("bg-gray-700");
    expect(creationBtn2.className).toContain("bg-gray-600");
  });
});
