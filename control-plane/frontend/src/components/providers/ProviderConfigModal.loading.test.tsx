import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import ProviderConfigModal from "./ProviderConfigModal";
import type { Provider } from "./providerData";

vi.mock("react-hot-toast", () => ({
  default: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

vi.mock("@/api/settings", () => ({
  testProviderKey: vi.fn().mockResolvedValue({
    success: true,
    message: "API key is valid.",
  }),
}));

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

function renderModal(
  props: Partial<React.ComponentProps<typeof ProviderConfigModal>> = {},
) {
  const defaultProps = {
    provider: testProvider,
    isOpen: true,
    onClose: vi.fn(),
    onSave: vi.fn(),
    currentMaskedKey: null,
  };
  return render(<ProviderConfigModal {...defaultProps} {...props} />);
}

describe("ProviderConfigModal – saving state", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows 'Saving...' text on save button when isSaving is true", async () => {
    renderModal({ isSaving: true });
    const user = userEvent.setup();

    // Type a key so the button text is meaningful
    await user.type(screen.getByLabelText("API Key"), "sk-ant-test1234567890");

    // The button should show "Saving..." text
    expect(screen.getByRole("button", { name: /saving/i })).toBeInTheDocument();
  });

  it("disables save button when isSaving is true even with valid input", async () => {
    renderModal({ isSaving: true });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-test1234567890");

    const saveBtn = screen.getByRole("button", { name: /saving/i });
    expect(saveBtn).toBeDisabled();
  });

  it("does not call onSave on Enter key when isSaving is true", async () => {
    const onSave = vi.fn();
    renderModal({ onSave, isSaving: true });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-test1234567890");
    await user.keyboard("{Enter}");

    expect(onSave).not.toHaveBeenCalled();
  });

  it("shows 'Save' text when isSaving is false", () => {
    renderModal({ isSaving: false });
    expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
  });

  it("defaults isSaving to false when prop is omitted", () => {
    renderModal();
    expect(screen.getByRole("button", { name: "Save" })).toBeInTheDocument();
  });
});
