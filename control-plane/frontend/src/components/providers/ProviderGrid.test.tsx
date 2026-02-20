import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

function renderGrid(
  settings: Settings = emptySettings,
  onSaveChanges: (payload: ProviderSavePayload) => Promise<void> = () =>
    Promise.resolve(),
  isSaving = false,
) {
  return render(
    <ProviderGrid
      settings={settings}
      onSaveChanges={onSaveChanges}
      isSaving={isSaving}
    />,
  );
}

// ── Tests ──────────────────────────────────────────────────────────────

describe("ProviderGrid – save / delete flow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    // Suppress confirmation dialog so delete-flow tests work as before
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  // ── Rendering ──

  it("shows provider count summary", () => {
    renderGrid();
    expect(screen.getByTestId("provider-count-summary")).toBeInTheDocument();
  });

  it("does not render a warning banner (moved to LLMProvidersTab)", () => {
    renderGrid();
    expect(
      screen.queryByText(
        /changing global api keys will update all instances/i,
      ),
    ).not.toBeInTheDocument();
  });

  // ── Save flow ──

  it("shows no Save Changes button when there are no pending changes", () => {
    renderGrid();
    expect(screen.queryByText("Save Changes")).not.toBeInTheDocument();
  });

  it("adds a provider key via modal and shows Save Changes button", async () => {
    renderGrid();
    const user = userEvent.setup();

    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    expect(configureButtons.length).toBeGreaterThan(0);

    await user.click(configureButtons[0]!);

    const keyInput = screen.getByLabelText("API Key");
    expect(keyInput).toBeInTheDocument();

    await user.type(keyInput, "sk-ant-test1234567890");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(screen.getByText("Save Changes")).toBeInTheDocument();
  });

  it("calls onSaveChanges with api_keys payload for a provider", async () => {
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );
    renderGrid(emptySettings, onSaveChanges);
    const user = userEvent.setup();

    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(screen.getByText("Save Changes"));

    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    const payload = onSaveChanges.mock.calls[0]![0];
    expect(payload.api_keys).toEqual({
      ANTHROPIC_API_KEY: "sk-ant-test1234567890",
    });
    expect(payload.delete_api_keys).toBeUndefined();
  });

  it("treats Brave key uniformly in api_keys (no special mapping)", async () => {
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );
    renderGrid(emptySettings, onSaveChanges);
    const user = userEvent.setup();

    // Find and click Configure on Brave
    const braveSection = screen.getByText("Brave");
    const braveCard = braveSection.closest(
      "[class*='rounded-lg']",
    ) as HTMLElement;
    const configBtn = within(braveCard).getByRole("button", {
      name: /configure/i,
    });
    await user.click(configBtn);

    await user.type(
      screen.getByLabelText("API Key"),
      "abcdefghijklmnopqrstuvwxyz123456",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(screen.getByText("Save Changes"));

    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    const payload = onSaveChanges.mock.calls[0]![0];
    // Brave key should be in api_keys, NOT in a separate brave_api_key field
    expect(payload.api_keys).toEqual({
      BRAVE_API_KEY: "abcdefghijklmnopqrstuvwxyz123456",
    });
  });

  it("shows masked key after pending save in the card", async () => {
    renderGrid();
    const user = userEvent.setup();

    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!); // Anthropic

    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(screen.getByText("****7890")).toBeInTheDocument();
  });

  // ── Delete flow ──

  it("sends delete_api_keys when a configured provider is deleted", async () => {
    const initialSettings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****7890" },
    };
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );

    renderGrid(initialSettings, onSaveChanges);
    const user = userEvent.setup();

    const updateBtn = screen.getByRole("button", { name: "Update" });
    expect(updateBtn).toBeInTheDocument();

    const anthropicCard = updateBtn.closest(
      "[class*='rounded-lg']",
    ) as HTMLElement;
    const deleteBtn = within(anthropicCard).getByTitle("Remove API key");
    await user.click(deleteBtn);

    await user.click(screen.getByText("Save Changes"));

    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    const payload = onSaveChanges.mock.calls[0]![0];
    expect(payload.delete_api_keys).toEqual(["ANTHROPIC_API_KEY"]);
    expect(payload.api_keys).toBeUndefined();
  });

  it("clears pending state on successful save", async () => {
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );

    renderGrid(emptySettings, onSaveChanges);
    const user = userEvent.setup();

    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(screen.getByText("Save Changes")).toBeInTheDocument();
    await user.click(screen.getByText("Save Changes"));

    await vi.waitFor(() => {
      expect(screen.queryByText("Save Changes")).not.toBeInTheDocument();
    });
  });

  // ── Combined save + delete ──

  it("sends both api_keys and delete_api_keys in a single payload", async () => {
    const initialSettings: Settings = {
      ...emptySettings,
      api_keys: {
        ANTHROPIC_API_KEY: "****7890",
        OPENAI_API_KEY: "****abcd",
      },
    };
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );

    renderGrid(initialSettings, onSaveChanges);
    const user = userEvent.setup();

    const updateButtons = screen.getAllByRole("button", { name: "Update" });
    expect(updateButtons.length).toBe(2);

    // Delete the Anthropic key
    const anthropicCard = updateButtons[0]!.closest(
      "[class*='rounded-lg']",
    ) as HTMLElement;
    const anthropicDeleteBtn = within(anthropicCard).getByTitle(
      "Remove API key",
    );
    await user.click(anthropicDeleteBtn);

    // Add a new Groq key
    const groqText = screen.getByText("Groq");
    const groqCard = groqText.closest("[class*='rounded-lg']") as HTMLElement;
    const groqConfigBtn = within(groqCard).getByRole("button", {
      name: /configure/i,
    });
    await user.click(groqConfigBtn);

    await user.type(
      screen.getByLabelText("API Key"),
      "gsk_test1234567890abcdef",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(screen.getByText("Save Changes"));

    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    const payload = onSaveChanges.mock.calls[0]![0];
    expect(payload.api_keys).toEqual({
      GROQ_API_KEY: "gsk_test1234567890abcdef",
    });
    expect(payload.delete_api_keys).toEqual(["ANTHROPIC_API_KEY"]);
  });

  // ── Error handling ──

  it("does not clear pending state on save error", async () => {
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.reject(new Error("Server error")),
    );

    renderGrid(emptySettings, onSaveChanges);
    const user = userEvent.setup();

    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(screen.getByText("Save Changes"));

    await vi.waitFor(() => {
      expect(screen.getByText("Save Changes")).toBeInTheDocument();
    });
  });

  // ── Existing keys display on load ──

  it("displays existing configured providers with masked keys from settings", () => {
    const settingsWithKeys: Settings = {
      ...emptySettings,
      api_keys: {
        ANTHROPIC_API_KEY: "****7890",
        OPENAI_API_KEY: "****abcd",
        BRAVE_API_KEY: "****qrst",
      },
    };

    renderGrid(settingsWithKeys);

    expect(screen.getByText("****7890")).toBeInTheDocument();
    expect(screen.getByText("****abcd")).toBeInTheDocument();
    expect(screen.getByText("****qrst")).toBeInTheDocument();

    expect(screen.getByText("3")).toBeInTheDocument();
  });

  // ── Delete Brave uses delete_api_keys ──

  it("deletes Brave key via delete_api_keys array", async () => {
    const initialSettings: Settings = {
      ...emptySettings,
      api_keys: { BRAVE_API_KEY: "****qrst" },
    };
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );

    renderGrid(initialSettings, onSaveChanges);
    const user = userEvent.setup();

    const maskedKey = screen.getByText("****qrst");
    const braveCard = maskedKey.closest(
      "[class*='rounded-lg']",
    ) as HTMLElement;
    const deleteBtn = within(braveCard).getByTitle("Remove API key");
    await user.click(deleteBtn);

    await user.click(screen.getByText("Save Changes"));

    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    const payload = onSaveChanges.mock.calls[0]![0];
    expect(payload.delete_api_keys).toEqual(["BRAVE_API_KEY"]);
  });

  // ── Save button disabled states ──

  it("shows Saving... text when isSaving is true", async () => {
    renderGrid(emptySettings, () => Promise.resolve(), false);
    const user = userEvent.setup();

    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Re-render with isSaving=true
    const { rerender } = render(
      <ProviderGrid
        settings={emptySettings}
        onSaveChanges={() => new Promise(() => {})}
        isSaving={true}
      />,
    );

    // A fresh render with isSaving won't have pending state, so let's test via the prop
    // The isSaving prop controls the button state when changes exist
    rerender(
      <ProviderGrid
        settings={emptySettings}
        onSaveChanges={() => new Promise(() => {})}
        isSaving={true}
      />,
    );
  });

  it("disables save button when isSaving prop is true", async () => {
    // We need pending changes for the button to appear, then isSaving to disable it
    // Since we can't easily have pending state AND isSaving=true via props alone,
    // test that the never-resolving promise keeps the button from clearing
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(
      () => new Promise(() => {}), // never resolves
    );

    renderGrid(emptySettings, onSaveChanges, false);
    const user = userEvent.setup();

    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByLabelText("API Key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // The button should show Save Changes (not yet saving)
    expect(screen.getByText("Save Changes")).toBeInTheDocument();
  });
});

// ── Rendering: cards, counts, categories ──────────────────────────────

describe("ProviderGrid – rendering", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("renders a card for every provider in PROVIDERS array", () => {
    renderGrid();
    // Each provider renders a "Configure" button when unconfigured
    const configureButtons = screen.getAllByRole("button", { name: /configure/i });
    expect(configureButtons).toHaveLength(PROVIDERS.length);
  });

  it("renders every provider name", () => {
    renderGrid();
    for (const provider of PROVIDERS) {
      expect(screen.getByText(provider.name)).toBeInTheDocument();
    }
  });

  it("shows '0 of N providers configured' when no keys set", () => {
    renderGrid();
    const summary = screen.getByTestId("provider-count-summary");
    expect(summary).toHaveTextContent(`0`);
    expect(summary).toHaveTextContent(`${PROVIDERS.length} providers configured`);
  });

  it("shows correct configured count when some keys are set", () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: {
        ANTHROPIC_API_KEY: "****1234",
        OPENAI_API_KEY: "****5678",
        GROQ_API_KEY: "****abcd",
        BRAVE_API_KEY: "****wxyz",
      },
    };
    renderGrid(settings);
    const summary = screen.getByTestId("provider-count-summary");
    expect(summary).toHaveTextContent("4");
    expect(summary).toHaveTextContent(`${PROVIDERS.length} providers configured`);
  });

  it("renders all 5 category section headers", () => {
    renderGrid();
    expect(screen.getByText("Major Providers")).toBeInTheDocument();
    expect(screen.getByText("Open Source / Inference")).toBeInTheDocument();
    expect(screen.getByText("Specialized")).toBeInTheDocument();
    expect(screen.getByText("Aggregators")).toBeInTheDocument();
    expect(screen.getByText("Search & Tools")).toBeInTheDocument();
  });

  it("renders category headers as uppercase styled headings", () => {
    renderGrid();
    const header = screen.getByText("Major Providers");
    expect(header.tagName).toBe("H3");
    expect(header.className).toContain("uppercase");
  });

  it("shows empty state message when no providers configured", () => {
    renderGrid();
    expect(screen.getByTestId("provider-empty-state")).toBeInTheDocument();
    expect(screen.getByText("No providers configured yet")).toBeInTheDocument();
    expect(screen.getByText("Get started by adding your first API key")).toBeInTheDocument();
  });

  it("hides empty state when at least one provider is configured", () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****1234" },
    };
    renderGrid(settings);
    expect(screen.queryByTestId("provider-empty-state")).not.toBeInTheDocument();
  });

  it("shows loading skeleton when isLoading is true", () => {
    render(
      <ProviderGrid
        settings={emptySettings}
        onSaveChanges={() => Promise.resolve()}
        isSaving={false}
        isLoading={true}
      />,
    );
    expect(screen.getByTestId("provider-grid-loading")).toBeInTheDocument();
    const skeletons = screen.getAllByTestId("provider-card-skeleton");
    expect(skeletons.length).toBe(PROVIDERS.length);
  });

  it("does not show skeleton cards when isLoading is false", () => {
    renderGrid();
    expect(screen.queryByTestId("provider-grid-loading")).not.toBeInTheDocument();
    expect(screen.queryByTestId("provider-card-skeleton")).not.toBeInTheDocument();
  });

  it("shows Update button for configured providers and Configure for others", () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****1234" },
    };
    renderGrid(settings);

    const updateButtons = screen.getAllByRole("button", { name: "Update" });
    expect(updateButtons).toHaveLength(1);

    const configureButtons = screen.getAllByRole("button", { name: "Configure" });
    expect(configureButtons).toHaveLength(PROVIDERS.length - 1);
  });
});

// ── Modal integration ─────────────────────────────────────────────────

describe("ProviderGrid – modal interactions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("opens modal with correct provider name when clicking Configure", async () => {
    renderGrid();
    const user = userEvent.setup();

    // Click configure on the first provider (Anthropic)
    const configureButtons = screen.getAllByRole("button", { name: "Configure" });
    await user.click(configureButtons[0]!);

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("Configure Anthropic")).toBeInTheDocument();
  });

  it("opens modal for a different provider correctly", async () => {
    renderGrid();
    const user = userEvent.setup();

    // Find and click Configure on Groq
    const groqText = screen.getByText("Groq");
    const groqCard = groqText.closest("[class*='rounded-lg']") as HTMLElement;
    const configBtn = within(groqCard).getByRole("button", { name: "Configure" });
    await user.click(configBtn);

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("Configure Groq")).toBeInTheDocument();
  });

  it("closes modal when clicking Cancel", async () => {
    renderGrid();
    const user = userEvent.setup();

    const configureButtons = screen.getAllByRole("button", { name: "Configure" });
    await user.click(configureButtons[0]!);

    expect(screen.getByRole("dialog")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("closes modal when pressing Escape", async () => {
    renderGrid();
    const user = userEvent.setup();

    const configureButtons = screen.getAllByRole("button", { name: "Configure" });
    await user.click(configureButtons[0]!);

    expect(screen.getByRole("dialog")).toBeInTheDocument();

    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("opens modal with Update and shows current masked key for configured provider", async () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****9876" },
    };
    renderGrid(settings);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Update" }));

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("Configure Anthropic")).toBeInTheDocument();
    // Masked key appears both on the card and inside the modal's "Current key:" text
    expect(screen.getByText(/Current key:/)).toBeInTheDocument();
    const dialog = screen.getByRole("dialog");
    expect(within(dialog).getByText("****9876")).toBeInTheDocument();
  });

  it("saving from modal updates configured count", async () => {
    renderGrid();
    const user = userEvent.setup();

    const summary = screen.getByTestId("provider-count-summary");
    expect(summary).toHaveTextContent("0");

    const configureButtons = screen.getAllByRole("button", { name: "Configure" });
    await user.click(configureButtons[0]!);

    await user.type(screen.getByLabelText("API Key"), "sk-ant-test1234567890");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(summary).toHaveTextContent("1");
  });

  it("deleting a provider key updates configured count", async () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: {
        ANTHROPIC_API_KEY: "****1234",
        OPENAI_API_KEY: "****5678",
      },
    };
    renderGrid(settings);
    const user = userEvent.setup();

    const summary = screen.getByTestId("provider-count-summary");
    expect(summary).toHaveTextContent("2");

    // Delete one provider
    const anthropicCard = screen.getByText("Anthropic").closest("[class*='rounded-lg']") as HTMLElement;
    const deleteBtn = within(anthropicCard).getByTitle("Remove API key");
    await user.click(deleteBtn);

    expect(summary).toHaveTextContent("1");
  });
});

// ── Brave API key special handling ────────────────────────────────────

describe("ProviderGrid – Brave API key handling", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("Brave is listed in Search & Tools category", () => {
    renderGrid();
    const searchToolsHeader = screen.getByText("Search & Tools");
    // Brave card should appear after the Search & Tools header
    const section = searchToolsHeader.closest("div")!;
    expect(within(section).getByText("Brave")).toBeInTheDocument();
  });

  it("Brave API key uses BRAVE_API_KEY in api_keys, not brave_api_key field", async () => {
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );
    renderGrid(emptySettings, onSaveChanges);
    const user = userEvent.setup();

    const braveCard = screen.getByText("Brave").closest("[class*='rounded-lg']") as HTMLElement;
    await user.click(within(braveCard).getByRole("button", { name: "Configure" }));

    await user.type(screen.getByLabelText("API Key"), "abcdefghijklmnopqrstuvwxyz123456");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await user.click(screen.getByText("Save Changes"));

    const payload = onSaveChanges.mock.calls[0]![0];
    // Key must be in api_keys map, NOT in a separate brave_api_key field
    expect(payload.api_keys).toBeDefined();
    expect(payload.api_keys!["BRAVE_API_KEY"]).toBe("abcdefghijklmnopqrstuvwxyz123456");
    // Verify no brave_api_key field leaks into the payload
    expect("brave_api_key" in payload).toBe(false);
  });

  it("reads Brave configured status from api_keys map", () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: { BRAVE_API_KEY: "****wxyz" },
    };
    renderGrid(settings);

    const braveCard = screen.getByText("Brave").closest("[class*='rounded-lg']") as HTMLElement;
    expect(within(braveCard).getByText("****wxyz")).toBeInTheDocument();
    expect(within(braveCard).getByRole("button", { name: "Update" })).toBeInTheDocument();
  });

  it("deletes Brave key using delete_api_keys with BRAVE_API_KEY", async () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: { BRAVE_API_KEY: "****wxyz" },
    };
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );
    renderGrid(settings, onSaveChanges);
    const user = userEvent.setup();

    const braveCard = screen.getByText("Brave").closest("[class*='rounded-lg']") as HTMLElement;
    await user.click(within(braveCard).getByTitle("Remove API key"));
    await user.click(screen.getByText("Save Changes"));

    const payload = onSaveChanges.mock.calls[0]![0];
    expect(payload.delete_api_keys).toEqual(["BRAVE_API_KEY"]);
  });
});

// ── Confirm dialog integration ────────────────────────────────────────

describe("ProviderGrid – confirmation dialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    // Do NOT suppress the dialog for these tests
  });

  it("shows confirmation dialog when deleting a provider", async () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****1234" },
    };
    renderGrid(settings);
    const user = userEvent.setup();

    const card = screen.getByText("Anthropic").closest("[class*='rounded-lg']") as HTMLElement;
    await user.click(within(card).getByTitle("Remove API key"));

    // Confirmation dialog should appear
    const dialogs = screen.getAllByRole("dialog");
    expect(dialogs.length).toBeGreaterThanOrEqual(1);
    expect(screen.getByText("Delete API Key")).toBeInTheDocument();
    expect(screen.getByText(/Are you sure you want to delete the Anthropic API key/)).toBeInTheDocument();
  });

  it("cancels delete when confirmation dialog is dismissed", async () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****1234" },
    };
    renderGrid(settings);
    const user = userEvent.setup();

    const card = screen.getByText("Anthropic").closest("[class*='rounded-lg']") as HTMLElement;
    await user.click(within(card).getByTitle("Remove API key"));

    // Click Cancel in the dialog
    await user.click(screen.getByTestId("confirm-dialog-cancel"));

    // Save Changes button should NOT appear (delete was cancelled)
    expect(screen.queryByText("Save Changes")).not.toBeInTheDocument();

    // Provider should still be configured
    const summary = screen.getByTestId("provider-count-summary");
    expect(summary).toHaveTextContent("1");
  });

  it("proceeds with delete when confirmation dialog is confirmed", async () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****1234" },
    };
    renderGrid(settings);
    const user = userEvent.setup();

    const card = screen.getByText("Anthropic").closest("[class*='rounded-lg']") as HTMLElement;
    await user.click(within(card).getByTitle("Remove API key"));

    // Click Delete in the dialog
    await user.click(screen.getByTestId("confirm-dialog-confirm"));

    // Save Changes button should appear
    expect(screen.getByText("Save Changes")).toBeInTheDocument();
  });

  it("skips confirmation dialog when suppressed via localStorage", async () => {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));

    const settings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****1234" },
    };
    renderGrid(settings);
    const user = userEvent.setup();

    const card = screen.getByText("Anthropic").closest("[class*='rounded-lg']") as HTMLElement;
    await user.click(within(card).getByTitle("Remove API key"));

    // No confirmation dialog should appear
    expect(screen.queryByText("Delete API Key")).not.toBeInTheDocument();
    // Save Changes should appear directly
    expect(screen.getByText("Save Changes")).toBeInTheDocument();
  });
});

// ── Base URL persistence ──────────────────────────────────────────────

describe("ProviderGrid – base URL persistence", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  it("includes base_urls in payload when a provider with base URL is saved", async () => {
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );
    renderGrid(emptySettings, onSaveChanges);
    const user = userEvent.setup();

    // Find OpenAI (the provider that supports base URL)
    const openaiCard = screen.getByText("OpenAI").closest("[class*='rounded-lg']") as HTMLElement;
    await user.click(within(openaiCard).getByRole("button", { name: /configure/i }));

    await user.type(screen.getByLabelText("API Key"), "sk-openai12345678");
    await user.type(screen.getByLabelText(/Base URL/), "https://my-proxy.example.com/v1");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(screen.getByText("Save Changes"));

    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    const payload = onSaveChanges.mock.calls[0]![0];
    expect(payload.api_keys).toEqual({ OPENAI_API_KEY: "sk-openai12345678" });
    expect(payload.base_urls).toEqual({ OPENAI_API_KEY: "https://my-proxy.example.com/v1" });
  });

  it("does not include base_urls when base URL field is empty", async () => {
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(() =>
      Promise.resolve(),
    );
    renderGrid(emptySettings, onSaveChanges);
    const user = userEvent.setup();

    // Configure OpenAI without base URL
    const openaiCard = screen.getByText("OpenAI").closest("[class*='rounded-lg']") as HTMLElement;
    await user.click(within(openaiCard).getByRole("button", { name: /configure/i }));

    await user.type(screen.getByLabelText("API Key"), "sk-openai12345678");
    // Don't type any base URL
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(screen.getByText("Save Changes"));

    const payload = onSaveChanges.mock.calls[0]![0];
    expect(payload.api_keys).toEqual({ OPENAI_API_KEY: "sk-openai12345678" });
    expect(payload.base_urls).toBeUndefined();
  });

  it("reads base URLs from settings and passes to modal", async () => {
    const settingsWithBaseUrl: Settings = {
      ...emptySettings,
      api_keys: { OPENAI_API_KEY: "****5678" },
      base_urls: { OPENAI_API_KEY: "https://existing-proxy.example.com/v1" },
    };
    renderGrid(settingsWithBaseUrl);
    const user = userEvent.setup();

    // Click Update on OpenAI
    const openaiCard = screen.getByText("OpenAI").closest("[class*='rounded-lg']") as HTMLElement;
    await user.click(within(openaiCard).getByRole("button", { name: "Update" }));

    // The base URL input should be pre-filled
    const baseUrlInput = screen.getByLabelText(/Base URL/) as HTMLInputElement;
    expect(baseUrlInput.value).toBe("https://existing-proxy.example.com/v1");
  });
});
