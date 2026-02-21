import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import SSHEventLog from "../SSHEventLog";
import { renderWithProviders } from "@/test/helpers";
import type { SSHEventsResponse } from "@/types/ssh";

const mockEvents: SSHEventsResponse = {
  events: [
    {
      instance_name: "bot-test",
      type: "connected",
      details: "SSH connection established",
      timestamp: "2026-02-21T10:00:00Z",
    },
    {
      instance_name: "bot-test",
      type: "health_check_failed",
      details: "Timeout after 10s",
      timestamp: "2026-02-21T11:00:00Z",
    },
    {
      instance_name: "bot-test",
      type: "disconnected",
      details: "Connection lost",
      timestamp: "2026-02-21T11:05:00Z",
    },
    {
      instance_name: "bot-test",
      type: "reconnecting",
      details: "Attempting reconnection",
      timestamp: "2026-02-21T11:06:00Z",
    },
    {
      instance_name: "bot-test",
      type: "reconnect_success",
      details: "Reconnected successfully",
      timestamp: "2026-02-21T11:07:00Z",
    },
  ],
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

import { fetchSSHEvents } from "@/api/ssh";

beforeEach(() => {
  vi.clearAllMocks();
});

describe("SSHEventLog", () => {
  it("shows loading skeleton initially", () => {
    vi.mocked(fetchSSHEvents).mockReturnValue(new Promise(() => {}));
    renderWithProviders(<SSHEventLog instanceId={1} />);
    expect(document.querySelector(".animate-pulse")).toBeInTheDocument();
  });

  it("renders events with correct severity colors", async () => {
    vi.mocked(fetchSSHEvents).mockResolvedValue(mockEvents);
    renderWithProviders(<SSHEventLog instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("connected")).toBeInTheDocument();
    });
    expect(screen.getByText("health check failed")).toBeInTheDocument();
    expect(screen.getByText("disconnected")).toBeInTheDocument();
    expect(screen.getByText("reconnecting")).toBeInTheDocument();
    expect(screen.getByText("reconnect success")).toBeInTheDocument();
  });

  it("shows event details", async () => {
    vi.mocked(fetchSSHEvents).mockResolvedValue(mockEvents);
    renderWithProviders(<SSHEventLog instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("SSH connection established")).toBeInTheDocument();
    });
    expect(screen.getByText("Timeout after 10s")).toBeInTheDocument();
    expect(screen.getByText("Connection lost")).toBeInTheDocument();
  });

  it("shows event count", async () => {
    vi.mocked(fetchSSHEvents).mockResolvedValue(mockEvents);
    renderWithProviders(<SSHEventLog instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("5 events")).toBeInTheDocument();
    });
  });

  it("singular event count for single event", async () => {
    vi.mocked(fetchSSHEvents).mockResolvedValue({
      events: [mockEvents.events[0]!],
    });
    renderWithProviders(<SSHEventLog instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("1 event")).toBeInTheDocument();
    });
  });

  it("shows error state with retry button", async () => {
    vi.mocked(fetchSSHEvents).mockRejectedValue(new Error("Failed"));
    renderWithProviders(<SSHEventLog instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("Failed to load SSH events.")).toBeInTheDocument();
    });
    expect(screen.getByText("Retry")).toBeInTheDocument();
  });

  it("shows empty state when no events", async () => {
    vi.mocked(fetchSSHEvents).mockResolvedValue({ events: [] });
    renderWithProviders(<SSHEventLog instanceId={1} />);

    await waitFor(() => {
      expect(
        screen.getByText("No connection events recorded."),
      ).toBeInTheDocument();
    });
  });

  it("opens and uses filter dropdown", async () => {
    vi.mocked(fetchSSHEvents).mockResolvedValue(mockEvents);
    const user = userEvent.setup();
    renderWithProviders(<SSHEventLog instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("5 events")).toBeInTheDocument();
    });

    // Open filter dropdown
    await user.click(screen.getByTitle("Filter events"));
    expect(screen.getByText("All events")).toBeInTheDocument();

    // Filter by connected events - click the one in the dropdown (inside the filter menu)
    const filterOptions = screen.getAllByText("connected");
    // The dropdown option is the one inside the filter panel
    const dropdownOption = filterOptions.find(
      (el) => el.closest(".absolute") !== null,
    );
    await user.click(dropdownOption ?? filterOptions[0]!);
    await waitFor(() => {
      expect(screen.getByText("1 event")).toBeInTheDocument();
    });
  });

  it("refresh button triggers refetch", async () => {
    vi.mocked(fetchSSHEvents).mockResolvedValue(mockEvents);
    const user = userEvent.setup();
    renderWithProviders(<SSHEventLog instanceId={1} />);

    await waitFor(() => {
      expect(screen.getByText("5 events")).toBeInTheDocument();
    });

    await user.click(screen.getByTitle("Refresh events"));
    expect(fetchSSHEvents).toHaveBeenCalledTimes(2);
  });

  it("does not fetch when disabled", () => {
    renderWithProviders(<SSHEventLog instanceId={1} enabled={false} />);
    expect(fetchSSHEvents).not.toHaveBeenCalled();
  });
});
