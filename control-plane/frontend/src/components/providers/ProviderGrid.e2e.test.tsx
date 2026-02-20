/**
 * End-to-end integration tests for all Phase 06 enhancements.
 *
 * These tests verify the full user interaction flows for:
 * - Connection testing with valid and invalid keys
 * - Base URL persistence
 * - Provider search and filtering
 * - Batch operations (select, delete, export, test)
 * - Usage analytics display in modal
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import ProviderGrid from "./ProviderGrid";
import type { Settings } from "@/types/settings";
import type { ProviderSavePayload } from "./ProviderGrid";
import { STORAGE_KEY } from "../ConfirmDialog";
import { HEALTH_COLORS } from "./providerHealth";

// Mock the settings API module
const mockTestProviderKey = vi.fn();
const mockFetchProviderAnalytics = vi.fn();

vi.mock("@/api/settings", () => ({
  testProviderKey: (...args: unknown[]) => mockTestProviderKey(...args),
  fetchProviderAnalytics: (...args: unknown[]) => mockFetchProviderAnalytics(...args),
}));

// Mock recharts to avoid SVG rendering issues in JSDOM
vi.mock("recharts", () => ({
  BarChart: ({ children }: { children: React.ReactNode }) => <div data-testid="mock-bar-chart">{children}</div>,
  Bar: () => <div />,
  XAxis: () => <div />,
  YAxis: () => <div />,
  Tooltip: () => <div />,
  ResponsiveContainer: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  Cell: () => <div />,
}));

// Mock react-hot-toast
vi.mock("react-hot-toast", () => ({
  default: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

// ── Helpers ────────────────────────────────────────────────────────────

const emptySettings: Settings = {
  brave_api_key: "",
  api_keys: {},
  base_urls: {},
  default_models: [],
  default_container_image: "",
  default_vnc_resolution: "",
  default_cpu_request: "",
  default_cpu_limit: "",
  default_memory_request: "",
  default_memory_limit: "",
  default_storage_homebrew: "",
  default_storage_clawd: "",
  default_storage_chrome: "",
};

const settingsWithKeys: Settings = {
  ...emptySettings,
  api_keys: {
    ANTHROPIC_API_KEY: "****7890",
    OPENAI_API_KEY: "****abcd",
    GROQ_API_KEY: "****efgh",
  },
};

const settingsWithBaseUrls: Settings = {
  ...settingsWithKeys,
  base_urls: {
    OPENAI_API_KEY: "https://my-proxy.example.com/v1",
  },
};

function renderGrid(
  settings: Settings = emptySettings,
  onSaveChanges: (payload: ProviderSavePayload) => Promise<void> = () =>
    Promise.resolve(),
  isSaving = false,
  initialEntries: string[] = ["/settings"],
) {
  return render(
    <MemoryRouter initialEntries={initialEntries}>
      <ProviderGrid
        settings={settings}
        onSaveChanges={onSaveChanges}
        isSaving={isSaving}
      />
    </MemoryRouter>,
  );
}

// ── E2E: Connection testing via ProviderConfigModal ──────────────────

describe("E2E – Connection testing with valid and invalid keys", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
    mockFetchProviderAnalytics.mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" });
  });

  it("tests a valid connection: enter key → click Test → shows success", async () => {
    mockTestProviderKey.mockResolvedValue({ success: true, message: "API key verified" });

    renderGrid();
    const user = userEvent.setup();

    // Search for OpenAI to narrow down the grid
    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");

    // Click configure on the visible OpenAI provider
    const configureBtn = screen.getByRole("button", { name: /configure/i });
    await user.click(configureBtn);

    // Modal should open
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("Configure OpenAI")).toBeInTheDocument();

    // Enter API key and test connection
    await user.type(screen.getByLabelText("API Key"), "sk-proj-valid-key12345");
    await user.click(screen.getByRole("button", { name: /test connection/i }));

    // Wait for test result
    await waitFor(() => {
      expect(screen.getByText("API key verified successfully.")).toBeInTheDocument();
    });

    expect(mockTestProviderKey).toHaveBeenCalledWith({
      provider: "openai",
      api_key: "sk-proj-valid-key12345",
      base_url: undefined,
    });
  });

  it("tests an invalid connection: enter key → click Test → shows failure", async () => {
    mockTestProviderKey.mockResolvedValue({
      success: false,
      message: "Invalid API key",
      details: "Authentication failed",
    });

    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");
    await user.click(screen.getByRole("button", { name: /configure/i }));

    await user.type(screen.getByLabelText("API Key"), "sk-proj-invalid-key");
    await user.click(screen.getByRole("button", { name: /test connection/i }));

    await waitFor(() => {
      expect(screen.getByText("Connection test failed.")).toBeInTheDocument();
    });
    expect(screen.getByText("Authentication failed")).toBeInTheDocument();
  });

  it("disables Test Connection button when API key is empty", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");
    await user.click(screen.getByRole("button", { name: /configure/i }));

    const testBtn = screen.getByRole("button", { name: /test connection/i });
    expect(testBtn).toBeDisabled();
  });

  it("tests connection with custom base URL when provider supports it", async () => {
    mockTestProviderKey.mockResolvedValue({ success: true, message: "API key verified" });

    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");
    await user.click(screen.getByRole("button", { name: /configure/i }));

    // Enter key and base URL
    await user.type(screen.getByLabelText("API Key"), "sk-proj-test-key12345");
    await user.type(screen.getByLabelText(/base url/i), "https://custom.proxy.com/v1");
    await user.click(screen.getByRole("button", { name: /test connection/i }));

    await waitFor(() => {
      expect(mockTestProviderKey).toHaveBeenCalledWith({
        provider: "openai",
        api_key: "sk-proj-test-key12345",
        base_url: "https://custom.proxy.com/v1",
      });
    });
  });

  it("shows loading state during connection test", async () => {
    // Make the test hang
    mockTestProviderKey.mockReturnValue(new Promise(() => {}));

    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");
    await user.click(screen.getByRole("button", { name: /configure/i }));

    await user.type(screen.getByLabelText("API Key"), "sk-proj-test-key12345");
    await user.click(screen.getByRole("button", { name: /test connection/i }));

    expect(screen.getByText("Testing...")).toBeInTheDocument();
  });
});

// ── E2E: Base URL persistence ────────────────────────────────────────

describe("E2E – Base URL persistence", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
    mockFetchProviderAnalytics.mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" });
  });

  it("saves custom base URL and includes it in save payload", async () => {
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );
    renderGrid(emptySettings, onSaveChanges);
    const user = userEvent.setup();

    // Open OpenAI modal
    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");
    await user.click(screen.getByRole("button", { name: /configure/i }));

    // Enter API key and base URL
    await user.type(screen.getByLabelText("API Key"), "sk-proj-test-key12345");
    await user.type(screen.getByLabelText(/base url/i), "https://my-proxy.example.com/v1");
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Click Save Changes
    await user.click(screen.getByText("Save Changes"));

    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    const payload = onSaveChanges.mock.calls[0]![0];
    expect(payload.api_keys).toEqual({ OPENAI_API_KEY: "sk-proj-test-key12345" });
    expect(payload.base_urls).toEqual({ OPENAI_API_KEY: "https://my-proxy.example.com/v1" });
  });

  it("pre-fills base URL from existing settings when modal opens", async () => {
    renderGrid(settingsWithBaseUrls);
    const user = userEvent.setup();

    // Open OpenAI modal (which has a base URL configured)
    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");
    const updateBtn = screen.getByRole("button", { name: /update/i });
    await user.click(updateBtn);

    // Base URL input should be pre-filled
    const baseUrlInput = screen.getByLabelText(/base url/i) as HTMLInputElement;
    expect(baseUrlInput.value).toBe("https://my-proxy.example.com/v1");
  });

  it("deleting an API key removes it from save payload (cascade)", async () => {
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );
    renderGrid(settingsWithBaseUrls, onSaveChanges);
    const user = userEvent.setup();

    // Search for OpenAI
    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");

    // Find the delete button
    const deleteBtn = screen.getByRole("button", { name: /remove openai api key/i });
    await user.click(deleteBtn);

    // Save changes
    await user.click(screen.getByText("Save Changes"));

    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    const payload = onSaveChanges.mock.calls[0]![0];
    expect(payload.delete_api_keys).toContain("OPENAI_API_KEY");
  });
});

// ── E2E: Provider search ─────────────────────────────────────────────

describe("E2E – Provider search: search for OpenAI", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
    mockFetchProviderAnalytics.mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" });
  });

  it("typing 'OpenAI' shows only the OpenAI card", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");

    expect(screen.getByText("OpenAI")).toBeInTheDocument();
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
    expect(screen.queryByText("Groq")).not.toBeInTheDocument();
    expect(screen.queryByText("Mistral")).not.toBeInTheDocument();
    expect(screen.queryByText("DeepSeek")).not.toBeInTheDocument();
    expect(screen.queryByText("Brave")).not.toBeInTheDocument();
  });

  it("search is case-insensitive", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "openai");
    expect(screen.getByText("OpenAI")).toBeInTheDocument();
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
  });

  it("shows only the Major Providers category when searching for OpenAI", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");

    expect(screen.getByText("Major Providers")).toBeInTheDocument();
    expect(screen.queryByText("Open Source / Inference")).not.toBeInTheDocument();
    expect(screen.queryByText("Specialized")).not.toBeInTheDocument();
    expect(screen.queryByText("Aggregators")).not.toBeInTheDocument();
    expect(screen.queryByText("Search & Tools")).not.toBeInTheDocument();
  });

  it("clearing search restores all providers", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();

    await user.click(screen.getByTestId("provider-search-clear"));

    expect(screen.getByText("Anthropic")).toBeInTheDocument();
    expect(screen.getByText("OpenAI")).toBeInTheDocument();
    expect(screen.getByText("Groq")).toBeInTheDocument();
  });
});

// ── E2E: Provider filtering ──────────────────────────────────────────

describe("E2E – Filtering: configured only", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
    mockFetchProviderAnalytics.mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" });
  });

  it("selecting 'Configured' hides unconfigured providers", async () => {
    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    await user.selectOptions(screen.getByTestId("provider-filter-select"), "configured");

    // Configured providers should be visible
    expect(screen.getByText("Anthropic")).toBeInTheDocument();
    expect(screen.getByText("OpenAI")).toBeInTheDocument();
    expect(screen.getByText("Groq")).toBeInTheDocument();

    // Unconfigured providers should be hidden
    expect(screen.queryByText("Mistral")).not.toBeInTheDocument();
    expect(screen.queryByText("DeepSeek")).not.toBeInTheDocument();
    expect(screen.queryByText("Brave")).not.toBeInTheDocument();
    expect(screen.queryByText("Together")).not.toBeInTheDocument();
    expect(screen.queryByText("Fireworks")).not.toBeInTheDocument();
  });

  it("selecting 'Not Configured' hides configured providers", async () => {
    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    await user.selectOptions(screen.getByTestId("provider-filter-select"), "not-configured");

    // Configured providers hidden
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
    expect(screen.queryByText("OpenAI")).not.toBeInTheDocument();
    expect(screen.queryByText("Groq")).not.toBeInTheDocument();

    // Unconfigured visible
    expect(screen.getByText("Mistral")).toBeInTheDocument();
    expect(screen.getByText("DeepSeek")).toBeInTheDocument();
    expect(screen.getByText("Brave")).toBeInTheDocument();
  });

  it("combined search + filter works correctly", async () => {
    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    // Filter to configured only
    await user.selectOptions(screen.getByTestId("provider-filter-select"), "configured");

    // Then search for "Anthropic"
    await user.type(screen.getByTestId("provider-search-input"), "Anthropic");

    // Only Anthropic should be visible
    expect(screen.getByText("Anthropic")).toBeInTheDocument();
    expect(screen.queryByText("OpenAI")).not.toBeInTheDocument();
    expect(screen.queryByText("Groq")).not.toBeInTheDocument();
  });
});

// ── E2E: Batch operations ────────────────────────────────────────────

describe("E2E – Batch operations: select 3 providers, delete all", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
    mockFetchProviderAnalytics.mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" });
  });

  it("selects all, deletes, and verifies save payload", async () => {
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );
    renderGrid(settingsWithKeys, onSaveChanges);
    const user = userEvent.setup();

    // Select all providers
    await user.click(screen.getByTestId("select-all-checkbox"));
    expect(screen.getByTestId("batch-action-bar")).toBeInTheDocument();

    // Click Delete Selected
    await user.click(screen.getByTestId("batch-delete-selected"));

    // Save Changes should appear (confirmation suppressed)
    await user.click(screen.getByText("Save Changes"));

    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    const payload = onSaveChanges.mock.calls[0]![0];
    expect(payload.delete_api_keys).toContain("ANTHROPIC_API_KEY");
    expect(payload.delete_api_keys).toContain("OPENAI_API_KEY");
    expect(payload.delete_api_keys).toContain("GROQ_API_KEY");
  });

  it("shows confirmation dialog when not suppressed", async () => {
    localStorage.clear(); // Don't suppress confirmation

    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));
    await user.click(screen.getByTestId("batch-delete-selected"));

    expect(screen.getByText("Delete Selected API Keys")).toBeInTheDocument();
  });

  it("batch delete with confirmation proceeds correctly", async () => {
    localStorage.clear(); // Don't suppress confirmation

    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );
    renderGrid(settingsWithKeys, onSaveChanges);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));
    await user.click(screen.getByTestId("batch-delete-selected"));

    // Confirm deletion
    await user.click(screen.getByTestId("confirm-dialog-confirm"));

    // Save Changes should appear
    await user.click(screen.getByText("Save Changes"));
    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    expect(onSaveChanges.mock.calls[0]![0].delete_api_keys).toContain("ANTHROPIC_API_KEY");
  });
});

// ── E2E: Export keys ─────────────────────────────────────────────────

describe("E2E – Export keys: select providers, verify format", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
    mockFetchProviderAnalytics.mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" });
  });

  it("exports .env file when Export Keys is clicked", async () => {
    // Track blob creation
    const createdBlobs: Blob[] = [];
    const originalCreateObjectURL = URL.createObjectURL;
    URL.createObjectURL = vi.fn((blob: Blob) => {
      createdBlobs.push(blob);
      return "blob:mock-url";
    });
    URL.revokeObjectURL = vi.fn();

    // Mock createElement to intercept the download link
    const clickSpy = vi.fn();
    const originalCreateElement = document.createElement.bind(document);
    vi.spyOn(document, "createElement").mockImplementation((tag: string) => {
      if (tag === "a") {
        const el = originalCreateElement("a");
        el.click = clickSpy;
        return el;
      }
      return originalCreateElement(tag);
    });

    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    // Select all
    await user.click(screen.getByTestId("select-all-checkbox"));

    // Click Export Keys
    await user.click(screen.getByTestId("batch-export-keys"));

    // Verify a blob was created
    expect(createdBlobs.length).toBe(1);
    const blobContent = await createdBlobs[0]!.text();

    // Verify the .env format
    expect(blobContent).toContain("# LLM Provider API Keys");
    expect(blobContent).toContain("ANTHROPIC_API_KEY=****7890");
    expect(blobContent).toContain("OPENAI_API_KEY=****abcd");
    expect(blobContent).toContain("GROQ_API_KEY=****efgh");

    // Unconfigured providers should have commented-out entries
    expect(blobContent).toContain("# MISTRAL_API_KEY=");

    // Download was triggered
    expect(clickSpy).toHaveBeenCalled();

    URL.createObjectURL = originalCreateObjectURL;
    vi.restoreAllMocks();
  });
});

// ── E2E: Batch test all ──────────────────────────────────────────────

describe("E2E – Batch test all: test selected providers in parallel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
    mockFetchProviderAnalytics.mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" });
  });

  it("runs test for all configured selected providers and shows results", async () => {
    mockTestProviderKey
      .mockResolvedValueOnce({ success: true, message: "API key verified" })
      .mockResolvedValueOnce({ success: false, message: "Invalid API key" })
      .mockResolvedValueOnce({ success: true, message: "API key verified" });

    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));
    await user.click(screen.getByTestId("batch-test-all"));

    // Wait for results
    await waitFor(() => {
      expect(screen.getByTestId("batch-test-results")).toBeInTheDocument();
    });

    // Verify summary
    const summary = screen.getByTestId("batch-test-summary");
    expect(summary).toHaveTextContent("2 passed, 1 failed");

    // All configured providers were tested
    expect(mockTestProviderKey).toHaveBeenCalledTimes(3);
  });

  it("Test All is disabled when no configured providers are selected", async () => {
    renderGrid(emptySettings);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));

    expect(screen.getByTestId("batch-test-all")).toBeDisabled();
  });

  it("dismisses test results when Dismiss is clicked", async () => {
    mockTestProviderKey.mockResolvedValue({ success: true, message: "OK" });

    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));
    await user.click(screen.getByTestId("batch-test-all"));

    await waitFor(() => {
      expect(screen.getByTestId("batch-test-results")).toBeInTheDocument();
    });

    await user.click(screen.getByTestId("batch-test-dismiss"));
    expect(screen.queryByTestId("batch-test-results")).not.toBeInTheDocument();
  });
});

// ── E2E: Usage analytics in modal ────────────────────────────────────

describe("E2E – Usage analytics: stats appear in modal", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("shows stats tab with analytics data for a configured provider", async () => {
    mockFetchProviderAnalytics.mockResolvedValue({
      providers: {
        openai: {
          provider: "openai",
          total_requests: 42,
          error_count: 3,
          error_rate: 0.071,
          avg_latency: 185,
          last_error: "Rate limited",
          last_error_at: "2026-02-19T10:00:00Z",
        },
      },
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });

    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    // Open OpenAI modal
    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");
    await user.click(screen.getByRole("button", { name: /update/i }));

    expect(screen.getByRole("dialog")).toBeInTheDocument();

    // Switch to Stats tab
    await user.click(screen.getByTestId("tab-stats"));

    // Wait for stats to load
    await waitFor(() => {
      expect(screen.getByTestId("stats-content")).toBeInTheDocument();
    });

    // Verify stats are displayed
    expect(screen.getByTestId("stats-total-requests")).toHaveTextContent("42");
    expect(screen.getByTestId("stats-error-rate")).toHaveTextContent("7.1%");
    expect(screen.getByTestId("stats-avg-latency")).toHaveTextContent("185ms");
    expect(screen.getByTestId("stats-error-count")).toHaveTextContent("3");

    // Verify last error is shown
    expect(screen.getByTestId("stats-last-error")).toBeInTheDocument();
    expect(screen.getByText("Rate limited")).toBeInTheDocument();
  });

  it("shows empty state when no analytics data for a provider", async () => {
    mockFetchProviderAnalytics.mockResolvedValue({
      providers: {},
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });

    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    // Open Groq modal (no analytics data)
    await user.type(screen.getByTestId("provider-search-input"), "Groq");
    await user.click(screen.getByRole("button", { name: /update/i }));

    // Switch to Stats tab
    await user.click(screen.getByTestId("tab-stats"));

    await waitFor(() => {
      expect(screen.getByTestId("stats-empty")).toBeInTheDocument();
    });
    expect(screen.getByText(/no usage data/i)).toBeInTheDocument();
  });

  it("switches back to configure tab and resets", async () => {
    mockFetchProviderAnalytics.mockResolvedValue({
      providers: {},
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });

    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");
    await user.click(screen.getByRole("button", { name: /update/i }));

    // Switch to Stats, then back to Configure
    await user.click(screen.getByTestId("tab-stats"));
    await user.click(screen.getByTestId("tab-configure"));

    // Title should reflect Configure tab
    expect(screen.getByText("Configure OpenAI")).toBeInTheDocument();
    // API Key input should be available
    expect(screen.getByLabelText("API Key")).toBeInTheDocument();
  });
});

// ── E2E: Health indicators on cards ──────────────────────────────────

describe("E2E – Health indicators on provider cards", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("shows green health indicator for healthy provider", async () => {
    mockFetchProviderAnalytics.mockResolvedValue({
      providers: {
        anthropic: {
          provider: "anthropic",
          total_requests: 100,
          error_count: 2,
          error_rate: 0.02,
          avg_latency: 120,
        },
      },
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });

    renderGrid(settingsWithKeys);

    // Wait for analytics to load and health indicators to appear
    await waitFor(() => {
      const indicators = screen.getAllByTestId("health-indicator");
      expect(indicators.length).toBeGreaterThanOrEqual(1);
    });

    // Find the health indicator for Anthropic
    const indicators = screen.getAllByTestId("health-indicator");
    const greenIndicator = indicators.find(
      (el) => el.style.backgroundColor === HEALTH_COLORS.healthy
        || el.getAttribute("title")?.includes("Healthy"),
    );
    expect(greenIndicator).toBeDefined();
  });

  it("shows no health indicator when no analytics data exists", async () => {
    mockFetchProviderAnalytics.mockResolvedValue({
      providers: {},
      period_days: 7,
      since: "2026-02-13T00:00:00Z",
    });

    renderGrid(settingsWithKeys);

    // Wait a tick for analytics to load (empty)
    await waitFor(() => {
      expect(screen.getByTestId("provider-count-summary")).toBeInTheDocument();
    });

    // No health indicators should be visible when status is "unknown"
    expect(screen.queryByTestId("health-indicator")).not.toBeInTheDocument();
  });
});

// ── E2E: URL query param persistence for search/filter ───────────────

describe("E2E – URL query param persistence", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
    mockFetchProviderAnalytics.mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" });
  });

  it("restores search from URL ?q= param", () => {
    renderGrid(emptySettings, () => Promise.resolve(), false, ["/settings?q=OpenAI"]);

    const input = screen.getByTestId("provider-search-input") as HTMLInputElement;
    expect(input.value).toBe("OpenAI");
    expect(screen.getByText("OpenAI")).toBeInTheDocument();
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
  });

  it("restores filter from URL ?filter= param", () => {
    renderGrid(settingsWithKeys, () => Promise.resolve(), false, ["/settings?filter=configured"]);

    const select = screen.getByTestId("provider-filter-select") as HTMLSelectElement;
    expect(select.value).toBe("configured");
    expect(screen.getByText("Anthropic")).toBeInTheDocument();
    expect(screen.queryByText("Mistral")).not.toBeInTheDocument();
  });

  it("restores both search and filter from URL params", () => {
    renderGrid(settingsWithKeys, () => Promise.resolve(), false, [
      "/settings?q=Anthropic&filter=configured",
    ]);

    expect(screen.getByText("Anthropic")).toBeInTheDocument();
    expect(screen.queryByText("OpenAI")).not.toBeInTheDocument();
  });
});
