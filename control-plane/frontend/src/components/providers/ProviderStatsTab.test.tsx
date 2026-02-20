import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import ProviderStatsTab from "./ProviderStatsTab";

// Mock recharts to avoid SVG rendering issues in tests
vi.mock("recharts", () => ({
  BarChart: ({ children }: { children: React.ReactNode }) => <div data-testid="mock-bar-chart">{children}</div>,
  Bar: () => <div />,
  XAxis: () => <div />,
  YAxis: () => <div />,
  Tooltip: () => <div />,
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Cell: () => <div />,
}));

// Mock the API
vi.mock("@/api/settings", () => ({
  fetchProviderAnalytics: vi.fn(),
}));

import { fetchProviderAnalytics } from "@/api/settings";
const mockFetchAnalytics = vi.mocked(fetchProviderAnalytics);

const defaultProps = {
  providerId: "openai",
  providerName: "OpenAI",
  brandColor: "#10A37F",
};

describe("ProviderStatsTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows loading state initially", () => {
    mockFetchAnalytics.mockReturnValue(new Promise(() => {})); // never resolves
    render(<ProviderStatsTab {...defaultProps} />);
    expect(screen.getByTestId("stats-loading")).toBeInTheDocument();
    expect(screen.getByText("Loading stats...")).toBeInTheDocument();
  });

  it("shows empty state when no data for provider", async () => {
    mockFetchAnalytics.mockResolvedValue({
      providers: {},
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });
    render(<ProviderStatsTab {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId("stats-empty")).toBeInTheDocument();
    });
    expect(screen.getByText("No usage data for OpenAI")).toBeInTheDocument();
  });

  it("shows empty state when total_requests is 0", async () => {
    mockFetchAnalytics.mockResolvedValue({
      providers: {
        openai: {
          provider: "openai",
          total_requests: 0,
          error_count: 0,
          error_rate: 0,
          avg_latency: 0,
        },
      },
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });
    render(<ProviderStatsTab {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId("stats-empty")).toBeInTheDocument();
    });
  });

  it("shows stats content when data is available", async () => {
    mockFetchAnalytics.mockResolvedValue({
      providers: {
        openai: {
          provider: "openai",
          total_requests: 50,
          error_count: 5,
          error_rate: 0.1,
          avg_latency: 250.5,
        },
      },
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });
    render(<ProviderStatsTab {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId("stats-content")).toBeInTheDocument();
    });

    expect(screen.getByTestId("stats-total-requests")).toHaveTextContent("50");
    expect(screen.getByTestId("stats-error-rate")).toHaveTextContent("10.0%");
    expect(screen.getByTestId("stats-avg-latency")).toHaveTextContent("251ms");
    expect(screen.getByTestId("stats-error-count")).toHaveTextContent("5");
    expect(screen.getByTestId("stats-chart")).toBeInTheDocument();
  });

  it("shows last error when present", async () => {
    mockFetchAnalytics.mockResolvedValue({
      providers: {
        openai: {
          provider: "openai",
          total_requests: 10,
          error_count: 2,
          error_rate: 0.2,
          avg_latency: 100,
          last_error: "Invalid API key: rejected",
          last_error_at: "2026-02-19T12:00:00Z",
        },
      },
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });
    render(<ProviderStatsTab {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId("stats-last-error")).toBeInTheDocument();
    });
    expect(screen.getByText("Invalid API key: rejected")).toBeInTheDocument();
  });

  it("does not show last error section when no errors", async () => {
    mockFetchAnalytics.mockResolvedValue({
      providers: {
        openai: {
          provider: "openai",
          total_requests: 10,
          error_count: 0,
          error_rate: 0,
          avg_latency: 100,
        },
      },
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });
    render(<ProviderStatsTab {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId("stats-content")).toBeInTheDocument();
    });
    expect(screen.queryByTestId("stats-last-error")).not.toBeInTheDocument();
  });

  it("shows error state when fetch fails", async () => {
    mockFetchAnalytics.mockRejectedValue(new Error("Network error"));
    render(<ProviderStatsTab {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId("stats-error")).toBeInTheDocument();
    });
    expect(screen.getByText("Failed to load analytics data")).toBeInTheDocument();
  });

  it("applies red color for high error rate", async () => {
    mockFetchAnalytics.mockResolvedValue({
      providers: {
        openai: {
          provider: "openai",
          total_requests: 10,
          error_count: 6,
          error_rate: 0.6,
          avg_latency: 100,
        },
      },
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });
    render(<ProviderStatsTab {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId("stats-error-rate")).toBeInTheDocument();
    });
    expect(screen.getByTestId("stats-error-rate")).toHaveClass("text-red-600");
  });

  it("applies yellow color for moderate error rate", async () => {
    mockFetchAnalytics.mockResolvedValue({
      providers: {
        openai: {
          provider: "openai",
          total_requests: 10,
          error_count: 3,
          error_rate: 0.3,
          avg_latency: 100,
        },
      },
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });
    render(<ProviderStatsTab {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId("stats-error-rate")).toBeInTheDocument();
    });
    expect(screen.getByTestId("stats-error-rate")).toHaveClass("text-yellow-600");
  });
});
