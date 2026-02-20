import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import ProviderGrid from "./ProviderGrid";
import type { Settings } from "@/types/settings";
import type { ProviderSavePayload } from "./ProviderGrid";
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

const settingsWithAnthropic: Settings = {
  ...emptySettings,
  api_keys: { ANTHROPIC_API_KEY: "****7890" },
};

const settingsWithTwo: Settings = {
  ...emptySettings,
  api_keys: {
    ANTHROPIC_API_KEY: "****7890",
    OPENAI_API_KEY: "****abcd",
  },
};

function renderGrid(
  settings: Settings = emptySettings,
  onSaveChanges: (payload: ProviderSavePayload) => Promise<void> = () =>
    Promise.resolve(),
  isSaving = false,
) {
  return render(
    <MemoryRouter>
      <ProviderGrid
        settings={settings}
        onSaveChanges={onSaveChanges}
        isSaving={isSaving}
      />
    </MemoryRouter>,
  );
}

function getDeleteButton(cardLabel: string) {
  const card = screen.getByLabelText(cardLabel);
  return within(card).getByTitle("Remove API key");
}

// ── Tests ──────────────────────────────────────────────────────────────

describe("ProviderGrid – delete confirmation dialog", () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it("shows confirmation dialog when clicking delete on a configured provider", async () => {
    renderGrid(settingsWithAnthropic);
    const user = userEvent.setup();

    await user.click(getDeleteButton("Anthropic provider (configured)"));

    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("Delete API Key")).toBeInTheDocument();
    expect(
      screen.getByText(
        "Are you sure you want to delete the Anthropic API key? This will affect all instances without overrides.",
      ),
    ).toBeInTheDocument();
  });

  it("shows provider name in the confirmation message", async () => {
    renderGrid(settingsWithTwo);
    const user = userEvent.setup();

    await user.click(getDeleteButton("OpenAI provider (configured)"));

    expect(
      screen.getByText(
        "Are you sure you want to delete the OpenAI API key? This will affect all instances without overrides.",
      ),
    ).toBeInTheDocument();
  });

  it("has red Delete button and gray Cancel button", async () => {
    renderGrid(settingsWithAnthropic);
    const user = userEvent.setup();

    await user.click(getDeleteButton("Anthropic provider (configured)"));

    const confirmBtn = screen.getByTestId("confirm-dialog-confirm");
    const cancelBtn = screen.getByTestId("confirm-dialog-cancel");
    expect(confirmBtn).toHaveTextContent("Delete");
    expect(confirmBtn.className).toContain("bg-red-600");
    expect(cancelBtn).toHaveTextContent("Cancel");
    expect(cancelBtn.className).toContain("text-gray-700");
  });

  it("does not delete the provider when Cancel is clicked", async () => {
    renderGrid(settingsWithAnthropic);
    const user = userEvent.setup();

    await user.click(getDeleteButton("Anthropic provider (configured)"));
    await user.click(screen.getByTestId("confirm-dialog-cancel"));

    // Dialog should be closed
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    // Provider should still be configured
    expect(screen.getByLabelText("Anthropic provider (configured)")).toBeInTheDocument();
    // No Save Changes button (no pending changes)
    expect(screen.queryByTestId("save-changes-button")).not.toBeInTheDocument();
  });

  it("deletes the provider when Delete is confirmed", async () => {
    renderGrid(settingsWithAnthropic);
    const user = userEvent.setup();

    await user.click(getDeleteButton("Anthropic provider (configured)"));
    await user.click(screen.getByTestId("confirm-dialog-confirm"));

    // Dialog should be closed
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    // Provider should no longer be configured
    expect(screen.getByLabelText("Anthropic provider")).toBeInTheDocument();
    // Save Changes button should appear
    expect(screen.getByTestId("save-changes-button")).toBeInTheDocument();
  });

  it("closes confirmation dialog when Escape is pressed", async () => {
    renderGrid(settingsWithAnthropic);
    const user = userEvent.setup();

    await user.click(getDeleteButton("Anthropic provider (configured)"));
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    await user.keyboard("{Escape}");

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    // Provider should still be configured
    expect(screen.getByLabelText("Anthropic provider (configured)")).toBeInTheDocument();
  });

  it("closes confirmation dialog when backdrop is clicked", async () => {
    renderGrid(settingsWithAnthropic);
    const user = userEvent.setup();

    await user.click(getDeleteButton("Anthropic provider (configured)"));
    expect(screen.getByRole("dialog")).toBeInTheDocument();

    await user.click(screen.getByTestId("confirm-dialog-backdrop"));

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows 'Don't ask again' checkbox", async () => {
    renderGrid(settingsWithAnthropic);
    const user = userEvent.setup();

    await user.click(getDeleteButton("Anthropic provider (configured)"));

    expect(screen.getByTestId("dont-ask-again-checkbox")).toBeInTheDocument();
    expect(screen.getByText("Don't ask again")).toBeInTheDocument();
  });

  it("skips confirmation dialog when 'Don't ask again' was previously checked", async () => {
    renderGrid(settingsWithAnthropic);
    const user = userEvent.setup();

    // First delete with "Don't ask again" checked
    await user.click(getDeleteButton("Anthropic provider (configured)"));
    await user.click(screen.getByTestId("dont-ask-again-checkbox"));
    await user.click(screen.getByTestId("confirm-dialog-confirm"));

    // Provider should be deleted immediately
    expect(screen.getByLabelText("Anthropic provider")).toBeInTheDocument();
  });

  it("skips confirmation when suppression is set in localStorage", async () => {
    // Pre-set the suppression
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));

    renderGrid(settingsWithAnthropic);
    const user = userEvent.setup();

    await user.click(getDeleteButton("Anthropic provider (configured)"));

    // Dialog should NOT appear
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    // Provider should be deleted directly
    expect(screen.getByLabelText("Anthropic provider")).toBeInTheDocument();
    // Save Changes should appear
    expect(screen.getByTestId("save-changes-button")).toBeInTheDocument();
  });

  it("applies fade-out animation after confirming delete", async () => {
    renderGrid(settingsWithAnthropic);
    const user = userEvent.setup();

    await user.click(getDeleteButton("Anthropic provider (configured)"));
    await user.click(screen.getByTestId("confirm-dialog-confirm"));

    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("animate-provider-fade-out");
  });
});
