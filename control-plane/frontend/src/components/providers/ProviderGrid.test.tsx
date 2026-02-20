import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import ProviderGrid from "./ProviderGrid";
import type { Settings, SettingsUpdatePayload } from "@/types/settings";

// ── Mocks ──────────────────────────────────────────────────────────────

const mockFetchSettings = vi.fn<() => Promise<Settings>>();
const mockUpdateSettings = vi.fn<(payload: SettingsUpdatePayload) => Promise<Settings>>();

vi.mock("@/api/settings", () => ({
  fetchSettings: (...args: unknown[]) => mockFetchSettings(...(args as [])),
  updateSettings: (...args: unknown[]) =>
    mockUpdateSettings(...(args as [SettingsUpdatePayload])),
}));

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

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false },
    },
  });
}

function renderGrid(settings: Settings = emptySettings) {
  mockFetchSettings.mockResolvedValue(settings);
  const qc = makeQueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <ProviderGrid />
    </QueryClientProvider>,
  );
}

// ── Tests ──────────────────────────────────────────────────────────────

describe("ProviderGrid – save / delete flow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  // ── Save flow ──

  it("shows no Save Changes button when there are no pending changes", async () => {
    renderGrid();
    // Wait for settings to load
    expect(
      await screen.findByText(/providers configured/i),
    ).toBeInTheDocument();
    expect(screen.queryByText("Save Changes")).not.toBeInTheDocument();
  });

  it("adds a provider key via modal and shows Save Changes button", async () => {
    renderGrid();
    const user = userEvent.setup();

    // Wait for grid to render and find the Anthropic card
    const configureButtons = await screen.findAllByRole("button", {
      name: /configure/i,
    });
    expect(configureButtons.length).toBeGreaterThan(0);

    // Click configure on the first provider (Anthropic)
    await user.click(configureButtons[0]!);

    // Modal should open – find the API key input
    const keyInput = screen.getByPlaceholderText("Enter API key");
    expect(keyInput).toBeInTheDocument();

    // Enter a key
    await user.type(keyInput, "sk-ant-test1234567890");

    // Click Save in the modal
    const modalSaveBtn = screen.getByRole("button", { name: "Save" });
    await user.click(modalSaveBtn);

    // Save Changes button should now appear
    expect(screen.getByText("Save Changes")).toBeInTheDocument();
  });

  it("sends correct PUT payload for a non-Brave provider", async () => {
    const settingsAfterSave: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****7890" },
    };
    mockUpdateSettings.mockResolvedValueOnce(settingsAfterSave);
    mockFetchSettings
      .mockResolvedValueOnce(emptySettings)
      .mockResolvedValueOnce(settingsAfterSave);

    renderGrid();
    const user = userEvent.setup();

    // Configure Anthropic
    const configureButtons = await screen.findAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByPlaceholderText("Enter API key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Click Save Changes
    await user.click(screen.getByText("Save Changes"));

    // Verify the payload sent to updateSettings
    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    expect(payload.api_keys).toEqual({
      ANTHROPIC_API_KEY: "sk-ant-test1234567890",
    });
    expect(payload.brave_api_key).toBeUndefined();
    expect(payload.delete_api_keys).toBeUndefined();
  });

  it("sends correct PUT payload for Brave (uses brave_api_key field)", async () => {
    const settingsAfterSave: Settings = {
      ...emptySettings,
      brave_api_key: "****qrst",
    };
    mockUpdateSettings.mockResolvedValueOnce(settingsAfterSave);
    mockFetchSettings
      .mockResolvedValueOnce(emptySettings)
      .mockResolvedValueOnce(settingsAfterSave);

    renderGrid();
    const user = userEvent.setup();

    // Find and click Configure on Brave
    const braveSection = await screen.findByText("Brave");
    const braveCard = braveSection.closest(
      "[class*='rounded-lg']",
    ) as HTMLElement;
    const configBtn = within(braveCard).getByRole("button", {
      name: /configure/i,
    });
    await user.click(configBtn);

    // Enter a 32-char alphanumeric key for Brave
    await user.type(
      screen.getByPlaceholderText("Enter API key"),
      "abcdefghijklmnopqrstuvwxyz123456",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Click Save Changes
    await user.click(screen.getByText("Save Changes"));

    // Verify the payload
    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    expect(payload.brave_api_key).toBe(
      "abcdefghijklmnopqrstuvwxyz123456",
    );
    // Brave should NOT appear in api_keys
    expect(payload.api_keys).toBeUndefined();
  });

  it("shows masked key after pending save in the card", async () => {
    renderGrid();
    const user = userEvent.setup();

    const configureButtons = await screen.findAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!); // Anthropic

    await user.type(
      screen.getByPlaceholderText("Enter API key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // The card should show the masked key: ****7890
    expect(screen.getByText("****7890")).toBeInTheDocument();
  });

  // ── Delete flow ──

  it("sends delete_api_keys when a configured provider is deleted", async () => {
    const initialSettings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****7890" },
    };
    const settingsAfterDelete: Settings = {
      ...emptySettings,
      api_keys: {},
    };
    mockUpdateSettings.mockResolvedValueOnce(settingsAfterDelete);
    mockFetchSettings
      .mockResolvedValueOnce(initialSettings)
      .mockResolvedValueOnce(settingsAfterDelete);

    renderGrid(initialSettings);
    const user = userEvent.setup();

    // Wait for Anthropic to show as configured (Update button instead of Configure)
    const updateBtn = await screen.findByRole("button", { name: "Update" });
    expect(updateBtn).toBeInTheDocument();

    // Find the delete button (trash icon) in the Anthropic card
    const anthropicCard = updateBtn.closest(
      "[class*='rounded-lg']",
    ) as HTMLElement;
    const deleteBtn = within(anthropicCard).getByTitle("Remove API key");
    await user.click(deleteBtn);

    // Save Changes should appear
    await user.click(screen.getByText("Save Changes"));

    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    expect(payload.delete_api_keys).toEqual(["ANTHROPIC_API_KEY"]);
    // Should not include api_keys since we only deleted
    expect(payload.api_keys).toBeUndefined();
  });

  it("clears pending state on successful save", async () => {
    mockUpdateSettings.mockResolvedValueOnce(emptySettings);
    mockFetchSettings
      .mockResolvedValueOnce(emptySettings)
      .mockResolvedValueOnce(emptySettings);

    renderGrid();
    const user = userEvent.setup();

    // Add a provider key
    const configureButtons = await screen.findAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByPlaceholderText("Enter API key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Save Changes should appear
    expect(screen.getByText("Save Changes")).toBeInTheDocument();

    // Click Save Changes
    await user.click(screen.getByText("Save Changes"));

    // After success, Save Changes button should disappear
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
    const settingsAfterUpdate: Settings = {
      ...emptySettings,
      api_keys: { GROQ_API_KEY: "****efgh" },
    };
    mockUpdateSettings.mockResolvedValueOnce(settingsAfterUpdate);
    mockFetchSettings
      .mockResolvedValueOnce(initialSettings)
      .mockResolvedValueOnce(settingsAfterUpdate);

    renderGrid(initialSettings);
    const user = userEvent.setup();

    // Wait for cards to render with configured state
    const updateButtons = await screen.findAllByRole("button", {
      name: "Update",
    });
    expect(updateButtons.length).toBe(2); // Anthropic + OpenAI

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
      screen.getByPlaceholderText("Enter API key"),
      "gsk_test1234567890abcdef",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    // Save all changes
    await user.click(screen.getByText("Save Changes"));

    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    expect(payload.api_keys).toEqual({
      GROQ_API_KEY: "gsk_test1234567890abcdef",
    });
    expect(payload.delete_api_keys).toEqual(["ANTHROPIC_API_KEY"]);
  });

  // ── Error handling ──

  it("does not clear pending state on save error", async () => {
    mockUpdateSettings.mockRejectedValueOnce({
      response: { data: { detail: "Server error" } },
    });

    renderGrid();
    const user = userEvent.setup();

    const configureButtons = await screen.findAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByPlaceholderText("Enter API key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(screen.getByText("Save Changes"));

    // Save Changes button should still be visible (pending state not cleared)
    await vi.waitFor(() => {
      expect(screen.getByText("Save Changes")).toBeInTheDocument();
    });
  });

  // ── Existing keys display on load ──

  it("displays existing configured providers with masked keys from server", async () => {
    const settingsWithKeys: Settings = {
      ...emptySettings,
      api_keys: {
        ANTHROPIC_API_KEY: "****7890",
        OPENAI_API_KEY: "****abcd",
      },
      brave_api_key: "****qrst",
    };

    renderGrid(settingsWithKeys);

    // Verify masked keys appear
    expect(await screen.findByText("****7890")).toBeInTheDocument();
    expect(screen.getByText("****abcd")).toBeInTheDocument();
    expect(screen.getByText("****qrst")).toBeInTheDocument();

    // Verify configured count
    expect(screen.getByText("3")).toBeInTheDocument();
  });

  // ── Delete Brave uses delete_api_keys ──

  it("deletes Brave key via delete_api_keys array", async () => {
    const initialSettings: Settings = {
      ...emptySettings,
      brave_api_key: "****qrst",
    };
    const settingsAfterDelete: Settings = { ...emptySettings };
    mockUpdateSettings.mockResolvedValueOnce(settingsAfterDelete);

    renderGrid(initialSettings);
    const user = userEvent.setup();

    // Wait for the masked Brave key to appear (proves settings loaded)
    const maskedKey = await screen.findByText("****qrst");
    const braveCard = maskedKey.closest(
      "[class*='rounded-lg']",
    ) as HTMLElement;
    const deleteBtn = within(braveCard).getByTitle("Remove API key");
    await user.click(deleteBtn);

    await user.click(screen.getByText("Save Changes"));

    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    expect(payload.delete_api_keys).toEqual(["BRAVE_API_KEY"]);
  });

  // ── Warning banner ──

  it("shows the global API key warning banner", async () => {
    renderGrid();
    expect(
      await screen.findByText(
        /changing global api keys will update all instances/i,
      ),
    ).toBeInTheDocument();
  });

  // ── Save button disabled states ──

  it("shows Saving... text while mutation is in progress", async () => {
    // Make updateSettings hang indefinitely
    mockUpdateSettings.mockImplementation(
      () => new Promise<Settings>(() => {}), // never resolves
    );

    renderGrid();
    const user = userEvent.setup();

    const configureButtons = await screen.findAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(
      screen.getByPlaceholderText("Enter API key"),
      "sk-ant-test1234567890",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(screen.getByText("Save Changes"));

    // Button should show Saving... once mutation is in flight
    expect(await screen.findByText("Saving...")).toBeInTheDocument();
  });
});
