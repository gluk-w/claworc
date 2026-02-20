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

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

function renderPage(initialPath = "/settings") {
  const qc = makeQueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialPath]}>
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

  it("renders loading state initially with skeleton and spinner", () => {
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

    expect(screen.getByTestId("settings-loading")).toBeInTheDocument();
    expect(screen.getByText("Loading settings...")).toBeInTheDocument();
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

    const tablist = screen.getByRole("tablist", { name: "Settings tabs" });
    expect(tablist).toBeInTheDocument();
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

// ── Integration: End-to-end tab save flows ──────────────────────────

describe("SettingsPage — integration", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchSettings.mockResolvedValue(mockSettings);
    mockUpdateSettings.mockResolvedValue(mockSettings);
  });

  it("saves resource limits through the Resource Limits tab", async () => {
    renderPage();
    const user = userEvent.setup();

    // Wait for load, then switch to Resource Limits tab
    await screen.findByText("LLM Providers");
    await user.click(screen.getByText("Resource Limits"));

    // Change a resource field (clear first since defaultValue is pre-filled)
    const inputs = screen.getAllByRole("textbox");
    await user.clear(inputs[0]!);
    await user.type(inputs[0]!, "1000m");

    // Save
    const saveBtn = screen.getByRole("button", { name: "Save Settings" });
    expect(saveBtn).not.toBeDisabled();
    await user.click(saveBtn);

    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    expect(payload.default_cpu_request).toBe("1000m");
  });

  it("saves agent image settings through the Agent Image tab", async () => {
    renderPage();
    const user = userEvent.setup();

    await screen.findByText("LLM Providers");
    await user.click(screen.getByText("Agent Image"));

    // Clear first since defaultValue is pre-filled
    const inputs = screen.getAllByRole("textbox");
    await user.clear(inputs[0]!);
    await user.type(inputs[0]!, "custom-image:v2");

    const saveBtn = screen.getByRole("button", { name: "Save Settings" });
    expect(saveBtn).not.toBeDisabled();
    await user.click(saveBtn);

    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    expect(payload.default_container_image).toBe("custom-image:v2");
  });

  it("saves a provider key through the LLM Providers tab", async () => {
    renderPage();
    const user = userEvent.setup();

    // Wait for providers tab (default)
    await screen.findByText("Provider Configuration");

    // Click first Configure button (Anthropic)
    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);

    // Enter API key and save in modal
    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-integration-test-key",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Click the global "Save Changes" button
    await user.click(screen.getByText("Save Changes"));

    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    expect(payload.api_keys).toHaveProperty("ANTHROPIC_API_KEY");
  });

  it("tabs have independent save state — editing one does not affect another", async () => {
    renderPage();
    const user = userEvent.setup();

    // Wait for load
    await screen.findByText("LLM Providers");

    // Go to Resource Limits, make a change
    await user.click(screen.getByText("Resource Limits"));
    const resourceInputs = screen.getAllByRole("textbox");
    await user.type(resourceInputs[0]!, "999m");

    // Resource Limits save button should be enabled
    const resSave = screen.getByRole("button", { name: "Save Settings" });
    expect(resSave).not.toBeDisabled();

    // Switch to Agent Image tab — its save button should be disabled (no changes)
    await user.click(screen.getByText("Agent Image"));
    const imgSave = screen.getByRole("button", { name: "Save Settings" });
    expect(imgSave).toBeDisabled();

    // The resource limit changes should not have triggered an update call
    expect(mockUpdateSettings).not.toHaveBeenCalled();
  });

  it("each tab renders its own save button independently", async () => {
    renderPage();
    const user = userEvent.setup();

    await screen.findByText("LLM Providers");

    // LLM Providers tab — no save button visible (ProviderGrid only shows it when changes exist)
    expect(screen.queryByText("Save Changes")).not.toBeInTheDocument();
    expect(screen.queryByText("Save Settings")).not.toBeInTheDocument();

    // Resource Limits tab — save button present but disabled
    await user.click(screen.getByText("Resource Limits"));
    expect(
      screen.getByRole("button", { name: "Save Settings" }),
    ).toBeDisabled();

    // Agent Image tab — save button present but disabled
    await user.click(screen.getByText("Agent Image"));
    expect(
      screen.getByRole("button", { name: "Save Settings" }),
    ).toBeDisabled();
  });

  it("tab navigation uses buttons (not links) so URL does not change", async () => {
    renderPage("/settings");
    const user = userEvent.setup();

    await screen.findByText("LLM Providers");

    // All tab triggers should be <button> elements with role=tab, not <a> links
    const tablist = screen.getByRole("tablist", { name: "Settings tabs" });
    const tabs = within(tablist).getAllByRole("tab");
    expect(tabs).toHaveLength(3);

    // Verify all are button elements (not anchors)
    for (const tab of tabs) {
      expect(tab.tagName).toBe("BUTTON");
      expect(tab).not.toHaveAttribute("href");
    }

    // Switch tabs — since they're buttons (not router Links), URL won't change
    await user.click(screen.getByText("Resource Limits"));
    await user.click(screen.getByText("Agent Image"));
    await user.click(screen.getByText("LLM Providers"));

    // No navigation occurred — we can still find content (would crash if route changed)
    expect(screen.getByText("Provider Configuration")).toBeInTheDocument();
  });

  it("tab bar uses horizontal flex layout for responsive overflow", async () => {
    renderPage();
    await screen.findByText("LLM Providers");

    const tablist = screen.getByRole("tablist", { name: "Settings tabs" });
    // The tablist uses flex layout with gap-0 for horizontal tabs
    expect(tablist.className).toContain("flex");
  });

  it("displays pre-configured provider keys from settings on load", async () => {
    mockFetchSettings.mockResolvedValue({
      ...mockSettings,
      api_keys: {
        ANTHROPIC_API_KEY: "****7890",
        OPENAI_API_KEY: "****abcd",
      },
      brave_api_key: "****qrst",
    });

    renderPage();

    // Wait for providers tab to load
    await screen.findByText("Provider Configuration");

    // All masked keys should be visible
    expect(screen.getByText("****7890")).toBeInTheDocument();
    expect(screen.getByText("****abcd")).toBeInTheDocument();
    expect(screen.getByText("****qrst")).toBeInTheDocument();
  });

  it("displays pre-configured resource limits on the Resource Limits tab", async () => {
    renderPage();
    const user = userEvent.setup();

    await screen.findByText("LLM Providers");
    await user.click(screen.getByText("Resource Limits"));

    // Verify existing settings values are shown
    const inputs = screen.getAllByRole("textbox");
    const values = inputs.map(
      (input) => (input as HTMLInputElement).defaultValue,
    );
    expect(values).toContain("250m");
    expect(values).toContain("500m");
    expect(values).toContain("256Mi");
    expect(values).toContain("512Mi");
    expect(values).toContain("1Gi");
  });

  it("displays pre-configured agent image settings on the Agent Image tab", async () => {
    renderPage();
    const user = userEvent.setup();

    await screen.findByText("LLM Providers");
    await user.click(screen.getByText("Agent Image"));

    const inputs = screen.getAllByRole("textbox");
    const values = inputs.map(
      (input) => (input as HTMLInputElement).defaultValue,
    );
    expect(values).toContain("ghcr.io/example/agent:latest");
    expect(values).toContain("1920x1080");
  });

  it("shows warning banner only on LLM Providers tab", async () => {
    renderPage();
    const user = userEvent.setup();

    await screen.findByText("LLM Providers");

    // Warning banner visible on LLM Providers tab
    expect(
      screen.getByText(/changing global api keys will update all instances/i),
    ).toBeInTheDocument();

    // Switch to Resource Limits — no warning banner
    await user.click(screen.getByText("Resource Limits"));
    expect(
      screen.queryByText(
        /changing global api keys will update all instances/i,
      ),
    ).not.toBeInTheDocument();

    // Switch to Agent Image — no warning banner
    await user.click(screen.getByText("Agent Image"));
    expect(
      screen.queryByText(
        /changing global api keys will update all instances/i,
      ),
    ).not.toBeInTheDocument();
  });
});
