import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import ProviderGrid from "./ProviderGrid";
import type { Settings } from "@/types/settings";
import type { ProviderSavePayload } from "./ProviderGrid";
import { PROVIDERS } from "./providerData";
import { STORAGE_KEY } from "../ConfirmDialog";

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

// ── Search and filter UI ──────────────────────────────────────────────

describe("ProviderGrid – search and filter UI", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("renders search input", () => {
    renderGrid();
    expect(screen.getByTestId("provider-search-input")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Search providers...")).toBeInTheDocument();
  });

  it("renders filter dropdown with All, Configured, Not Configured options", () => {
    renderGrid();
    const select = screen.getByTestId("provider-filter-select");
    expect(select).toBeInTheDocument();
    expect(screen.getByText("Show: All")).toBeInTheDocument();
    expect(screen.getByText("Show: Configured")).toBeInTheDocument();
    expect(screen.getByText("Show: Not Configured")).toBeInTheDocument();
  });

  it("search input has accessible label", () => {
    renderGrid();
    expect(screen.getByLabelText("Search providers")).toBeInTheDocument();
  });

  it("filter dropdown has accessible label", () => {
    renderGrid();
    expect(screen.getByLabelText("Filter providers by status")).toBeInTheDocument();
  });

  it("renders search and filter container", () => {
    renderGrid();
    expect(screen.getByTestId("provider-search-filter")).toBeInTheDocument();
  });
});

// ── Search by name ────────────────────────────────────────────────────

describe("ProviderGrid – search by name", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("filters providers by name as user types", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");

    expect(screen.getByText("OpenAI")).toBeInTheDocument();
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
    expect(screen.queryByText("Groq")).not.toBeInTheDocument();
  });

  it("search is case-insensitive", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "openai");

    expect(screen.getByText("OpenAI")).toBeInTheDocument();
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
  });

  it("filters by partial name match", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "gro");

    expect(screen.getByText("Groq")).toBeInTheDocument();
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
  });

  it("shows multiple matching providers", async () => {
    renderGrid();
    const user = userEvent.setup();

    // "open" matches OpenAI and OpenRouter
    await user.type(screen.getByTestId("provider-search-input"), "open");

    expect(screen.getByText("OpenAI")).toBeInTheDocument();
    expect(screen.getByText("OpenRouter")).toBeInTheDocument();
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
  });

  it("hides category headers when no providers match in that category", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "Anthropic");

    // Major Providers should still be visible (Anthropic is there)
    expect(screen.getByText("Major Providers")).toBeInTheDocument();
    // Other categories should be hidden
    expect(screen.queryByText("Open Source / Inference")).not.toBeInTheDocument();
    expect(screen.queryByText("Specialized")).not.toBeInTheDocument();
    expect(screen.queryByText("Aggregators")).not.toBeInTheDocument();
    expect(screen.queryByText("Search & Tools")).not.toBeInTheDocument();
  });
});

// ── Search by description ─────────────────────────────────────────────

describe("ProviderGrid – search by description", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("filters providers by description content", async () => {
    renderGrid();
    const user = userEvent.setup();

    // "Claude" appears in Anthropic's description
    await user.type(screen.getByTestId("provider-search-input"), "Claude");

    expect(screen.getByText("Anthropic")).toBeInTheDocument();
    expect(screen.queryByText("OpenAI")).not.toBeInTheDocument();
  });

  it("matches on description keywords", async () => {
    renderGrid();
    const user = userEvent.setup();

    // "web search" appears in Brave's description
    await user.type(screen.getByTestId("provider-search-input"), "web search");

    expect(screen.getByText("Brave")).toBeInTheDocument();
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
  });
});

// ── Filter by status ──────────────────────────────────────────────────

describe("ProviderGrid – filter by status", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("shows only configured providers when 'Configured' filter is selected", async () => {
    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    await user.selectOptions(
      screen.getByTestId("provider-filter-select"),
      "configured",
    );

    // Configured providers should be visible
    expect(screen.getByText("Anthropic")).toBeInTheDocument();
    expect(screen.getByText("OpenAI")).toBeInTheDocument();
    expect(screen.getByText("Groq")).toBeInTheDocument();

    // Unconfigured providers should be hidden
    expect(screen.queryByText("Mistral")).not.toBeInTheDocument();
    expect(screen.queryByText("DeepSeek")).not.toBeInTheDocument();
    expect(screen.queryByText("Brave")).not.toBeInTheDocument();
  });

  it("shows only not-configured providers when 'Not Configured' filter is selected", async () => {
    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    await user.selectOptions(
      screen.getByTestId("provider-filter-select"),
      "not-configured",
    );

    // Configured providers should be hidden
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
    expect(screen.queryByText("OpenAI")).not.toBeInTheDocument();
    expect(screen.queryByText("Groq")).not.toBeInTheDocument();

    // Unconfigured providers should be visible
    expect(screen.getByText("Mistral")).toBeInTheDocument();
    expect(screen.getByText("Brave")).toBeInTheDocument();
  });

  it("shows all providers when 'All' filter is re-selected", async () => {
    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    // First filter to configured
    await user.selectOptions(
      screen.getByTestId("provider-filter-select"),
      "configured",
    );
    expect(screen.queryByText("Mistral")).not.toBeInTheDocument();

    // Then reset to all
    await user.selectOptions(
      screen.getByTestId("provider-filter-select"),
      "all",
    );
    expect(screen.getByText("Mistral")).toBeInTheDocument();
    expect(screen.getByText("Anthropic")).toBeInTheDocument();
  });
});

// ── Combined search and filter ────────────────────────────────────────

describe("ProviderGrid – combined search and filter", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("applies both search and filter simultaneously", async () => {
    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    // Filter to configured only
    await user.selectOptions(
      screen.getByTestId("provider-filter-select"),
      "configured",
    );

    // Then search for "Anthropic"
    await user.type(screen.getByTestId("provider-search-input"), "Anthropic");

    // Only Anthropic (configured + matches search) should be visible
    expect(screen.getByText("Anthropic")).toBeInTheDocument();
    expect(screen.queryByText("OpenAI")).not.toBeInTheDocument();
    expect(screen.queryByText("Groq")).not.toBeInTheDocument();
  });

  it("shows no results when search and filter yield empty", async () => {
    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    // Filter to configured
    await user.selectOptions(
      screen.getByTestId("provider-filter-select"),
      "configured",
    );

    // Search for a provider that isn't configured
    await user.type(screen.getByTestId("provider-search-input"), "Brave");

    expect(screen.getByTestId("provider-no-results")).toBeInTheDocument();
    expect(screen.getByText("No providers match your search")).toBeInTheDocument();
  });
});

// ── No results empty state ────────────────────────────────────────────

describe("ProviderGrid – no results empty state", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("shows 'No providers match your search' when search yields no results", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "zzzznonexistent");

    expect(screen.getByTestId("provider-no-results")).toBeInTheDocument();
    expect(screen.getByText("No providers match your search")).toBeInTheDocument();
    expect(screen.getByText("Try adjusting your search or filter criteria")).toBeInTheDocument();
  });

  it("does not show no-results state when results exist", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");

    expect(screen.queryByTestId("provider-no-results")).not.toBeInTheDocument();
  });

  it("does not show no-results state when no filters are active", () => {
    renderGrid();
    expect(screen.queryByTestId("provider-no-results")).not.toBeInTheDocument();
  });

  it("shows empty state (not no-results) when no providers configured and no filters active", () => {
    renderGrid();
    expect(screen.getByTestId("provider-empty-state")).toBeInTheDocument();
    expect(screen.queryByTestId("provider-no-results")).not.toBeInTheDocument();
  });

  it("hides original empty state when filters are active", async () => {
    renderGrid();
    const user = userEvent.setup();

    // Type something that matches no provider
    await user.type(screen.getByTestId("provider-search-input"), "zzzznonexistent");

    // No-results should show, but not the "no configured" empty state
    expect(screen.queryByTestId("provider-empty-state")).not.toBeInTheDocument();
    expect(screen.getByTestId("provider-no-results")).toBeInTheDocument();
  });
});

// ── Clear search ──────────────────────────────────────────────────────

describe("ProviderGrid – clear search", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("shows clear button when search has text", async () => {
    renderGrid();
    const user = userEvent.setup();

    expect(screen.queryByTestId("provider-search-clear")).not.toBeInTheDocument();

    await user.type(screen.getByTestId("provider-search-input"), "test");

    expect(screen.getByTestId("provider-search-clear")).toBeInTheDocument();
  });

  it("clears search when clear button is clicked", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();

    await user.click(screen.getByTestId("provider-search-clear"));

    // All providers should be visible again
    expect(screen.getByText("Anthropic")).toBeInTheDocument();
    expect(screen.getByText("OpenAI")).toBeInTheDocument();
  });

  it("clear button has accessible label", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.type(screen.getByTestId("provider-search-input"), "test");

    expect(screen.getByLabelText("Clear search")).toBeInTheDocument();
  });
});

// ── URL query param persistence ───────────────────────────────────────

describe("ProviderGrid – URL query param persistence", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("initializes search from URL query param", () => {
    renderGrid(emptySettings, () => Promise.resolve(), false, [
      "/settings?q=OpenAI",
    ]);

    const input = screen.getByTestId("provider-search-input") as HTMLInputElement;
    expect(input.value).toBe("OpenAI");
    // Should only show OpenAI (filtered by search)
    expect(screen.getByText("OpenAI")).toBeInTheDocument();
    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
  });

  it("initializes filter from URL query param", () => {
    renderGrid(settingsWithKeys, () => Promise.resolve(), false, [
      "/settings?filter=configured",
    ]);

    const select = screen.getByTestId("provider-filter-select") as HTMLSelectElement;
    expect(select.value).toBe("configured");
    // Should only show configured providers
    expect(screen.getByText("Anthropic")).toBeInTheDocument();
    expect(screen.queryByText("Mistral")).not.toBeInTheDocument();
  });

  it("initializes both search and filter from URL query params", () => {
    renderGrid(settingsWithKeys, () => Promise.resolve(), false, [
      "/settings?q=Anthropic&filter=configured",
    ]);

    const input = screen.getByTestId("provider-search-input") as HTMLInputElement;
    expect(input.value).toBe("Anthropic");

    const select = screen.getByTestId("provider-filter-select") as HTMLSelectElement;
    expect(select.value).toBe("configured");

    expect(screen.getByText("Anthropic")).toBeInTheDocument();
    expect(screen.queryByText("OpenAI")).not.toBeInTheDocument();
  });

  it("defaults to all filter when no filter param in URL", () => {
    renderGrid(emptySettings, () => Promise.resolve(), false, ["/settings"]);

    const select = screen.getByTestId("provider-filter-select") as HTMLSelectElement;
    expect(select.value).toBe("all");
  });
});

// ── Interaction with existing features ────────────────────────────────

describe("ProviderGrid – search interaction with existing features", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("can configure a provider while search is active", async () => {
    renderGrid();
    const user = userEvent.setup();

    // Search for OpenAI
    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");

    // Configure the visible OpenAI provider
    const configureBtn = screen.getByRole("button", { name: /configure/i });
    await user.click(configureBtn);

    // Modal should open
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("Configure OpenAI")).toBeInTheDocument();
  });

  it("maintains configured count regardless of search filter", async () => {
    renderGrid(settingsWithKeys);
    const user = userEvent.setup();

    // Count should show 3 configured
    const summary = screen.getByTestId("provider-count-summary");
    expect(summary).toHaveTextContent("3");

    // Search should not change configured count
    await user.type(screen.getByTestId("provider-search-input"), "OpenAI");
    expect(summary).toHaveTextContent("3");
  });

  it("search does not affect Save Changes functionality", async () => {
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );
    renderGrid(emptySettings, onSaveChanges);
    const user = userEvent.setup();

    // Search for Anthropic
    await user.type(screen.getByTestId("provider-search-input"), "Anthropic");

    // Configure it
    const configureBtn = screen.getByRole("button", { name: /configure/i });
    await user.click(configureBtn);
    await user.type(screen.getByLabelText("API Key"), "sk-ant-test1234567890");
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Save Changes should work
    await user.click(screen.getByText("Save Changes"));
    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    expect(onSaveChanges.mock.calls[0]![0].api_keys).toEqual({
      ANTHROPIC_API_KEY: "sk-ant-test1234567890",
    });
  });
});
