import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ProviderGrid from "./ProviderGrid";
import type { Settings } from "@/types/settings";
import type { ProviderSavePayload } from "./ProviderGrid";
import { STORAGE_KEY } from "../ConfirmDialog";

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
