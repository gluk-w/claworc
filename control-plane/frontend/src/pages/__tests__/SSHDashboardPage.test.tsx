import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import SSHDashboardPage from "../SSHDashboardPage";
import { renderWithProviders } from "@/test/helpers";
import type { GlobalSSHStatusResponse } from "@/types/ssh";

// Mock recharts to avoid SVG rendering issues in jsdom
vi.mock("recharts", async (importOriginal) => {
  const mod = await importOriginal<typeof import("recharts")>();
  return {
    ...mod,
    ResponsiveContainer: ({ children }: { children: React.ReactNode }) => (
      <div data-testid="responsive-container">{children}</div>
    ),
  };
});

const mockGlobalStatus: GlobalSSHStatusResponse = {
  instances: [
    {
      instance_id: 1,
      instance_name: "bot-alpha",
      display_name: "Alpha Instance",
      instance_status: "running",
      connection_state: "connected",
      health: {
        connected_at: "2026-02-21T10:00:00Z",
        last_health_check: "2026-02-21T12:00:00Z",
        uptime_seconds: 7200,
        successful_checks: 100,
        failed_checks: 2,
        healthy: true,
      },
      tunnel_count: 2,
      healthy_tunnels: 2,
    },
    {
      instance_id: 2,
      instance_name: "bot-beta",
      display_name: "Beta Instance",
      instance_status: "running",
      connection_state: "failed",
      health: {
        connected_at: "2026-02-21T09:00:00Z",
        last_health_check: "2026-02-21T11:00:00Z",
        uptime_seconds: 0,
        successful_checks: 50,
        failed_checks: 30,
        healthy: false,
      },
      tunnel_count: 2,
      healthy_tunnels: 0,
    },
    {
      instance_id: 3,
      instance_name: "bot-gamma",
      display_name: "Gamma Instance",
      instance_status: "stopped",
      connection_state: "disconnected",
      health: null,
      tunnel_count: 0,
      healthy_tunnels: 0,
    },
  ],
  total_count: 3,
  connected: 1,
  reconnecting: 0,
  failed: 1,
  disconnected: 1,
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

import { fetchGlobalSSHStatus, fetchSSHMetrics } from "@/api/ssh";

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(fetchSSHMetrics).mockResolvedValue({
    uptime_buckets: [],
    health_rates: [],
    reconnection_counts: [],
  });
});

describe("SSHDashboardPage", () => {
  it("shows loading state initially", () => {
    vi.mocked(fetchGlobalSSHStatus).mockReturnValue(new Promise(() => {}));
    renderWithProviders(<SSHDashboardPage />);
    expect(screen.getByText("Loading SSH status...")).toBeInTheDocument();
  });

  it("shows error state on failure", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockRejectedValue(new Error("Failed"));
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      expect(
        screen.getByText("Failed to load SSH status. Please try again."),
      ).toBeInTheDocument();
    });
  });

  it("renders dashboard header", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue(mockGlobalStatus);
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      expect(
        screen.getByText("SSH Connection Dashboard"),
      ).toBeInTheDocument();
    });
  });

  it("renders stat cards with correct counts", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue(mockGlobalStatus);
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      // "Connected" appears in stat card and filter button
      expect(screen.getAllByText("Connected").length).toBeGreaterThanOrEqual(1);
    });
    expect(screen.getAllByText("Reconnecting").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Failed").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Disconnected").length).toBeGreaterThanOrEqual(1);
  });

  it("renders instance table with all instances", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue(mockGlobalStatus);
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("Alpha Instance")).toBeInTheDocument();
    });
    expect(screen.getByText("Beta Instance")).toBeInTheDocument();
    expect(screen.getByText("Gamma Instance")).toBeInTheDocument();
  });

  it("renders instance links to detail page", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue(mockGlobalStatus);
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      const link = screen.getByText("Alpha Instance");
      expect(link.closest("a")).toHaveAttribute("href", "/instances/1");
    });
  });

  it("renders connection state badges", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue(mockGlobalStatus);
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("connected")).toBeInTheDocument();
    });
    expect(screen.getByText("failed")).toBeInTheDocument();
    expect(screen.getByText("disconnected")).toBeInTheDocument();
  });

  it("renders health labels", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue(mockGlobalStatus);
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("Healthy")).toBeInTheDocument();
    });
    expect(screen.getByText("Unhealthy")).toBeInTheDocument();
  });

  it("renders tunnel counts", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue(mockGlobalStatus);
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("2/2")).toBeInTheDocument();
    });
    expect(screen.getByText("0/2")).toBeInTheDocument();
  });

  it("shows filter buttons", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue(mockGlobalStatus);
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("All")).toBeInTheDocument();
    });
  });

  it("shows empty state when no instances", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue({
      instances: [],
      total_count: 0,
      connected: 0,
      reconnecting: 0,
      failed: 0,
      disconnected: 0,
    });
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("No instances found.")).toBeInTheDocument();
    });
  });

  it("filters instances by connection state", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue(mockGlobalStatus);
    const user = userEvent.setup();
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("Alpha Instance")).toBeInTheDocument();
    });

    // Find the filter section and click "Disconnected" filter button
    const filterButtons = screen.getAllByRole("button");
    const disconnectedButton = filterButtons.find(
      (btn) => btn.textContent?.includes("Disconnected") && btn.textContent?.includes("(1)"),
    );
    expect(disconnectedButton).toBeDefined();
    await user.click(disconnectedButton!);

    await waitFor(() => {
      expect(screen.queryByText("Alpha Instance")).not.toBeInTheDocument();
    });
    expect(screen.getByText("Gamma Instance")).toBeInTheDocument();
    expect(screen.queryByText("Beta Instance")).not.toBeInTheDocument();
  });

  it("sorts by display name descending", async () => {
    vi.mocked(fetchGlobalSSHStatus).mockResolvedValue(mockGlobalStatus);
    const user = userEvent.setup();
    renderWithProviders(<SSHDashboardPage />);

    await waitFor(() => {
      expect(screen.getByText("Alpha Instance")).toBeInTheDocument();
    });

    // Click "Instance" column header to toggle to desc sort
    const instanceHeaders = screen.getAllByText("Instance");
    const sortableHeader = instanceHeaders.find((el) => el.closest("th"));
    expect(sortableHeader).toBeDefined();
    await user.click(sortableHeader!);

    // Now the order should be reversed (Gamma first)
    const links = screen.getAllByRole("link");
    const instanceLinks = links.filter((l) =>
      l.getAttribute("href")?.startsWith("/instances/"),
    );
    expect(instanceLinks[0]).toHaveTextContent("Gamma Instance");
  });
});
