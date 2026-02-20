import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ProviderGrid from "./ProviderGrid";
import type { Settings } from "@/types/settings";
import { PROVIDERS } from "./providerData";

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

function renderGrid(settings: Settings = emptySettings) {
  return render(
    <ProviderGrid
      settings={settings}
      onSaveChanges={() => Promise.resolve()}
      isSaving={false}
    />,
  );
}

// ── Tests ──────────────────────────────────────────────────────────────

describe("ProviderGrid – empty state", () => {
  it("shows empty state message when no providers are configured", () => {
    renderGrid();
    expect(screen.getByText("No providers configured yet")).toBeInTheDocument();
    expect(
      screen.getByText("Get started by adding your first API key"),
    ).toBeInTheDocument();
  });

  it("renders empty state with test id", () => {
    renderGrid();
    expect(screen.getByTestId("provider-empty-state")).toBeInTheDocument();
  });

  it("hides empty state when at least one provider is configured", () => {
    const settingsWithKey: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****7890" },
    };
    renderGrid(settingsWithKey);
    expect(
      screen.queryByText("No providers configured yet"),
    ).not.toBeInTheDocument();
    expect(screen.queryByTestId("provider-empty-state")).not.toBeInTheDocument();
  });

  it("hides empty state after a provider is configured via modal", async () => {
    renderGrid();
    const user = userEvent.setup();

    expect(screen.getByTestId("provider-empty-state")).toBeInTheDocument();

    // Configure Anthropic
    const configureButtons = screen.getAllByRole("button", {
      name: /configure/i,
    });
    await user.click(configureButtons[0]!);
    await user.type(screen.getByLabelText("API Key"), "sk-ant-test1234567890");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(screen.queryByTestId("provider-empty-state")).not.toBeInTheDocument();
  });
});

describe("ProviderGrid – provider descriptions", () => {
  it("renders actionable descriptions for all providers", () => {
    renderGrid();
    for (const provider of PROVIDERS) {
      expect(screen.getByText(provider.description)).toBeInTheDocument();
    }
  });
});
