import { describe, it, expect, vi, afterEach, beforeEach } from "vitest";
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

// ── Tests ──────────────────────────────────────────────────────────────

describe("ProviderGrid – success feedback animations", () => {
  beforeEach(() => {
    localStorage.clear();
    // Suppress confirmation dialog so delete animation tests work directly
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ "delete-provider": true }));
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  // ── Save button success animation ──

  describe("save button success animation", () => {
    it("shows 'Saved!' text with checkmark icon after successful save", async () => {
      const onSaveChanges = vi.fn(() => Promise.resolve());
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
        expect(screen.getByText("Saved!")).toBeInTheDocument();
      });
      expect(screen.getByTestId("save-success-icon")).toBeInTheDocument();
    });

    it("shows green background on save button after success", async () => {
      const onSaveChanges = vi.fn(() => Promise.resolve());
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
        const btn = screen.getByTestId("save-changes-button");
        expect(btn.className).toContain("bg-green-600");
      });
    });

    it("disables save button during success state", async () => {
      const onSaveChanges = vi.fn(() => Promise.resolve());
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
        const btn = screen.getByTestId("save-changes-button");
        expect(btn).toBeDisabled();
      });
    });

    it("hides the save button after success timeout", async () => {
      const onSaveChanges = vi.fn(() => Promise.resolve());
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

      // Verify success state appears
      await vi.waitFor(() => {
        expect(screen.getByText("Saved!")).toBeInTheDocument();
      });

      // Wait for the 1500ms timeout to expire
      await vi.waitFor(
        () => {
          expect(
            screen.queryByTestId("save-changes-button"),
          ).not.toBeInTheDocument();
        },
        { timeout: 3000 },
      );
    });

    it("does not show success state on save error", async () => {
      const onSaveChanges = vi.fn(() =>
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
      expect(screen.queryByText("Saved!")).not.toBeInTheDocument();
    });
  });

  // ── Card fade-in animation on add ──

  describe("card fade-in animation on add", () => {
    it("applies fade-in animation class to newly configured provider card", async () => {
      renderGrid();
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

      const anthropicCard = screen.getByLabelText(
        "Anthropic provider (configured)",
      );
      expect(anthropicCard.className).toContain("animate-provider-fade-in");
    });

    it("clears fade-in animation class after duration", async () => {
      renderGrid();
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

      const anthropicCard = screen.getByLabelText(
        "Anthropic provider (configured)",
      );
      expect(anthropicCard.className).toContain("animate-provider-fade-in");

      // Wait for 200ms animation duration to clear
      await vi.waitFor(
        () => {
          expect(anthropicCard.className).not.toContain(
            "animate-provider-fade-in",
          );
        },
        { timeout: 1000 },
      );
    });
  });

  // ── Card fade-out animation on delete ──

  describe("card fade-out animation on delete", () => {
    it("applies fade-out animation class when deleting a provider", async () => {
      renderGrid(settingsWithAnthropic);
      const user = userEvent.setup();

      const updateBtn = screen.getByRole("button", { name: "Update" });
      const anthropicCard = updateBtn.closest(
        "[class*='rounded-lg']",
      ) as HTMLElement;
      const deleteBtn = within(anthropicCard).getByTitle("Remove API key");
      await user.click(deleteBtn);

      const card = screen.getByLabelText("Anthropic provider");
      expect(card.className).toContain("animate-provider-fade-out");
    });

    it("clears fade-out animation class after duration", async () => {
      renderGrid(settingsWithAnthropic);
      const user = userEvent.setup();

      const updateBtn = screen.getByRole("button", { name: "Update" });
      const anthropicCard = updateBtn.closest(
        "[class*='rounded-lg']",
      ) as HTMLElement;
      const deleteBtn = within(anthropicCard).getByTitle("Remove API key");
      await user.click(deleteBtn);

      const card = screen.getByLabelText("Anthropic provider");
      expect(card.className).toContain("animate-provider-fade-out");

      // Wait for 400ms animation duration to clear
      await vi.waitFor(
        () => {
          expect(card.className).not.toContain("animate-provider-fade-out");
        },
        { timeout: 1000 },
      );
    });
  });

  // ── Save button transition utilities ──

  describe("save button uses correct transition utilities", () => {
    it("save button has transition-all duration-200 ease-in-out", async () => {
      renderGrid(emptySettings);
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

      const btn = screen.getByTestId("save-changes-button");
      expect(btn.className).toContain("transition-all");
      expect(btn.className).toContain("duration-200");
      expect(btn.className).toContain("ease-in-out");
    });
  });

  // ── Card uses ease-in-out ──

  describe("card transition utility", () => {
    it("provider cards have ease-in-out transition", () => {
      renderGrid();
      const card = screen.getByLabelText("Anthropic provider");
      expect(card.className).toContain("ease-in-out");
      expect(card.className).toContain("transition-all");
      expect(card.className).toContain("duration-200");
    });
  });
});
