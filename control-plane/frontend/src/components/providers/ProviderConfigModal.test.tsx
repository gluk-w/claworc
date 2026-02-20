import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ProviderConfigModal from "./ProviderConfigModal";
import type { Provider } from "./providerData";

// ── Mocks ──────────────────────────────────────────────────────────────

vi.mock("react-hot-toast", () => ({
  default: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

const testProvider: Provider = {
  id: "anthropic",
  name: "Anthropic",
  envVarName: "ANTHROPIC_API_KEY",
  category: "Major Providers",
  description: "Claude models for advanced reasoning and analysis.",
  docsUrl: "https://console.anthropic.com/settings/keys",
  supportsBaseUrl: false,
};

const testProviderWithBaseUrl: Provider = {
  id: "openai",
  name: "OpenAI",
  envVarName: "OPENAI_API_KEY",
  category: "Major Providers",
  description: "GPT and o-series models.",
  docsUrl: "https://platform.openai.com/api-keys",
  supportsBaseUrl: true,
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
