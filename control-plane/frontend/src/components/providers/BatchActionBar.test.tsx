import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import BatchActionBar from "./BatchActionBar";
import type { Provider } from "./providerData";
import { testProviderKey } from "@/api/settings";

vi.mock("@/api/settings", () => ({
  testProviderKey: vi.fn(),
  fetchProviderAnalytics: vi.fn().mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" }),
}));

const mockedTestProviderKey = vi.mocked(testProviderKey);

const anthropic: Provider = {
  id: "anthropic",
  name: "Anthropic",
  envVarName: "ANTHROPIC_API_KEY",
  category: "Major Providers",
  description: "Claude models",
  docsUrl: "https://console.anthropic.com/settings/keys",
  supportsBaseUrl: false,
  brandColor: "#D4A574",
};

const openai: Provider = {
  id: "openai",
  name: "OpenAI",
  envVarName: "OPENAI_API_KEY",
  category: "Major Providers",
  description: "GPT models",
  docsUrl: "https://platform.openai.com/api-keys",
  supportsBaseUrl: true,
  brandColor: "#10A37F",
};

const groq: Provider = {
  id: "groq",
  name: "Groq",
  envVarName: "GROQ_API_KEY",
  category: "Open Source / Inference",
  description: "Fast inference",
  docsUrl: "https://console.groq.com/keys",
  supportsBaseUrl: false,
  brandColor: "#F55036",
};

function renderBar(overrides: Partial<React.ComponentProps<typeof BatchActionBar>> = {}) {
  const defaults = {
    selectedProviders: [anthropic, openai],
    configuredKeys: { ANTHROPIC_API_KEY: "****7890", OPENAI_API_KEY: "****abcd" },
    onDeleteSelected: vi.fn(),
    onClearSelection: vi.fn(),
  };
  return render(<BatchActionBar {...defaults} {...overrides} />);
}

describe("BatchActionBar – rendering", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows the correct selection count", () => {
    renderBar();
    expect(screen.getByTestId("batch-selection-count")).toHaveTextContent("2 providers selected");
  });

  it("shows singular form for 1 provider", () => {
    renderBar({ selectedProviders: [anthropic] });
    expect(screen.getByTestId("batch-selection-count")).toHaveTextContent("1 provider selected");
  });

  it("renders all action buttons", () => {
    renderBar();
    expect(screen.getByTestId("batch-test-all")).toBeInTheDocument();
    expect(screen.getByTestId("batch-export-keys")).toBeInTheDocument();
    expect(screen.getByTestId("batch-delete-selected")).toBeInTheDocument();
    expect(screen.getByTestId("batch-clear-selection")).toBeInTheDocument();
  });

  it("disables Test All when no configured providers selected", () => {
    renderBar({
      selectedProviders: [groq],
      configuredKeys: {},
    });
    expect(screen.getByTestId("batch-test-all")).toBeDisabled();
  });
});

describe("BatchActionBar – actions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("calls onDeleteSelected when Delete Selected is clicked", async () => {
    const onDeleteSelected = vi.fn();
    renderBar({ onDeleteSelected });
    const user = userEvent.setup();

    await user.click(screen.getByTestId("batch-delete-selected"));
    expect(onDeleteSelected).toHaveBeenCalledTimes(1);
  });

  it("calls onClearSelection when Clear is clicked", async () => {
    const onClearSelection = vi.fn();
    renderBar({ onClearSelection });
    const user = userEvent.setup();

    await user.click(screen.getByTestId("batch-clear-selection"));
    expect(onClearSelection).toHaveBeenCalledTimes(1);
  });
});

describe("BatchActionBar – export keys", () => {
  let originalCreateObjectURL: typeof URL.createObjectURL;
  let originalRevokeObjectURL: typeof URL.revokeObjectURL;

  beforeEach(() => {
    vi.clearAllMocks();
    originalCreateObjectURL = URL.createObjectURL;
    originalRevokeObjectURL = URL.revokeObjectURL;
  });

  afterEach(() => {
    URL.createObjectURL = originalCreateObjectURL;
    URL.revokeObjectURL = originalRevokeObjectURL;
    vi.restoreAllMocks();
  });

  it("triggers download with .env content when Export Keys is clicked", async () => {
    // Render first so createElement mock doesn't interfere with React rendering
    renderBar();
    const user = userEvent.setup();

    URL.createObjectURL = vi.fn(() => "blob:test-url");
    URL.revokeObjectURL = vi.fn();

    const clickSpy = vi.fn();
    const originalCreateElement = document.createElement.bind(document);
    vi.spyOn(document, "createElement").mockImplementation((tag: string) => {
      if (tag === "a") {
        return { href: "", download: "", click: clickSpy } as unknown as HTMLAnchorElement;
      }
      return originalCreateElement(tag);
    });
    vi.spyOn(document.body, "appendChild").mockImplementation((node) => node);
    vi.spyOn(document.body, "removeChild").mockImplementation((node) => node);

    await user.click(screen.getByTestId("batch-export-keys"));

    expect(URL.createObjectURL).toHaveBeenCalled();
    expect(clickSpy).toHaveBeenCalled();
    expect(URL.revokeObjectURL).toHaveBeenCalled();
  });

  it("creates a Blob with provider keys for export", async () => {
    renderBar({
      selectedProviders: [anthropic, groq],
      configuredKeys: { ANTHROPIC_API_KEY: "****7890" },
    });
    const user = userEvent.setup();

    URL.createObjectURL = vi.fn(() => "blob:test-url");
    URL.revokeObjectURL = vi.fn();

    const originalCreateElement = document.createElement.bind(document);
    vi.spyOn(document, "createElement").mockImplementation((tag: string) => {
      if (tag === "a") {
        return { href: "", download: "", click: vi.fn() } as unknown as HTMLAnchorElement;
      }
      return originalCreateElement(tag);
    });
    vi.spyOn(document.body, "appendChild").mockImplementation((node) => node);
    vi.spyOn(document.body, "removeChild").mockImplementation((node) => node);

    await user.click(screen.getByTestId("batch-export-keys"));

    expect(URL.createObjectURL).toHaveBeenCalled();
    const blobArg = (URL.createObjectURL as ReturnType<typeof vi.fn>).mock.calls[0]?.[0];
    expect(blobArg).toBeInstanceOf(Blob);
  });
});

describe("BatchActionBar – test all", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("runs connection tests in parallel and shows results", async () => {
    mockedTestProviderKey
      .mockResolvedValueOnce({ success: true, message: "API key verified successfully." })
      .mockResolvedValueOnce({ success: false, message: "Invalid API key", details: "Unauthorized" });

    renderBar();
    const user = userEvent.setup();

    await user.click(screen.getByTestId("batch-test-all"));

    // Wait for results to appear
    const results = await screen.findByTestId("batch-test-results");
    expect(results).toBeInTheDocument();

    expect(screen.getByTestId("batch-test-summary")).toHaveTextContent("1 passed, 1 failed");

    const anthropicResult = screen.getByTestId("batch-test-result-anthropic");
    expect(within(anthropicResult).getByText("Anthropic:")).toBeInTheDocument();

    const openaiResult = screen.getByTestId("batch-test-result-openai");
    expect(within(openaiResult).getByText("OpenAI:")).toBeInTheDocument();
  });

  it("shows all passed when all tests succeed", async () => {
    mockedTestProviderKey
      .mockResolvedValueOnce({ success: true, message: "OK" })
      .mockResolvedValueOnce({ success: true, message: "OK" });

    renderBar();
    const user = userEvent.setup();

    await user.click(screen.getByTestId("batch-test-all"));

    const results = await screen.findByTestId("batch-test-results");
    expect(results).toBeInTheDocument();
    expect(screen.getByTestId("batch-test-summary")).toHaveTextContent("2 passed, 0 failed");
  });

  it("handles network errors gracefully", async () => {
    mockedTestProviderKey
      .mockResolvedValueOnce({ success: true, message: "OK" })
      .mockRejectedValueOnce(new Error("Network error"));

    renderBar();
    const user = userEvent.setup();

    await user.click(screen.getByTestId("batch-test-all"));

    const results = await screen.findByTestId("batch-test-results");
    expect(results).toBeInTheDocument();
    expect(screen.getByTestId("batch-test-summary")).toHaveTextContent("1 passed, 1 failed");
  });

  it("dismisses test results when Dismiss is clicked", async () => {
    mockedTestProviderKey
      .mockResolvedValueOnce({ success: true, message: "OK" })
      .mockResolvedValueOnce({ success: true, message: "OK" });

    renderBar();
    const user = userEvent.setup();

    await user.click(screen.getByTestId("batch-test-all"));
    await screen.findByTestId("batch-test-results");

    await user.click(screen.getByTestId("batch-test-dismiss"));
    expect(screen.queryByTestId("batch-test-results")).not.toBeInTheDocument();
  });

  it("only tests configured providers, not unconfigured ones", async () => {
    mockedTestProviderKey.mockResolvedValue({ success: true, message: "OK" });

    renderBar({
      selectedProviders: [anthropic, groq],
      configuredKeys: { ANTHROPIC_API_KEY: "****7890" },
    });
    const user = userEvent.setup();

    await user.click(screen.getByTestId("batch-test-all"));

    await screen.findByTestId("batch-test-results");

    // Only Anthropic should be tested (Groq is not configured)
    expect(mockedTestProviderKey).toHaveBeenCalledTimes(1);
    expect(mockedTestProviderKey).toHaveBeenCalledWith({
      provider: "anthropic",
      api_key: "****7890",
    });
  });

  it("shows Testing... text while tests are running", async () => {
    // Make the test hang to check loading state
    let resolveTest: (v: { success: boolean; message: string }) => void;
    mockedTestProviderKey.mockImplementation(
      () => new Promise((resolve) => { resolveTest = resolve; }),
    );

    renderBar({ selectedProviders: [anthropic], configuredKeys: { ANTHROPIC_API_KEY: "****7890" } });
    const user = userEvent.setup();

    await user.click(screen.getByTestId("batch-test-all"));

    expect(screen.getByTestId("batch-test-all")).toHaveTextContent("Testing...");
    expect(screen.getByTestId("batch-test-all")).toBeDisabled();

    // Resolve to clean up
    resolveTest!({ success: true, message: "OK" });
    await screen.findByTestId("batch-test-results");
  });
});
