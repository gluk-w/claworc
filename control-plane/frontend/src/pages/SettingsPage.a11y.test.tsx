import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import SettingsPage from "./SettingsPage";
import type { Settings } from "@/types/settings";

// ── Mocks ──────────────────────────────────────────────────────────────

const mockFetchSettings = vi.fn();
const mockUpdateSettings = vi.fn();

vi.mock("@/api/settings", () => ({
  fetchSettings: (...args: unknown[]) => mockFetchSettings(...args),
  updateSettings: (...args: unknown[]) => mockUpdateSettings(...args),
}));

vi.mock("react-hot-toast", () => ({
  default: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

const mockSettings: Settings = {
  brave_api_key: "",
  api_keys: {},
  default_models: [],
  default_container_image: "ghcr.io/example/agent:latest",
  default_vnc_resolution: "1920x1080",
  default_cpu_request: "250m",
  default_cpu_limit: "500m",
  default_memory_request: "256Mi",
  default_memory_limit: "512Mi",
  default_storage_homebrew: "1Gi",
  default_storage_clawd: "1Gi",
  default_storage_chrome: "1Gi",
};

// ── Helpers ────────────────────────────────────────────────────────────

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/settings"]}>
        <SettingsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

// ── Tests ──────────────────────────────────────────────────────────────

describe("SettingsPage – tab accessibility", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchSettings.mockResolvedValue(mockSettings);
    mockUpdateSettings.mockResolvedValue(mockSettings);
  });

  it("renders tabs with role=tablist", async () => {
    renderPage();
    await screen.findByText("LLM Providers");

    const tablist = screen.getByRole("tablist", { name: "Settings tabs" });
    expect(tablist).toBeInTheDocument();
  });

  it("renders each tab button with role=tab", async () => {
    renderPage();
    await screen.findByText("LLM Providers");

    const tabs = screen.getAllByRole("tab");
    expect(tabs).toHaveLength(3);
    expect(tabs[0]).toHaveTextContent("LLM Providers");
    expect(tabs[1]).toHaveTextContent("Resource Limits");
    expect(tabs[2]).toHaveTextContent("Agent Image");
  });

  it("sets aria-selected=true on the active tab", async () => {
    renderPage();
    await screen.findByText("LLM Providers");

    const tabs = screen.getAllByRole("tab");
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
    expect(tabs[1]).toHaveAttribute("aria-selected", "false");
    expect(tabs[2]).toHaveAttribute("aria-selected", "false");
  });

  it("updates aria-selected when switching tabs", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("LLM Providers");

    await user.click(screen.getByRole("tab", { name: "Resource Limits" }));

    const tabs = screen.getAllByRole("tab");
    expect(tabs[0]).toHaveAttribute("aria-selected", "false");
    expect(tabs[1]).toHaveAttribute("aria-selected", "true");
    expect(tabs[2]).toHaveAttribute("aria-selected", "false");
  });

  it("only active tab has tabIndex=0, others have tabIndex=-1", async () => {
    renderPage();
    await screen.findByText("LLM Providers");

    const tabs = screen.getAllByRole("tab");
    expect(tabs[0]).toHaveAttribute("tabindex", "0");
    expect(tabs[1]).toHaveAttribute("tabindex", "-1");
    expect(tabs[2]).toHaveAttribute("tabindex", "-1");
  });

  it("renders tab panels with role=tabpanel", async () => {
    renderPage();
    await screen.findByText("LLM Providers");

    const panel = screen.getByRole("tabpanel");
    expect(panel).toBeInTheDocument();
  });

  it("links tabs to panels via aria-controls", async () => {
    renderPage();
    await screen.findByText("LLM Providers");

    const tab = screen.getByRole("tab", { name: "LLM Providers" });
    expect(tab).toHaveAttribute("aria-controls", "tabpanel-providers");

    const panel = screen.getByRole("tabpanel");
    expect(panel).toHaveAttribute("id", "tabpanel-providers");
    expect(panel).toHaveAttribute("aria-labelledby", "tab-providers");
  });

  it("navigates to next tab with ArrowRight", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("LLM Providers");

    // Focus the first tab
    const firstTab = screen.getByRole("tab", { name: "LLM Providers" });
    firstTab.focus();

    await user.keyboard("{ArrowRight}");

    // Resource Limits should now be active
    const tabs = screen.getAllByRole("tab");
    expect(tabs[1]).toHaveAttribute("aria-selected", "true");
    expect(tabs[1]).toHaveFocus();
  });

  it("navigates to previous tab with ArrowLeft", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("LLM Providers");

    // Click Resource Limits first
    await user.click(screen.getByRole("tab", { name: "Resource Limits" }));
    const secondTab = screen.getByRole("tab", { name: "Resource Limits" });
    secondTab.focus();

    await user.keyboard("{ArrowLeft}");

    const tabs = screen.getAllByRole("tab");
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
    expect(tabs[0]).toHaveFocus();
  });

  it("wraps from last tab to first on ArrowRight", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("LLM Providers");

    // Click Agent Image (last tab)
    await user.click(screen.getByRole("tab", { name: "Agent Image" }));
    screen.getByRole("tab", { name: "Agent Image" }).focus();

    await user.keyboard("{ArrowRight}");

    const tabs = screen.getAllByRole("tab");
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
    expect(tabs[0]).toHaveFocus();
  });

  it("wraps from first tab to last on ArrowLeft", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("LLM Providers");

    const firstTab = screen.getByRole("tab", { name: "LLM Providers" });
    firstTab.focus();

    await user.keyboard("{ArrowLeft}");

    const tabs = screen.getAllByRole("tab");
    expect(tabs[2]).toHaveAttribute("aria-selected", "true");
    expect(tabs[2]).toHaveFocus();
  });

  it("Home key moves to first tab", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("LLM Providers");

    // Go to last tab
    await user.click(screen.getByRole("tab", { name: "Agent Image" }));
    screen.getByRole("tab", { name: "Agent Image" }).focus();

    await user.keyboard("{Home}");

    const tabs = screen.getAllByRole("tab");
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
    expect(tabs[0]).toHaveFocus();
  });

  it("End key moves to last tab", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("LLM Providers");

    const firstTab = screen.getByRole("tab", { name: "LLM Providers" });
    firstTab.focus();

    await user.keyboard("{End}");

    const tabs = screen.getAllByRole("tab");
    expect(tabs[2]).toHaveAttribute("aria-selected", "true");
    expect(tabs[2]).toHaveFocus();
  });

  it("all tab buttons have focus:ring classes", async () => {
    renderPage();
    await screen.findByText("LLM Providers");

    const tabs = screen.getAllByRole("tab");
    for (const tab of tabs) {
      expect(tab.className).toContain("focus:ring-2");
      expect(tab.className).toContain("focus:ring-blue-500");
    }
  });
});
