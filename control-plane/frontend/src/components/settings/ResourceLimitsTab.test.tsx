import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import ResourceLimitsTab from "./ResourceLimitsTab";
import type { Settings, SettingsUpdatePayload } from "@/types/settings";

// ── Mocks ──────────────────────────────────────────────────────────────

const mockUpdateSettings =
  vi.fn<(payload: SettingsUpdatePayload) => Promise<Settings>>();

vi.mock("@/api/settings", () => ({
  fetchSettings: vi.fn(),
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

function renderTab(
  settings: Settings = emptySettings,
  onFieldChange = vi.fn(),
) {
  const qc = makeQueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <ResourceLimitsTab settings={settings} onFieldChange={onFieldChange} />
    </QueryClientProvider>,
  );
}

// ── Tests ──────────────────────────────────────────────────────────────

describe("ResourceLimitsTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the header and all 7 resource fields", () => {
    renderTab();

    expect(screen.getByText("Default Resource Limits")).toBeInTheDocument();
    expect(screen.getByText("Default CPU Request")).toBeInTheDocument();
    expect(screen.getByText("Default CPU Limit")).toBeInTheDocument();
    expect(screen.getByText("Default Memory Request")).toBeInTheDocument();
    expect(screen.getByText("Default Memory Limit")).toBeInTheDocument();
    expect(screen.getByText("Default Homebrew Storage")).toBeInTheDocument();
    expect(screen.getByText("Default Clawd Storage")).toBeInTheDocument();
    expect(screen.getByText("Default Browser Storage")).toBeInTheDocument();
  });

  it("renders a 2-column grid layout", () => {
    const { container } = renderTab();
    const grid = container.querySelector(".grid.grid-cols-2");
    expect(grid).toBeInTheDocument();
  });

  it("displays existing values from settings", () => {
    const settings: Settings = {
      ...emptySettings,
      default_cpu_request: "250m",
      default_memory_limit: "512Mi",
    };
    renderTab(settings);

    const inputs = screen.getAllByRole("textbox");
    const cpuInput = inputs.find(
      (input) => (input as HTMLInputElement).defaultValue === "250m",
    );
    const memInput = inputs.find(
      (input) => (input as HTMLInputElement).defaultValue === "512Mi",
    );
    expect(cpuInput).toBeDefined();
    expect(memInput).toBeDefined();
  });

  it("calls onFieldChange when a field value changes", async () => {
    const onFieldChange = vi.fn();
    renderTab(emptySettings, onFieldChange);
    const user = userEvent.setup();

    const inputs = screen.getAllByRole("textbox");
    await user.type(inputs[0]!, "500m");

    expect(onFieldChange).toHaveBeenCalledWith("default_cpu_request", "500m");
  });

  it("shows save button disabled when no changes are pending", () => {
    renderTab();
    const saveBtn = screen.getByRole("button", { name: "Save Settings" });
    expect(saveBtn).toBeDisabled();
  });

  it("enables save button after a field change and sends correct payload", async () => {
    mockUpdateSettings.mockResolvedValueOnce(emptySettings);
    renderTab();
    const user = userEvent.setup();

    const inputs = screen.getAllByRole("textbox");
    await user.type(inputs[0]!, "500m");

    const saveBtn = screen.getByRole("button", { name: "Save Settings" });
    expect(saveBtn).not.toBeDisabled();

    await user.click(saveBtn);

    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    expect(payload.default_cpu_request).toBe("500m");
  });

  it("clears pending state after successful save", async () => {
    mockUpdateSettings.mockResolvedValueOnce(emptySettings);
    renderTab();
    const user = userEvent.setup();

    const inputs = screen.getAllByRole("textbox");
    await user.type(inputs[0]!, "500m");

    await user.click(screen.getByRole("button", { name: "Save Settings" }));

    await vi.waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Save Settings" }),
      ).toBeDisabled();
    });
  });

  it("shows 'Saving...' while mutation is in progress", async () => {
    mockUpdateSettings.mockImplementation(
      () => new Promise<Settings>(() => {}),
    );
    renderTab();
    const user = userEvent.setup();

    const inputs = screen.getAllByRole("textbox");
    await user.type(inputs[0]!, "500m");

    await user.click(screen.getByRole("button", { name: "Save Settings" }));

    expect(await screen.findByText("Saving...")).toBeInTheDocument();
  });
});
