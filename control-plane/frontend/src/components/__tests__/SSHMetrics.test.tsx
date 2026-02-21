import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import SSHMetrics from "../SSHMetrics";
import { renderWithProviders } from "@/test/helpers";
import type { SSHMetricsResponse } from "@/types/ssh";

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

const mockMetrics: SSHMetricsResponse = {
  uptime_buckets: [
    { label: "<1h", count: 2 },
    { label: "1-6h", count: 3 },
    { label: "6-24h", count: 1 },
    { label: "1-7d", count: 0 },
    { label: ">7d", count: 1 },
  ],
  health_rates: [
    {
      instance_name: "bot-alpha",
      display_name: "Alpha",
      success_rate: 0.98,
      total_checks: 100,
    },
    {
      instance_name: "bot-beta",
      display_name: "Beta",
      success_rate: 0.75,
      total_checks: 50,
    },
  ],
  reconnection_counts: [
    {
      instance_name: "bot-alpha",
      display_name: "Alpha",
      count: 3,
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

import { fetchSSHMetrics } from "@/api/ssh";

beforeEach(() => {
  vi.clearAllMocks();
});

describe("SSHMetrics", () => {
  it("returns null while loading", () => {
    vi.mocked(fetchSSHMetrics).mockReturnValue(new Promise(() => {}));
    const { container } = renderWithProviders(<SSHMetrics />);
    expect(container.firstChild).toBeNull();
  });

  it("returns null when no data has any values", async () => {
    vi.mocked(fetchSSHMetrics).mockResolvedValue({
      uptime_buckets: [
        { label: "<1h", count: 0 },
        { label: "1-6h", count: 0 },
      ],
      health_rates: [],
      reconnection_counts: [],
    });
    const { container } = renderWithProviders(<SSHMetrics />);
    await waitFor(() => {
      expect(container.firstChild).toBeNull();
    });
  });

  it("renders collapsed header with metrics title", async () => {
    vi.mocked(fetchSSHMetrics).mockResolvedValue(mockMetrics);
    renderWithProviders(<SSHMetrics />);

    await waitFor(() => {
      expect(screen.getByText("Connection Metrics")).toBeInTheDocument();
    });
  });

  it("expands to show charts on click", async () => {
    vi.mocked(fetchSSHMetrics).mockResolvedValue(mockMetrics);
    const user = userEvent.setup();
    renderWithProviders(<SSHMetrics />);

    await waitFor(() => {
      expect(screen.getByText("Connection Metrics")).toBeInTheDocument();
    });

    await user.click(screen.getByText("Connection Metrics"));

    expect(
      screen.getByText("Connection Uptime Distribution"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Health Check Success Rate"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Reconnection Attempts"),
    ).toBeInTheDocument();
  });

  it("collapses charts on second click", async () => {
    vi.mocked(fetchSSHMetrics).mockResolvedValue(mockMetrics);
    const user = userEvent.setup();
    renderWithProviders(<SSHMetrics />);

    await waitFor(() => {
      expect(screen.getByText("Connection Metrics")).toBeInTheDocument();
    });

    // Expand
    await user.click(screen.getByText("Connection Metrics"));
    expect(
      screen.getByText("Connection Uptime Distribution"),
    ).toBeInTheDocument();

    // Collapse
    await user.click(screen.getByText("Connection Metrics"));
    expect(
      screen.queryByText("Connection Uptime Distribution"),
    ).not.toBeInTheDocument();
  });
});
