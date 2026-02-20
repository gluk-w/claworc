import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import ProviderGrid from "./ProviderGrid";
import type { Settings } from "@/types/settings";
import type { ProviderSavePayload } from "./ProviderGrid";
import { PROVIDERS } from "./providerData";
import { STORAGE_KEY } from "../ConfirmDialog";

vi.mock("@/api/settings", () => ({
  testProviderKey: vi.fn(() =>
    Promise.resolve({ success: true, message: "OK" }),
  ),
  fetchProviderAnalytics: vi.fn().mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" }),
}));

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
    <MemoryRouter initialEntries={["/settings"]}>
      <ProviderGrid
        settings={settings}
        onSaveChanges={onSaveChanges}
        isSaving={isSaving}
      />
    </MemoryRouter>,
  );
}

describe("ProviderGrid – batch selection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ "delete-provider": true }),
    );
  });

  it("renders a Select All checkbox", () => {
    renderGrid();
    expect(screen.getByTestId("select-all-checkbox")).toBeInTheDocument();
    expect(screen.getByLabelText("Select all providers")).toBeInTheDocument();
  });

  it("selects all providers when Select All is clicked", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));

    // Batch action bar should appear
    expect(screen.getByTestId("batch-action-bar")).toBeInTheDocument();
    expect(screen.getByTestId("batch-selection-count")).toHaveTextContent(
      `${PROVIDERS.length} providers selected`,
    );
  });

  it("deselects all when Select All is clicked again", async () => {
    renderGrid();
    const user = userEvent.setup();

    // Select all
    await user.click(screen.getByTestId("select-all-checkbox"));
    expect(screen.getByTestId("batch-action-bar")).toBeInTheDocument();

    // Deselect all
    await user.click(screen.getByTestId("select-all-checkbox"));
    expect(screen.queryByTestId("batch-action-bar")).not.toBeInTheDocument();
  });

  it("shows checkboxes on cards when any provider is selected", async () => {
    renderGrid();
    const user = userEvent.setup();

    // Initially no card checkboxes visible
    expect(screen.queryByTestId("select-anthropic")).not.toBeInTheDocument();

    // Click Select All to enter selection mode
    await user.click(screen.getByTestId("select-all-checkbox"));

    // Card checkboxes should now be visible
    expect(screen.getByTestId("select-anthropic")).toBeInTheDocument();
    expect(screen.getByTestId("select-openai")).toBeInTheDocument();
  });

  it("toggles individual card selection via checkbox", async () => {
    renderGrid();
    const user = userEvent.setup();

    // Select all to enter selection mode
    await user.click(screen.getByTestId("select-all-checkbox"));
    expect(screen.getByTestId("batch-selection-count")).toHaveTextContent(
      `${PROVIDERS.length} providers selected`,
    );

    // Deselect one
    await user.click(screen.getByTestId("select-anthropic"));
    expect(screen.getByTestId("batch-selection-count")).toHaveTextContent(
      `${PROVIDERS.length - 1} providers selected`,
    );
  });

  it("clears selection when Clear button is clicked", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));
    expect(screen.getByTestId("batch-action-bar")).toBeInTheDocument();

    await user.click(screen.getByTestId("batch-clear-selection"));
    expect(screen.queryByTestId("batch-action-bar")).not.toBeInTheDocument();
  });
});

describe("ProviderGrid – batch delete", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ "delete-provider": true }),
    );
  });

  it("batch deletes configured providers and shows Save Changes", async () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: {
        ANTHROPIC_API_KEY: "****7890",
        OPENAI_API_KEY: "****abcd",
        GROQ_API_KEY: "****1111",
      },
    };
    const onSaveChanges = vi.fn<(p: ProviderSavePayload) => Promise<void>>(
      () => Promise.resolve(),
    );

    renderGrid(settings, onSaveChanges);
    const user = userEvent.setup();

    // Select all to enter selection mode
    await user.click(screen.getByTestId("select-all-checkbox"));

    // Click Delete Selected
    await user.click(screen.getByTestId("batch-delete-selected"));

    // Save Changes should be available (confirmation suppressed)
    expect(screen.getByText("Save Changes")).toBeInTheDocument();

    await user.click(screen.getByText("Save Changes"));

    expect(onSaveChanges).toHaveBeenCalledTimes(1);
    const payload = onSaveChanges.mock.calls[0]![0];
    expect(payload.delete_api_keys).toContain("ANTHROPIC_API_KEY");
    expect(payload.delete_api_keys).toContain("OPENAI_API_KEY");
    expect(payload.delete_api_keys).toContain("GROQ_API_KEY");
  });

  it("shows confirmation dialog for batch delete when not suppressed", async () => {
    localStorage.clear(); // Don't suppress

    const settings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****7890" },
    };
    renderGrid(settings);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));
    await user.click(screen.getByTestId("batch-delete-selected"));

    // Confirmation dialog should appear
    expect(screen.getByText("Delete Selected API Keys")).toBeInTheDocument();
  });

  it("cancels batch delete when confirmation is dismissed", async () => {
    localStorage.clear(); // Don't suppress

    const settings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****7890" },
    };
    renderGrid(settings);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));
    await user.click(screen.getByTestId("batch-delete-selected"));

    // Cancel
    await user.click(screen.getByTestId("confirm-dialog-cancel"));

    // Save Changes should NOT appear
    expect(screen.queryByText("Save Changes")).not.toBeInTheDocument();
  });

  it("proceeds with batch delete when confirmation is accepted", async () => {
    localStorage.clear(); // Don't suppress

    const settings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****7890", OPENAI_API_KEY: "****abcd" },
    };
    renderGrid(settings);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));
    await user.click(screen.getByTestId("batch-delete-selected"));

    // Confirm
    await user.click(screen.getByTestId("confirm-dialog-confirm"));

    // Save Changes should appear
    expect(screen.getByText("Save Changes")).toBeInTheDocument();
  });
});

describe("ProviderGrid – batch export", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ "delete-provider": true }),
    );
  });

  it("Export Keys button is visible when providers are selected", async () => {
    renderGrid();
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));
    expect(screen.getByTestId("batch-export-keys")).toBeInTheDocument();
  });
});

describe("ProviderGrid – batch test", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    localStorage.setItem(
      STORAGE_KEY,
      JSON.stringify({ "delete-provider": true }),
    );
  });

  it("Test All button is visible when providers are selected", async () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: { ANTHROPIC_API_KEY: "****7890" },
    };
    renderGrid(settings);
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));
    expect(screen.getByTestId("batch-test-all")).toBeInTheDocument();
  });

  it("Test All is disabled when no configured providers are selected", async () => {
    renderGrid(); // no keys configured
    const user = userEvent.setup();

    await user.click(screen.getByTestId("select-all-checkbox"));
    expect(screen.getByTestId("batch-test-all")).toBeDisabled();
  });
});
