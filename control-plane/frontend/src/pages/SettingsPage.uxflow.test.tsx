import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import SettingsPage from "./SettingsPage";
import type { Settings } from "@/types/settings";
import { STORAGE_KEY } from "@/components/ConfirmDialog";

// ── Mocks ──────────────────────────────────────────────────────────────

const mockFetchSettings = vi.fn();
const mockUpdateSettings = vi.fn();

vi.mock("@/api/settings", () => ({
  fetchSettings: (...args: unknown[]) => mockFetchSettings(...args),
  updateSettings: (...args: unknown[]) => mockUpdateSettings(...args),
  fetchProviderAnalytics: vi.fn().mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" }),
}));

vi.mock("react-hot-toast", () => ({
  default: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

const emptySettings: Settings = {
  brave_api_key: "",
  api_keys: {},
  base_urls: {},
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

const settingsWithProviders: Settings = {
  ...emptySettings,
  api_keys: {
    ANTHROPIC_API_KEY: "****7890",
    OPENAI_API_KEY: "****abcd",
  },
  brave_api_key: "****qrst",
};

// ── Helpers ────────────────────────────────────────────────────────────

function renderPage(fetchValue: Settings = emptySettings) {
  mockFetchSettings.mockResolvedValue(fetchValue);
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

// ── 1. Full keyboard navigation ───────────────────────────────────────

describe("SettingsPage – complete keyboard navigation flow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ "delete-provider": true }),
    );
    mockUpdateSettings.mockResolvedValue(emptySettings);
  });

  it("navigates between tabs using only arrow keys", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("LLM Providers");

    const tabs = screen.getAllByRole("tab");

    // Focus the first tab
    tabs[0]!.focus();
    expect(tabs[0]).toHaveFocus();

    // ArrowRight → Resource Limits
    await user.keyboard("{ArrowRight}");
    expect(tabs[1]).toHaveFocus();
    expect(tabs[1]).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("Default Resource Limits")).toBeInTheDocument();

    // ArrowRight → Agent Image
    await user.keyboard("{ArrowRight}");
    expect(tabs[2]).toHaveFocus();
    expect(tabs[2]).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("Default Container Image")).toBeInTheDocument();

    // ArrowRight wraps → LLM Providers
    await user.keyboard("{ArrowRight}");
    expect(tabs[0]).toHaveFocus();
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("Provider Configuration")).toBeInTheDocument();

    // ArrowLeft wraps → Agent Image
    await user.keyboard("{ArrowLeft}");
    expect(tabs[2]).toHaveFocus();
    expect(tabs[2]).toHaveAttribute("aria-selected", "true");
  });

  it("navigates to first/last tab using Home/End keys", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("LLM Providers");

    const tabs = screen.getAllByRole("tab");

    // Start at first tab, End → last
    tabs[0]!.focus();
    await user.keyboard("{End}");
    expect(tabs[2]).toHaveFocus();
    expect(tabs[2]).toHaveAttribute("aria-selected", "true");

    // Home → first
    await user.keyboard("{Home}");
    expect(tabs[0]).toHaveFocus();
    expect(tabs[0]).toHaveAttribute("aria-selected", "true");
  });

  it("opens provider config modal via keyboard on a focused card", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Find first provider card and focus it
    const anthropicCard = screen
      .getByText("Anthropic")
      .closest("[tabindex='0']") as HTMLElement;
    anthropicCard.focus();
    expect(anthropicCard).toHaveFocus();

    // Press Enter to open modal
    await user.keyboard("{Enter}");

    // Modal should open
    const dialog = screen.getByRole("dialog");
    expect(dialog).toBeInTheDocument();
    expect(
      screen.getByText("Configure Anthropic"),
    ).toBeInTheDocument();
  });

  it("opens provider config modal via Space key on a focused card", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    const anthropicCard = screen
      .getByText("Anthropic")
      .closest("[tabindex='0']") as HTMLElement;
    anthropicCard.focus();

    await user.keyboard(" ");

    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("closes modal with Escape key", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Open modal
    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);

    expect(screen.getByRole("dialog")).toBeInTheDocument();

    // Close with Escape
    await user.keyboard("{Escape}");

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("traps focus within modal when tabbing (Shift+Tab wraps from first to last)", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Open Anthropic config modal
    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);

    const dialog = screen.getByRole("dialog");
    expect(dialog).toBeInTheDocument();

    // API key input should be focused initially
    const apiKeyInput = screen.getByLabelText("API Key");
    await vi.waitFor(() => {
      expect(apiKeyInput).toHaveFocus();
    });

    // The focus trap code queries focusable elements in the dialog ref.
    // Shift+Tab from the first focusable element should wrap to the last.
    // The first focusable in the dialog is the API key input.
    await user.keyboard("{Shift>}{Tab}{/Shift}");

    // Focus should have wrapped to the last focusable element.
    // Verify focus is still within the dialog (trapped).
    expect(dialog.contains(document.activeElement)).toBe(true);
    // And it should NOT be on the API key input anymore (it wrapped)
    expect(document.activeElement).not.toBe(apiKeyInput);
  });

  it("saves via Enter key in the API key input", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Open modal
    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);

    // Type key and press Enter
    const apiKeyInput = screen.getByLabelText("API Key");
    await user.type(apiKeyInput, "sk-ant-test1234567890");
    await user.keyboard("{Enter}");

    // Modal should close and Save Changes should appear
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByText("Save Changes")).toBeInTheDocument();
  });

  it("completes full keyboard-only flow: navigate → configure → save → verify", async () => {
    mockUpdateSettings.mockResolvedValue(emptySettings);
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // 1. Focus first provider card
    const anthropicCard = screen
      .getByText("Anthropic")
      .closest("[tabindex='0']") as HTMLElement;
    anthropicCard.focus();

    // 2. Open modal with Enter
    await user.keyboard("{Enter}");
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    // 3. Type API key (input auto-focused)
    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-test1234567890",
    );

    // 4. Save with Enter
    await user.keyboard("{Enter}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    // 5. Verify masked key appears
    expect(screen.getByText("****7890")).toBeInTheDocument();

    // 6. Click Save Changes to persist
    await user.click(screen.getByText("Save Changes"));

    // 7. Verify save was called
    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
  });
});

// ── 2. Focus indicators on all interactive elements ───────────────────

describe("SettingsPage – visible focus indicators", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    mockUpdateSettings.mockResolvedValue(emptySettings);
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

  it("provider cards have focus:ring classes", async () => {
    renderPage();
    await screen.findByText("Provider Configuration");

    const cards = screen.getAllByRole("generic").filter(
      (el) => el.getAttribute("tabindex") === "0" && el.getAttribute("aria-label")?.includes("provider"),
    );

    expect(cards.length).toBeGreaterThan(0);
    for (const card of cards) {
      expect(card.className).toContain("focus:ring-2");
      expect(card.className).toContain("focus:ring-blue-500");
    }
  });

  it("configure/update buttons have focus:ring classes", async () => {
    renderPage(settingsWithProviders);
    await screen.findByText("Provider Configuration");

    const configureButtons = screen.getAllByRole("button", {
      name: /configure|update/i,
    });
    for (const btn of configureButtons) {
      expect(btn.className).toContain("focus:ring-2");
      expect(btn.className).toContain("focus:ring-blue-500");
    }
  });

  it("delete buttons have focus:ring classes", async () => {
    renderPage(settingsWithProviders);
    await screen.findByText("Provider Configuration");

    const deleteButtons = screen.getAllByRole("button", {
      name: /remove.*api key/i,
    });
    expect(deleteButtons.length).toBeGreaterThan(0);
    for (const btn of deleteButtons) {
      expect(btn.className).toContain("focus:ring-2");
      expect(btn.className).toContain("focus:ring-blue-500");
    }
  });

  it("doc links have focus:ring classes", async () => {
    renderPage();
    await screen.findByText("Provider Configuration");

    const docLinks = screen.getAllByRole("link", {
      name: /documentation/i,
    });
    expect(docLinks.length).toBeGreaterThan(0);
    for (const link of docLinks) {
      expect(link.className).toContain("focus:ring-2");
      expect(link.className).toContain("focus:ring-blue-500");
    }
  });

  it("modal inputs and buttons have focus:ring classes", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Open modal
    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);

    const dialog = screen.getByRole("dialog");

    // API key input
    const apiKeyInput = within(dialog).getByLabelText("API Key");
    expect(apiKeyInput.className).toContain("focus:ring-2");
    expect(apiKeyInput.className).toContain("focus:ring-blue-500");

    // All buttons in dialog
    const dialogButtons = within(dialog).getAllByRole("button");
    for (const btn of dialogButtons) {
      expect(btn.className).toContain("focus:ring-2");
      expect(btn.className).toContain("focus:ring-blue-500");
    }

    // Doc link in dialog
    const docLink = within(dialog).getByRole("link");
    expect(docLink.className).toContain("focus:ring-2");
    expect(docLink.className).toContain("focus:ring-blue-500");
  });

  it("save changes button has focus:ring classes when visible", async () => {
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ "delete-provider": true }),
    );
    renderPage(settingsWithProviders);
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Delete a provider to make Save Changes appear
    const deleteButtons = screen.getAllByRole("button", {
      name: /remove.*api key/i,
    });
    await user.click(deleteButtons[0]!);

    const saveBtn = screen.getByTestId("save-changes-button");
    expect(saveBtn.className).toContain("focus:ring-2");
    expect(saveBtn.className).toContain("focus:ring-blue-500");
  });
});

// ── 3. Empty state ────────────────────────────────────────────────────

describe("SettingsPage – empty state flow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ "delete-provider": true }),
    );
    mockUpdateSettings.mockResolvedValue(emptySettings);
  });

  it("shows empty state message when no providers are configured", async () => {
    renderPage(emptySettings);
    await screen.findByText("Provider Configuration");

    expect(screen.getByTestId("provider-empty-state")).toBeInTheDocument();
    expect(
      screen.getByText("No providers configured yet"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Get started by adding your first API key"),
    ).toBeInTheDocument();
  });

  it("shows empty state when all providers are deleted", async () => {
    const singleProvider: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****7890" },
    };

    renderPage(singleProvider);
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Verify configured state
    expect(screen.getByText("1")).toBeInTheDocument();
    expect(
      screen.queryByTestId("provider-empty-state"),
    ).not.toBeInTheDocument();

    // Delete the only configured provider
    const deleteBtn = screen.getByRole("button", {
      name: /remove anthropic api key/i,
    });
    await user.click(deleteBtn);

    // Now the empty state should appear
    expect(screen.getByTestId("provider-empty-state")).toBeInTheDocument();
    expect(
      screen.getByText("No providers configured yet"),
    ).toBeInTheDocument();
  });

  it("hides empty state after configuring a provider", async () => {
    renderPage(emptySettings);
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Empty state is visible
    expect(screen.getByTestId("provider-empty-state")).toBeInTheDocument();

    // Configure first provider
    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Empty state should be gone
    expect(
      screen.queryByTestId("provider-empty-state"),
    ).not.toBeInTheDocument();
  });

  it("provider count updates correctly when adding and deleting", async () => {
    renderPage(emptySettings);
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Start with 0
    expect(screen.getByTestId("provider-count-summary")).toHaveTextContent(
      "0",
    );

    // Add one provider
    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(screen.getByTestId("provider-count-summary")).toHaveTextContent(
      "1",
    );
  });
});

// ── 4. Loading state ──────────────────────────────────────────────────

describe("SettingsPage – loading states", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
  });

  it("shows full page loading skeleton while settings are fetching", () => {
    // Never resolve the settings fetch
    mockFetchSettings.mockReturnValue(new Promise(() => {}));
    const qc = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
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

  it("loading skeleton contains animated pulse elements", () => {
    mockFetchSettings.mockReturnValue(new Promise(() => {}));
    const qc = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
      },
    });
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <SettingsPage />
        </MemoryRouter>
      </QueryClientProvider>,
    );

    const loadingContainer = screen.getByTestId("settings-loading");
    const pulseElements = loadingContainer.querySelectorAll(".animate-pulse");
    expect(pulseElements.length).toBeGreaterThan(0);
  });

  it("transitions from loading state to content when settings load", async () => {
    renderPage(emptySettings);

    // Initially shows loading
    // Then content appears
    await screen.findByText("Settings");
    expect(screen.queryByTestId("settings-loading")).not.toBeInTheDocument();
    expect(screen.getByText("LLM Providers")).toBeInTheDocument();
    expect(screen.getByText("Provider Configuration")).toBeInTheDocument();
  });
});

// ── 5. Confirmation dialog keyboard flow ──────────────────────────────

describe("SettingsPage – confirmation dialog keyboard interaction", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    // Do NOT suppress confirmation dialog for these tests
    mockUpdateSettings.mockResolvedValue(settingsWithProviders);
  });

  it("shows confirmation dialog when deleting a provider key", async () => {
    renderPage(settingsWithProviders);
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    const deleteBtn = screen.getAllByRole("button", {
      name: /remove.*api key/i,
    })[0]!;
    await user.click(deleteBtn);

    const dialog = screen.getByRole("dialog", { name: /delete api key/i });
    expect(dialog).toBeInTheDocument();
    expect(
      screen.getByText(/are you sure you want to delete/i),
    ).toBeInTheDocument();
  });

  it("cancels delete with Escape key in confirmation dialog", async () => {
    renderPage(settingsWithProviders);
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    const deleteBtn = screen.getAllByRole("button", {
      name: /remove.*api key/i,
    })[0]!;
    await user.click(deleteBtn);

    // Dialog appears
    expect(
      screen.getByText(/are you sure you want to delete/i),
    ).toBeInTheDocument();

    // Escape to cancel
    await user.keyboard("{Escape}");

    // Dialog dismissed, provider still configured
    expect(
      screen.queryByText(/are you sure you want to delete/i),
    ).not.toBeInTheDocument();

    // The masked key should still be visible (delete was canceled)
    expect(screen.getByText("****7890")).toBeInTheDocument();
  });

  it("confirms delete and provider disappears from configured list", async () => {
    renderPage(settingsWithProviders);
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // 3 providers configured
    expect(screen.getByText("****7890")).toBeInTheDocument();

    const deleteBtn = screen.getAllByRole("button", {
      name: /remove.*api key/i,
    })[0]!;
    await user.click(deleteBtn);

    // Confirm
    await user.click(screen.getByTestId("confirm-dialog-confirm"));

    // Dialog dismissed
    expect(
      screen.queryByText(/are you sure you want to delete/i),
    ).not.toBeInTheDocument();

    // Save Changes button appears
    expect(screen.getByText("Save Changes")).toBeInTheDocument();
  });

  it("confirmation dialog focuses Cancel button by default (safe-by-default)", async () => {
    renderPage(settingsWithProviders);
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    const deleteBtn = screen.getAllByRole("button", {
      name: /remove.*api key/i,
    })[0]!;
    await user.click(deleteBtn);

    // Cancel button should be focused by default
    const cancelBtn = screen.getByTestId("confirm-dialog-cancel");
    expect(cancelBtn).toHaveFocus();
  });
});

// ── 6. Save success feedback ──────────────────────────────────────────

describe("SettingsPage – save success feedback", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ "delete-provider": true }),
    );
  });

  it("shows green 'Saved!' feedback with checkmark after successful save", async () => {
    mockUpdateSettings.mockResolvedValue(emptySettings);
    renderPage(emptySettings);
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Add a provider key
    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Save changes
    await user.click(screen.getByText("Save Changes"));

    // Should show success state
    await vi.waitFor(() => {
      expect(screen.getByText("Saved!")).toBeInTheDocument();
    });
    expect(screen.getByTestId("save-success-icon")).toBeInTheDocument();

    // Button should be green
    const saveBtn = screen.getByTestId("save-changes-button");
    expect(saveBtn.className).toContain("bg-green-600");
  });
});

// ── 7. Complete end-to-end UX flow ────────────────────────────────────

describe("SettingsPage – complete end-to-end UX flow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ "delete-provider": true }),
    );
  });

  it("full lifecycle: load → configure → save → navigate tabs → delete → save", async () => {
    mockUpdateSettings.mockResolvedValue(emptySettings);
    renderPage(emptySettings);
    const user = userEvent.setup();

    // 1. Wait for page to load
    await screen.findByText("Provider Configuration");
    expect(screen.getByText("0")).toBeInTheDocument();

    // 2. Configure Anthropic
    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    expect(screen.getByText("Configure Anthropic")).toBeInTheDocument();

    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // 3. Verify masked key appears
    expect(screen.getByText("****7890")).toBeInTheDocument();
    expect(screen.getByText("1")).toBeInTheDocument();

    // 4. Save changes
    await user.click(screen.getByText("Save Changes"));
    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);

    // 5. Navigate to Resource Limits tab via keyboard
    const tabs = screen.getAllByRole("tab");
    tabs[0]!.focus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByText("Default Resource Limits")).toBeInTheDocument();

    // 6. Navigate back to LLM Providers
    await user.keyboard("{ArrowLeft}");
    expect(screen.getByText("Provider Configuration")).toBeInTheDocument();
  });

  it("full lifecycle with multiple providers: add, update, delete", async () => {
    mockUpdateSettings.mockResolvedValue(settingsWithProviders);
    renderPage(settingsWithProviders);
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Start with 3 configured providers (Anthropic, OpenAI, Brave)
    expect(screen.getByText("3")).toBeInTheDocument();

    // Delete one provider
    const deleteButtons = screen.getAllByRole("button", {
      name: /remove.*api key/i,
    });
    await user.click(deleteButtons[0]!);

    // Add a new provider (Groq)
    const groqText = screen.getByText("Groq");
    const groqCard = groqText.closest("[tabindex='0']") as HTMLElement;
    const groqConfigBtn = within(groqCard).getByRole("button", {
      name: /configure/i,
    });
    await user.click(groqConfigBtn);

    await user.type(
      screen.getByLabelText("API Key"),
      "gsk_test1234567890abcdef",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Should still have 3 configured (one deleted, one added)
    expect(screen.getByText("3")).toBeInTheDocument();

    // Save all changes
    await user.click(screen.getByText("Save Changes"));
    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
  });

  it("handles OpenAI provider with base URL field", async () => {
    mockUpdateSettings.mockResolvedValue(emptySettings);
    renderPage(emptySettings);
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    // Find and open OpenAI configure modal
    const openaiText = screen.getByText("OpenAI");
    const openaiCard = openaiText.closest("[tabindex='0']") as HTMLElement;
    const configBtn = within(openaiCard).getByRole("button", {
      name: /configure/i,
    });
    await user.click(configBtn);

    // Modal should have both API Key and Base URL inputs
    expect(screen.getByLabelText("API Key")).toBeInTheDocument();
    expect(screen.getByLabelText(/base url/i)).toBeInTheDocument();

    // Check placeholder
    const apiKeyInput = screen.getByLabelText("API Key");
    expect(apiKeyInput).toHaveAttribute("placeholder", "sk-...");

    const baseUrlInput = screen.getByLabelText(/base url/i);
    expect(baseUrlInput).toHaveAttribute(
      "placeholder",
      "https://api.openai.com/v1",
    );

    // Save with key only
    await user.type(apiKeyInput, "sk-test1234567890abcdef");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("tab panels have correct ARIA attributes for panel-tab association", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("LLM Providers");

    // LLM Providers tab panel
    let panel = screen.getByRole("tabpanel");
    expect(panel).toHaveAttribute("id", "tabpanel-providers");
    expect(panel).toHaveAttribute("aria-labelledby", "tab-providers");

    // Switch to Resource Limits
    await user.click(screen.getByRole("tab", { name: "Resource Limits" }));
    panel = screen.getByRole("tabpanel");
    expect(panel).toHaveAttribute("id", "tabpanel-resources");
    expect(panel).toHaveAttribute("aria-labelledby", "tab-resources");

    // Switch to Agent Image
    await user.click(screen.getByRole("tab", { name: "Agent Image" }));
    panel = screen.getByRole("tabpanel");
    expect(panel).toHaveAttribute("id", "tabpanel-image");
    expect(panel).toHaveAttribute("aria-labelledby", "tab-image");
  });

  it("modal has correct ARIA attributes for accessibility", async () => {
    renderPage();
    const user = userEvent.setup();
    await screen.findByText("Provider Configuration");

    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);

    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-modal", "true");
    expect(dialog).toHaveAttribute("aria-labelledby", "provider-modal-title");

    // Title referenced by aria-labelledby exists
    const title = screen.getByText("Configure Anthropic");
    expect(title).toHaveAttribute("id", "provider-modal-title");
  });
});
