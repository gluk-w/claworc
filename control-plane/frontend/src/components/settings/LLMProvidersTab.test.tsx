import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import LLMProvidersTab from "./LLMProvidersTab";
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
  onSave = vi.fn(),
) {
  const qc = makeQueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <LLMProvidersTab settings={settings} onSave={onSave} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

// ── Tests ──────────────────────────────────────────────────────────────

describe("LLMProvidersTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the warning banner", () => {
    renderTab();
    expect(
      screen.getByText(
        /changing global api keys will update all instances/i,
      ),
    ).toBeInTheDocument();
  });

  it("renders the Provider Configuration header", () => {
    renderTab();
    expect(screen.getByText("Provider Configuration")).toBeInTheDocument();
  });

  it("renders the ProviderGrid with provider count", () => {
    renderTab();
    expect(screen.getByTestId("provider-count-summary")).toBeInTheDocument();
  });

  it("normalizes brave_api_key into api_keys for ProviderGrid display", () => {
    const settings: Settings = {
      ...emptySettings,
      brave_api_key: "****qrst",
    };
    renderTab(settings);

    // Brave should show as configured with the masked key
    expect(screen.getByText("****qrst")).toBeInTheDocument();
  });

  it("maps BRAVE_API_KEY back to brave_api_key field on save", async () => {
    mockUpdateSettings.mockResolvedValueOnce(emptySettings);
    renderTab();
    const user = userEvent.setup();

    // Configure Brave
    const braveSection = screen.getByText("Brave");
    const braveCard = braveSection.closest(
      "[class*='rounded-lg']",
    ) as HTMLElement;
    const configBtn = within(braveCard).getByRole("button", {
      name: /configure/i,
    });
    await user.click(configBtn);

    await user.type(
      screen.getByPlaceholderText("Enter API key"),
      "abcdefghijklmnopqrstuvwxyz123456",
    );
    await user.click(screen.getByRole("button", { name: "Save" }));

    await user.click(screen.getByText("Save Changes"));

    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    // Should be mapped to brave_api_key, NOT in api_keys
    expect(payload.brave_api_key).toBe(
      "abcdefghijklmnopqrstuvwxyz123456",
    );
    expect(payload.api_keys).toBeUndefined();
  });

  it("sends non-Brave keys in api_keys field on save", async () => {
    mockUpdateSettings.mockResolvedValueOnce(emptySettings);
    renderTab();
    const user = userEvent.setup();

    // Configure Anthropic (first configure button)
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

    expect(mockUpdateSettings).toHaveBeenCalledTimes(1);
    const payload = mockUpdateSettings.mock.calls[0]![0];
    expect(payload.api_keys).toEqual({
      ANTHROPIC_API_KEY: "sk-ant-test1234567890",
    });
    expect(payload.brave_api_key).toBeUndefined();
  });

  it("calls onSave callback after successful save", async () => {
    mockUpdateSettings.mockResolvedValueOnce(emptySettings);
    const onSave = vi.fn();
    renderTab(emptySettings, onSave);
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
      expect(onSave).toHaveBeenCalledTimes(1);
    });
  });

  it("displays existing api_keys and brave_api_key together", () => {
    const settings: Settings = {
      ...emptySettings,
      api_keys: {
        ANTHROPIC_API_KEY: "****7890",
        OPENAI_API_KEY: "****abcd",
      },
      brave_api_key: "****qrst",
    };
    renderTab(settings);

    expect(screen.getByText("****7890")).toBeInTheDocument();
    expect(screen.getByText("****abcd")).toBeInTheDocument();
    expect(screen.getByText("****qrst")).toBeInTheDocument();
    // 3 providers configured
    expect(screen.getByText("3")).toBeInTheDocument();
  });

  it("shows Saving... while mutation is in progress", async () => {
    mockUpdateSettings.mockImplementation(
      () => new Promise<Settings>(() => {}),
    );
    renderTab();
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

    expect(await screen.findByText("Saving...")).toBeInTheDocument();
  });
});
