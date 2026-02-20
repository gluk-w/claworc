import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import AgentImageTab from "./AgentImageTab";
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
      <AgentImageTab settings={settings} onFieldChange={onFieldChange} />
    </QueryClientProvider>,
  );
}

// ── Tests ──────────────────────────────────────────────────────────────

describe("AgentImageTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the header and both fields", () => {
    renderTab();

    expect(screen.getByText("Agent Image")).toBeInTheDocument();
    expect(screen.getByText("Default Container Image")).toBeInTheDocument();
    expect(screen.getByText("Default VNC Resolution")).toBeInTheDocument();
  });

  it("renders fields in a vertical layout", () => {
    const { container } = renderTab();
    const layout = container.querySelector(".space-y-4");
    expect(layout).toBeInTheDocument();
  });

  it("displays existing values from settings", () => {
    const settings: Settings = {
      ...emptySettings,
      default_container_image: "myregistry/agent:v2",
      default_vnc_resolution: "2560x1440",
    };
    renderTab(settings);

    const inputs = screen.getAllByRole("textbox");
    const imageInput = inputs.find(
      (input) =>
        (input as HTMLInputElement).defaultValue === "myregistry/agent:v2",
    );
    const vncInput = inputs.find(
      (input) => (input as HTMLInputElement).defaultValue === "2560x1440",
    );
    expect(imageInput).toBeDefined();
    expect(vncInput).toBeDefined();
  });

  it("defaults VNC resolution to 1920x1080 when not set", () => {
    renderTab();

    const inputs = screen.getAllByRole("textbox");
    const vncInput = inputs.find(
      (input) => (input as HTMLInputElement).defaultValue === "1920x1080",
    );
    expect(vncInput).toBeDefined();
  });

  it("calls onFieldChange when container image changes", async () => {
    const onFieldChange = vi.fn();
    renderTab(emptySettings, onFieldChange);
    const user = userEvent.setup();

    const inputs = screen.getAllByRole("textbox");
    await user.type(inputs[0]!, "new-image:latest");

    expect(onFieldChange).toHaveBeenCalledWith(
      "default_container_image",
      "new-image:latest",
    );
  });

  it("calls onFieldChange when VNC resolution changes", async () => {
    const onFieldChange = vi.fn();
    renderTab(emptySettings, onFieldChange);
    const user = userEvent.setup();

    const inputs = screen.getAllByRole("textbox");
    await user.clear(inputs[1]!);
    await user.type(inputs[1]!, "1280x720");

    expect(onFieldChange).toHaveBeenCalledWith(
      "default_vnc_resolution",
      "1280x720",
    );
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
    await user.type(inputs[0]!, "custom-image:v3");

    const saveBtn = screen.getByRole("button", { name: "Save Settings" });
    expect(saveBtn).not.toBeDisabled();

    await user.click(saveBtn);

    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    expect(payload.default_container_image).toBe("custom-image:v3");
  });

  it("clears pending state after successful save", async () => {
    mockUpdateSettings.mockResolvedValueOnce(emptySettings);
    renderTab();
    const user = userEvent.setup();

    const inputs = screen.getAllByRole("textbox");
    await user.type(inputs[0]!, "img:v1");

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
    await user.type(inputs[0]!, "img:v1");

    await user.click(screen.getByRole("button", { name: "Save Settings" }));

    expect(await screen.findByText("Saving...")).toBeInTheDocument();
  });
});
