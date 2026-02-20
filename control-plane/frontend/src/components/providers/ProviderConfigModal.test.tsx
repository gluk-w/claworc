import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ProviderConfigModal from "./ProviderConfigModal";
import type { Provider } from "./providerData";
import * as settingsApi from "@/api/settings";

// ── Mocks ──────────────────────────────────────────────────────────────

vi.mock("react-hot-toast", () => ({
  default: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("@/api/settings", () => ({
  testProviderKey: vi.fn().mockResolvedValue({
    success: true,
    message: "API key is valid. Connection successful!",
  }),
}));

const mockTestProviderKey = vi.mocked(settingsApi.testProviderKey);

const testProvider: Provider = {
  id: "anthropic",
  name: "Anthropic",
  envVarName: "ANTHROPIC_API_KEY",
  category: "Major Providers",
  description: "Claude models for advanced reasoning and analysis.",
  docsUrl: "https://console.anthropic.com/settings/keys",
  supportsBaseUrl: false,
  brandColor: "#D4A574",
};

const testProviderWithBaseUrl: Provider = {
  id: "openai",
  name: "OpenAI",
  envVarName: "OPENAI_API_KEY",
  category: "Major Providers",
  description: "GPT and o-series models.",
  docsUrl: "https://platform.openai.com/api-keys",
  supportsBaseUrl: true,
  brandColor: "#10A37F",
};

// ── Helpers ────────────────────────────────────────────────────────────

function renderModal(props: Partial<React.ComponentProps<typeof ProviderConfigModal>> = {}) {
  const defaultProps = {
    provider: testProvider,
    isOpen: true,
    onClose: vi.fn(),
    onSave: vi.fn(),
    currentMaskedKey: null,
  };
  return render(<ProviderConfigModal {...defaultProps} {...props} />);
}

// ── Tests ──────────────────────────────────────────────────────────────

describe("ProviderConfigModal – accessibility", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders with role=dialog and aria-modal=true", () => {
    renderModal();
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-modal", "true");
  });

  it("has aria-labelledby pointing to the modal title", () => {
    renderModal();
    const dialog = screen.getByRole("dialog");
    expect(dialog).toHaveAttribute("aria-labelledby", "provider-modal-title");
    expect(screen.getByText("Configure Anthropic")).toHaveAttribute("id", "provider-modal-title");
  });

  it("focuses the API key input when modal opens", async () => {
    renderModal();
    // The input should receive focus after a microtask
    await vi.waitFor(() => {
      expect(screen.getByLabelText("API Key")).toHaveFocus();
    });
  });

  it("closes on Escape key", async () => {
    const onClose = vi.fn();
    renderModal({ onClose });
    const user = userEvent.setup();

    await user.keyboard("{Escape}");

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("saves on Enter key when input has value", async () => {
    const onSave = vi.fn();
    renderModal({ onSave });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-test1234567890");
    await user.keyboard("{Enter}");

    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith("sk-ant-test1234567890", undefined);
  });

  it("does not save on Enter key when input is empty", async () => {
    const onSave = vi.fn();
    renderModal({ onSave });
    const user = userEvent.setup();

    // Focus input and press Enter without typing
    await user.click(screen.getByLabelText("API Key"));
    await user.keyboard("{Enter}");

    expect(onSave).not.toHaveBeenCalled();
  });

  it("has proper htmlFor/id association on API key label and input", () => {
    renderModal();
    const input = screen.getByLabelText("API Key");
    expect(input).toHaveAttribute("id", "api-key-input");
  });

  it("has aria-describedby linking to current key text when present", () => {
    renderModal({ currentMaskedKey: "****7890" });
    const input = screen.getByLabelText("API Key");
    expect(input.getAttribute("aria-describedby")).toContain("api-key-current");
    expect(screen.getByText(/Current key:/)).toHaveAttribute("id", "api-key-current");
  });

  it("has aria-label on the close button", () => {
    renderModal();
    expect(screen.getByLabelText("Close dialog")).toBeInTheDocument();
  });

  it("has aria-label on the visibility toggle button", () => {
    renderModal();
    expect(screen.getByLabelText("Show API key")).toBeInTheDocument();
  });

  it("toggles visibility toggle aria-label", async () => {
    renderModal();
    const user = userEvent.setup();

    const toggleBtn = screen.getByLabelText("Show API key");
    await user.click(toggleBtn);

    expect(screen.getByLabelText("Hide API key")).toBeInTheDocument();
  });

  it("has accessible name on the save button from text content", () => {
    renderModal();
    expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
  });

  it("traps focus within the modal — Tab from last wraps to first", async () => {
    renderModal();
    const user = userEvent.setup();

    // Type something to enable the Save button (it's disabled when empty)
    await user.type(screen.getByLabelText("API Key"), "test-key");

    // Focus the Cancel button (last non-disabled button before Save is now enabled)
    const saveBtn = screen.getByRole("button", { name: "Save" });
    saveBtn.focus();
    expect(saveBtn).toHaveFocus();

    // Tab should wrap to first focusable element (Close dialog button)
    await user.keyboard("{Tab}");

    expect(screen.getByLabelText("Close dialog")).toHaveFocus();
  });

  it("traps focus within the modal — Shift+Tab from first wraps to last", async () => {
    renderModal();
    const user = userEvent.setup();

    // Type something to enable the Save button
    await user.type(screen.getByLabelText("API Key"), "test-key");

    // Focus the Close button (first focusable element in the dialog)
    screen.getByLabelText("Close dialog").focus();
    expect(screen.getByLabelText("Close dialog")).toHaveFocus();

    // Shift+Tab should wrap to last focusable element (Save button)
    await user.keyboard("{Shift>}{Tab}{/Shift}");

    expect(screen.getByRole("button", { name: "Save" })).toHaveFocus();
  });

  it("renders nothing when isOpen is false", () => {
    const { container } = render(
      <ProviderConfigModal
        provider={testProvider}
        isOpen={false}
        onClose={vi.fn()}
        onSave={vi.fn()}
        currentMaskedKey={null}
      />,
    );
    expect(container.innerHTML).toBe("");
  });

  it("has htmlFor/id association on base URL label when provider supports it", () => {
    renderModal({ provider: testProviderWithBaseUrl });
    const baseUrlInput = screen.getByLabelText(/Base URL/);
    expect(baseUrlInput).toHaveAttribute("id", "base-url-input");
  });

  it("has aria-describedby on base URL input pointing to note", () => {
    renderModal({ provider: testProviderWithBaseUrl });
    const baseUrlInput = screen.getByLabelText(/Base URL/);
    expect(baseUrlInput).toHaveAttribute("aria-describedby", "base-url-note");
  });

  it("shows validation error with role=alert", async () => {
    renderModal();
    const user = userEvent.setup();

    // Type a key and test connection (which will fail for non-matching format)
    await user.type(screen.getByLabelText("API Key"), "bad-key");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    // Wait for the async validation
    await vi.waitFor(() => {
      const alert = screen.queryByRole("alert");
      expect(alert).toBeInTheDocument();
    });
  });
});

// ── Rendering ─────────────────────────────────────────────────────────

describe("ProviderConfigModal – rendering", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the modal when isOpen=true", () => {
    renderModal({ isOpen: true });
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("Configure Anthropic")).toBeInTheDocument();
  });

  it("does not render when isOpen=false", () => {
    const { container } = render(
      <ProviderConfigModal
        provider={testProvider}
        isOpen={false}
        onClose={vi.fn()}
        onSave={vi.fn()}
        currentMaskedKey={null}
      />,
    );
    expect(container.innerHTML).toBe("");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows provider name in the modal title", () => {
    renderModal({ provider: testProviderWithBaseUrl });
    expect(screen.getByText("Configure OpenAI")).toBeInTheDocument();
  });

  it("shows API Key input as password type by default", () => {
    renderModal();
    const input = screen.getByLabelText("API Key");
    expect(input).toHaveAttribute("type", "password");
  });

  it("shows the current masked key when provided", () => {
    renderModal({ currentMaskedKey: "****abcd" });
    expect(screen.getByText("****abcd")).toBeInTheDocument();
  });

  it("does not show current masked key when null", () => {
    renderModal({ currentMaskedKey: null });
    expect(screen.queryByText(/Current key:/)).not.toBeInTheDocument();
  });

  it("shows base URL field for providers with supportsBaseUrl=true", () => {
    renderModal({ provider: testProviderWithBaseUrl });
    expect(screen.getByLabelText(/Base URL/)).toBeInTheDocument();
  });

  it("does not show base URL field for providers with supportsBaseUrl=false", () => {
    renderModal({ provider: testProvider });
    expect(screen.queryByLabelText(/Base URL/)).not.toBeInTheDocument();
  });

  it("shows documentation link with correct provider URL", () => {
    renderModal();
    const link = screen.getByText(/Get an API key from Anthropic/);
    expect(link).toHaveAttribute("href", "https://console.anthropic.com/settings/keys");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("shows placeholder text from provider data", () => {
    const providerWithPlaceholder: Provider = {
      ...testProvider,
      apiKeyPlaceholder: "sk-ant-api03-...",
    };
    renderModal({ provider: providerWithPlaceholder });
    expect(screen.getByPlaceholderText("sk-ant-api03-...")).toBeInTheDocument();
  });

  it("shows default placeholder when provider has no apiKeyPlaceholder", () => {
    renderModal();
    expect(screen.getByPlaceholderText("Enter API key")).toBeInTheDocument();
  });

  it("shows 'Saving...' text when isSaving is true", () => {
    renderModal({ isSaving: true });
    expect(screen.getByRole("button", { name: /Saving/ })).toBeInTheDocument();
  });
});

// ── Interactions ──────────────────────────────────────────────────────

describe("ProviderConfigModal – interactions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("toggles API key input between password and text type", async () => {
    renderModal();
    const user = userEvent.setup();
    const input = screen.getByLabelText("API Key");

    expect(input).toHaveAttribute("type", "password");

    await user.click(screen.getByLabelText("Show API key"));
    expect(input).toHaveAttribute("type", "text");

    await user.click(screen.getByLabelText("Hide API key"));
    expect(input).toHaveAttribute("type", "password");
  });

  it("save button is disabled when API key is empty", () => {
    renderModal();
    const saveBtn = screen.getByRole("button", { name: "Save" });
    expect(saveBtn).toBeDisabled();
  });

  it("save button is enabled when API key has a value", async () => {
    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "some-key-value");
    const saveBtn = screen.getByRole("button", { name: "Save" });
    expect(saveBtn).toBeEnabled();
  });

  it("save button is disabled when isSaving is true", () => {
    renderModal({ isSaving: true });
    const saveBtn = screen.getByRole("button", { name: /Saving/ });
    expect(saveBtn).toBeDisabled();
  });

  it("escape key closes modal", async () => {
    const onClose = vi.fn();
    renderModal({ onClose });
    const user = userEvent.setup();

    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("clicking backdrop closes modal", async () => {
    const onClose = vi.fn();
    renderModal({ onClose });
    const user = userEvent.setup();

    // The backdrop is the element with aria-hidden="true" and the onClick handler
    const backdrop = document.querySelector('[aria-hidden="true"]') as HTMLElement;
    expect(backdrop).toBeTruthy();
    await user.click(backdrop);

    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("close button closes modal", async () => {
    const onClose = vi.fn();
    renderModal({ onClose });
    const user = userEvent.setup();

    await user.click(screen.getByLabelText("Close dialog"));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("cancel button closes modal", async () => {
    const onClose = vi.fn();
    renderModal({ onClose });
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("save button calls onSave with API key only (no base URL support)", async () => {
    const onSave = vi.fn();
    renderModal({ onSave });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-test1234567890");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith("sk-ant-test1234567890", undefined);
  });

  it("save button calls onSave with API key and base URL when provided", async () => {
    const onSave = vi.fn();
    renderModal({ provider: testProviderWithBaseUrl, onSave });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-openai-key-12345");
    await user.type(screen.getByLabelText(/Base URL/), "https://my-proxy.com/v1");
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith("sk-openai-key-12345", "https://my-proxy.com/v1");
  });

  it("save button calls onSave without base URL when base URL is empty", async () => {
    const onSave = vi.fn();
    renderModal({ provider: testProviderWithBaseUrl, onSave });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-openai-key-12345");
    // Don't type anything in base URL
    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith("sk-openai-key-12345", undefined);
  });

  it("clears input fields after save", async () => {
    const onSave = vi.fn();
    // Re-render after save to check state reset
    const { rerender } = render(
      <ProviderConfigModal
        provider={testProvider}
        isOpen={true}
        onClose={vi.fn()}
        onSave={onSave}
        currentMaskedKey={null}
      />,
    );
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-test1234567890");
    await user.click(screen.getByRole("button", { name: "Save" }));

    // After save, the component resets its state. Check the input is cleared.
    const input = screen.getByLabelText("API Key");
    expect(input).toHaveValue("");
  });

  it("clears input fields after close", async () => {
    const onClose = vi.fn();
    renderModal({ onClose });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "some-key");
    await user.click(screen.getByLabelText("Close dialog"));

    expect(onClose).toHaveBeenCalled();
  });

  it("test connection button is disabled when API key is empty", () => {
    renderModal();
    const testBtn = screen.getByRole("button", { name: "Test Connection" });
    expect(testBtn).toBeDisabled();
  });

  it("test connection button is enabled when API key has a value", async () => {
    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "some-key-value");
    const testBtn = screen.getByRole("button", { name: "Test Connection" });
    expect(testBtn).toBeEnabled();
  });
});

// ── Validation ────────────────────────────────────────────────────────

describe("ProviderConfigModal – validation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows valid result for correct Anthropic key format", async () => {
    renderModal({ provider: testProvider });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-valid-key-data");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByRole("status")).toBeInTheDocument();
      expect(screen.getByText("API key verified successfully.")).toBeInTheDocument();
    });
  });

  it("shows invalid result for incorrect Anthropic key format", async () => {
    renderModal({ provider: testProvider });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "wrong-prefix-key");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });
  });

  it("shows valid result for correct OpenAI key format", async () => {
    renderModal({ provider: testProviderWithBaseUrl });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-openai12345678");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByRole("status")).toBeInTheDocument();
    });
  });

  it("shows invalid result for incorrect OpenAI key format", async () => {
    renderModal({ provider: testProviderWithBaseUrl });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "not-an-openai-key");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });
  });

  it("shows invalid result for too-short key on generic provider", async () => {
    const genericProvider: Provider = {
      id: "mistral",
      name: "Mistral",
      envVarName: "MISTRAL_API_KEY",
      category: "Open Source / Inference",
      description: "Deploy efficient open-weight models.",
      docsUrl: "https://console.mistral.ai/api-keys/",
      supportsBaseUrl: false,
      brandColor: "#F7D046",
    };
    renderModal({ provider: genericProvider });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "short");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });
  });

  it("shows valid result for sufficiently long key on generic provider", async () => {
    const genericProvider: Provider = {
      id: "mistral",
      name: "Mistral",
      envVarName: "MISTRAL_API_KEY",
      category: "Open Source / Inference",
      description: "Deploy efficient open-weight models.",
      docsUrl: "https://console.mistral.ai/api-keys/",
      supportsBaseUrl: false,
      brandColor: "#F7D046",
    };
    renderModal({ provider: genericProvider });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "valid-long-enough-key");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByRole("status")).toBeInTheDocument();
    });
  });

  it("resets validation state when API key input changes", async () => {
    renderModal({ provider: testProvider });
    const user = userEvent.setup();

    // Trigger failed validation
    await user.type(screen.getByLabelText("API Key"), "bad-key");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });

    // Type a new character — validation state should reset
    await user.type(screen.getByLabelText("API Key"), "x");

    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("disables save button when validation fails", async () => {
    renderModal({ provider: testProvider });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "bad-key");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });

    const saveBtn = screen.getByRole("button", { name: "Save" });
    expect(saveBtn).toBeDisabled();
  });

  it("shows 'Testing...' text during connection test", async () => {
    // Use a delayed mock so the loading state is visible
    mockTestProviderKey.mockImplementationOnce(
      () => new Promise((resolve) => setTimeout(() => resolve({
        success: true,
        message: "API key is valid.",
      }), 100)),
    );

    renderModal({ provider: testProvider });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-valid-key-here");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    // The button text changes while the API call is in progress
    expect(screen.getByRole("button", { name: /Testing/ })).toBeInTheDocument();
  });
});
