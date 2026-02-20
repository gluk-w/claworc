import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ProviderConfigModal from "./ProviderConfigModal";
import type { Provider } from "./providerData";

// Mock recharts
vi.mock("recharts", () => ({
  BarChart: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Bar: () => <div />,
  XAxis: () => <div />,
  YAxis: () => <div />,
  Tooltip: () => <div />,
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Cell: () => <div />,
}));

// Mock the analytics API
vi.mock("@/api/settings", () => ({
  testProviderKey: vi.fn(),
  fetchProviderAnalytics: vi.fn().mockResolvedValue({
    providers: {
      openai: {
        provider: "openai",
        total_requests: 10,
        error_count: 1,
        error_rate: 0.1,
        avg_latency: 200,
      },
    },
    period_days: 7,
    since: "2026-02-13T00:00:00Z",
  }),
}));

// Mock react-hot-toast
vi.mock("react-hot-toast", () => ({
  default: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

const testProvider: Provider = {
  id: "openai",
  name: "OpenAI",
  envVarName: "OPENAI_API_KEY",
  category: "Major Providers",
  description: "Access GPT and o-series models.",
  docsUrl: "https://platform.openai.com/api-keys",
  supportsBaseUrl: true,
  brandColor: "#10A37F",
};

function renderModal(props: Partial<React.ComponentProps<typeof ProviderConfigModal>> = {}) {
  const defaultProps = {
    provider: testProvider,
    isOpen: true,
    onClose: vi.fn(),
    onSave: vi.fn(),
    currentMaskedKey: null,
  };
  return render(<ProviderConfigModal {...defaultProps} {...props} />);
}

describe("ProviderConfigModal – tabs", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders Configure and Stats tabs", () => {
    renderModal();
    expect(screen.getByTestId("tab-configure")).toBeInTheDocument();
    expect(screen.getByTestId("tab-stats")).toBeInTheDocument();
  });

  it("shows Configure tab by default", () => {
    renderModal();
    const configureTab = screen.getByTestId("tab-configure");
    expect(configureTab).toHaveAttribute("aria-selected", "true");
    expect(screen.getByLabelText("API Key")).toBeInTheDocument();
  });

  it("switches to Stats tab when clicked", async () => {
    const user = userEvent.setup();
    renderModal();

    await user.click(screen.getByTestId("tab-stats"));

    const statsTab = screen.getByTestId("tab-stats");
    expect(statsTab).toHaveAttribute("aria-selected", "true");
    // API key input should no longer be visible
    expect(screen.queryByLabelText("API Key")).not.toBeInTheDocument();
  });

  it("switches back to Configure tab", async () => {
    const user = userEvent.setup();
    renderModal();

    await user.click(screen.getByTestId("tab-stats"));
    await user.click(screen.getByTestId("tab-configure"));

    expect(screen.getByTestId("tab-configure")).toHaveAttribute("aria-selected", "true");
    expect(screen.getByLabelText("API Key")).toBeInTheDocument();
  });

  it("updates the modal title when switching tabs", async () => {
    const user = userEvent.setup();
    renderModal();

    expect(screen.getByText("Configure OpenAI")).toBeInTheDocument();

    await user.click(screen.getByTestId("tab-stats"));
    expect(screen.getByText("Stats OpenAI")).toBeInTheDocument();
  });

  it("has proper ARIA attributes on tabs", () => {
    renderModal();
    const tablist = screen.getByRole("tablist");
    expect(tablist).toBeInTheDocument();

    const configureTab = screen.getByTestId("tab-configure");
    expect(configureTab).toHaveAttribute("role", "tab");
    expect(configureTab).toHaveAttribute("aria-controls", "tab-panel-configure");

    const statsTab = screen.getByTestId("tab-stats");
    expect(statsTab).toHaveAttribute("role", "tab");
    expect(statsTab).toHaveAttribute("aria-controls", "tab-panel-stats");
  });

  it("resets to Configure tab when modal is closed and reopened", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const { rerender } = renderModal({ onClose });

    // Switch to Stats tab
    await user.click(screen.getByTestId("tab-stats"));
    expect(screen.getByTestId("tab-stats")).toHaveAttribute("aria-selected", "true");

    // Close the modal
    await user.click(screen.getByLabelText("Close dialog"));
    expect(onClose).toHaveBeenCalled();

    // Reopen the modal
    rerender(
      <ProviderConfigModal
        provider={testProvider}
        isOpen={true}
        onClose={onClose}
        onSave={vi.fn()}
        currentMaskedKey={null}
      />,
    );

    // Should be back on Configure tab
    expect(screen.getByTestId("tab-configure")).toHaveAttribute("aria-selected", "true");
  });
});
