import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import SSHStatus from "../SSHStatus";
import { renderWithProviders } from "@/test/helpers";
import type { SSHStatusResponse } from "@/types/ssh";

const mockSSHStatus: SSHStatusResponse = {
  connection_state: "connected",
  health: {
    connected_at: "2026-02-21T10:00:00Z",
    last_health_check: "2026-02-21T12:00:00Z",
    uptime_seconds: 7200,
    successful_checks: 100,
    failed_checks: 2,
    healthy: true,
  },
  tunnels: [
    {
      service: "VNC",
      local_port: 5900,
      remote_port: 5900,
      created_at: "2026-02-21T10:00:00Z",
      last_check: "2026-02-21T12:00:00Z",
      bytes_transferred: 1048576,
      healthy: true,
    },
    {
      service: "Gateway",
      local_port: 8080,
      remote_port: 8080,
      created_at: "2026-02-21T10:00:00Z",
      last_check: "2026-02-21T12:00:00Z",
      bytes_transferred: 2048,
      healthy: false,
    },
  ],
  recent_events: [],
};

vi.mock("@/api/ssh", () => ({
  fetchSSHStatus: vi.fn(),
  fetchSSHEvents: vi.fn(),
  testSSHConnection: vi.fn(),
  reconnectSSH: vi.fn(),
  fetchSSHFingerprint: vi.fn(),
  fetchGlobalSSHStatus: vi.fn(),
  fetchSSHMetrics: vi.fn(),
}));

import { fetchSSHStatus } from "@/api/ssh";

beforeEach(() => {
  vi.clearAllMocks();
});

describe("SSHStatus", () => {
  it("shows loading skeleton initially", () => {
    vi.mocked(fetchSSHStatus).mockReturnValue(new Promise(() => {})); // never resolves
    const { container } = renderWithProviders(<SSHStatus instanceId={1} />);
    expect(container.querySelector(".animate-pulse")).toBeInTheDocument();
  });

  it("renders connected state with health metrics", async () => {
    vi.mocked(fetchSSHStatus).mockResolvedValue(mockSSHStatus);
    renderWithProviders(<SSHStatus instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("connected")).toBeInTheDocument();
    });
    expect(screen.getByText("SSH Connection")).toBeInTheDocument();
    expect(screen.getByText("2h 0m")).toBeInTheDocument();
    expect(screen.getByText("100 / 2")).toBeInTheDocument();
  });

  it("renders tunnel badges for healthy and unhealthy tunnels", async () => {
    vi.mocked(fetchSSHStatus).mockResolvedValue(mockSSHStatus);
    renderWithProviders(<SSHStatus instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("VNC")).toBeInTheDocument();
    });
    expect(screen.getByText("Gateway")).toBeInTheDocument();
  });

  it("renders error state with retry button", async () => {
    vi.mocked(fetchSSHStatus).mockRejectedValue(new Error("Network error"));
    renderWithProviders(<SSHStatus instanceId={1} />);

    await waitFor(() => {
      expect(
        screen.getByText("Failed to load SSH connection status."),
      ).toBeInTheDocument();
    });
    expect(screen.getByText("Retry")).toBeInTheDocument();
  });

  it("renders reconnecting state with yellow indicator", async () => {
    vi.mocked(fetchSSHStatus).mockResolvedValue({
      ...mockSSHStatus,
      connection_state: "reconnecting",
    });
    renderWithProviders(<SSHStatus instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("reconnecting")).toBeInTheDocument();
    });
  });

  it("renders failed state with red indicator", async () => {
    vi.mocked(fetchSSHStatus).mockResolvedValue({
      ...mockSSHStatus,
      connection_state: "failed",
    });
    renderWithProviders(<SSHStatus instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("failed")).toBeInTheDocument();
    });
  });

  it("renders disconnected state when no health data", async () => {
    vi.mocked(fetchSSHStatus).mockResolvedValue({
      connection_state: "disconnected",
      health: null,
      tunnels: [],
      recent_events: [],
    });
    renderWithProviders(<SSHStatus instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("disconnected")).toBeInTheDocument();
    });
    // No health metrics section when health is null
    expect(screen.queryByText("Uptime")).not.toBeInTheDocument();
  });

  it("refresh button triggers query invalidation", async () => {
    vi.mocked(fetchSSHStatus).mockResolvedValue(mockSSHStatus);
    const user = userEvent.setup();
    renderWithProviders(<SSHStatus instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("connected")).toBeInTheDocument();
    });

    const refreshBtn = screen.getByTitle("Refresh SSH status");
    await user.click(refreshBtn);
    // fetchSSHStatus should be called again after invalidation
    expect(fetchSSHStatus).toHaveBeenCalledTimes(2);
  });

  it("does not fetch when enabled is false", () => {
    renderWithProviders(<SSHStatus instanceId={1} enabled={false} />);
    expect(fetchSSHStatus).not.toHaveBeenCalled();
  });

  it("renders no tunnels section when tunnels array is empty", async () => {
    vi.mocked(fetchSSHStatus).mockResolvedValue({
      ...mockSSHStatus,
      tunnels: [],
    });
    renderWithProviders(<SSHStatus instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("connected")).toBeInTheDocument();
    });
    expect(screen.queryByText("Active Tunnels")).not.toBeInTheDocument();
  });

  it("formats short uptime correctly (seconds)", async () => {
    vi.mocked(fetchSSHStatus).mockResolvedValue({
      ...mockSSHStatus,
      health: { ...mockSSHStatus.health!, uptime_seconds: 45 },
    });
    renderWithProviders(<SSHStatus instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("45s")).toBeInTheDocument();
    });
  });

  it("formats day-level uptime correctly", async () => {
    vi.mocked(fetchSSHStatus).mockResolvedValue({
      ...mockSSHStatus,
      health: { ...mockSSHStatus.health!, uptime_seconds: 90000 }, // 1d 1h
    });
    renderWithProviders(<SSHStatus instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("1d 1h")).toBeInTheDocument();
    });
  });
});
