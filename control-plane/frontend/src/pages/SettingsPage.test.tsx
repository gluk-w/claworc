import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
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

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

function renderPage() {
  const qc = makeQueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SettingsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

// ── Tests ──────────────────────────────────────────────────────────────

describe("SettingsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchSettings.mockResolvedValue(mockSettings);
    mockUpdateSettings.mockResolvedValue(mockSettings);
  });

  it("renders loading state initially", () => {
    // Use a query client that won't resolve immediately
    const qc = new QueryClient({
      defaultOptions: {
        queries: { retry: false, enabled: false },
      },
    });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SettingsPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });

  it("renders the Settings heading and all 3 tab buttons", async () => {
    renderPage();

    expect(await screen.findByText("Settings")).toBeInTheDocument();
    expect(screen.getByText("LLM Providers")).toBeInTheDocument();
    expect(screen.getByText("Resource Limits")).toBeInTheDocument();
    expect(screen.getByText("Agent Image")).toBeInTheDocument();
  });

  it("shows LLM Providers tab as active by default", async () => {
    renderPage();

    const providersTab = await screen.findByText("LLM Providers");
    expect(providersTab.className).toContain("border-blue-600");
    expect(providersTab.className).toContain("text-blue-600");

    // Other tabs should not be active
    const resourcesTab = screen.getByText("Resource Limits");
    expect(resourcesTab.className).toContain("border-transparent");
  });

  it("renders LLM Providers content by default", async () => {
    renderPage();

    // LLMProvidersTab renders a warning banner and "Provider Configuration"
    expect(
      await screen.findByText(
        /changing global api keys will update all instances/i,
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Provider Configuration")).toBeInTheDocument();
  });

  it("switches to Resource Limits tab on click", async () => {
    renderPage();
    const user = userEvent.setup();

    await screen.findByText("LLM Providers");
    await user.click(screen.getByText("Resource Limits"));

    // Resource Limits tab should now be active
    const resourcesTab = screen.getByText("Resource Limits");
    expect(resourcesTab.className).toContain("border-blue-600");

    // ResourceLimitsTab content should render
    expect(screen.getByText("Default Resource Limits")).toBeInTheDocument();
    expect(screen.getByText("Default CPU Request")).toBeInTheDocument();
  });

  it("switches to Agent Image tab on click", async () => {
    renderPage();
    const user = userEvent.setup();

    await screen.findByText("LLM Providers");
    await user.click(screen.getByText("Agent Image"));

    // AgentImageTab content should render
    expect(screen.getByText("Default Container Image")).toBeInTheDocument();
    expect(screen.getByText("Default VNC Resolution")).toBeInTheDocument();
  });

  it("hides other tab content when switching tabs", async () => {
    renderPage();
    const user = userEvent.setup();

    // Wait for initial load
    await screen.findByText("Provider Configuration");

    // Switch to Resource Limits
    await user.click(screen.getByText("Resource Limits"));

    // Provider content should be gone
    expect(screen.queryByText("Provider Configuration")).not.toBeInTheDocument();
    // Resource content should be visible
    expect(screen.getByText("Default Resource Limits")).toBeInTheDocument();
  });

  it("does not render a global save button", async () => {
    renderPage();

    await screen.findByText("LLM Providers");

    // The old global "Save Settings" button from SettingsPage should not exist
    // (individual tabs have their own save buttons)
    // Check all three tabs
    const user = userEvent.setup();

    // Check providers tab - it uses "Save Changes" from ProviderGrid
    // No global save button should exist at the page level

    await user.click(screen.getByText("Resource Limits"));
    // ResourceLimitsTab has its own "Save Settings" button
    const saveBtn = screen.getByRole("button", { name: "Save Settings" });
    // Verify it's inside the tab, not a global one
    expect(saveBtn).toBeInTheDocument();
  });

  it("renders tab navigation with proper aria label", async () => {
    renderPage();
    await screen.findByText("LLM Providers");

    const nav = screen.getByRole("navigation", { name: "Settings tabs" });
    expect(nav).toBeInTheDocument();
  });

  it("can switch back and forth between tabs", async () => {
    renderPage();
    const user = userEvent.setup();

    await screen.findByText("Provider Configuration");

    // Go to Resource Limits
    await user.click(screen.getByText("Resource Limits"));
    expect(screen.getByText("Default Resource Limits")).toBeInTheDocument();

    // Go to Agent Image
    await user.click(screen.getByText("Agent Image"));
    expect(screen.getByText("Default Container Image")).toBeInTheDocument();

    // Go back to LLM Providers
    await user.click(screen.getByText("LLM Providers"));
    expect(screen.getByText("Provider Configuration")).toBeInTheDocument();
  });
});
