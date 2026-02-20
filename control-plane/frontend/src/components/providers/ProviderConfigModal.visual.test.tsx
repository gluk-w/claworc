import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import ProviderConfigModal from "./ProviderConfigModal";
import type { Provider } from "./providerData";

// Suppress react-hot-toast in test environment
vi.mock("react-hot-toast", () => ({
  default: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("@/api/settings", () => ({
  testProviderKey: vi.fn().mockResolvedValue({
    success: true,
    message: "API key is valid.",
  }),
}));

const testProvider: Provider = {
  id: "openai",
  name: "OpenAI",
  envVarName: "OPENAI_API_KEY",
  category: "Major Providers",
  description: "GPT-4 and beyond for versatile language tasks.",
  docsUrl: "https://platform.openai.com/api-keys",
  supportsBaseUrl: true,
  apiKeyPlaceholder: "sk-...",
  baseUrlPlaceholder: "https://api.openai.com/v1",
  brandColor: "#10A37F",
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
    isSaving: false,
  };
  return render(<ProviderConfigModal {...defaultProps} {...props} />);
}

describe("ProviderConfigModal – visual design consistency", () => {
  // ── Border radius ──

  it("uses rounded-lg for the modal dialog", () => {
    renderModal();
    const dialog = screen.getByRole("dialog");
    expect(dialog.className).toContain("rounded-lg");
  });

  it("uses rounded-md for the API key input", () => {
    renderModal();
    const input = screen.getByLabelText("API Key");
    expect(input.className).toContain("rounded-md");
  });

  it("uses rounded-md for the close button", () => {
    renderModal();
    const closeBtn = screen.getByLabelText("Close dialog");
    expect(closeBtn.className).toContain("rounded-md");
  });

  it("uses rounded-md for all footer buttons", () => {
    renderModal();
    const testBtn = screen.getByRole("button", { name: /test connection/i });
    const cancelBtn = screen.getByRole("button", { name: /cancel/i });
    const saveBtn = screen.getByRole("button", { name: /save/i });

    expect(testBtn.className).toContain("rounded-md");
    expect(cancelBtn.className).toContain("rounded-md");
    expect(saveBtn.className).toContain("rounded-md");
  });

  // ── Transitions ──

  it("has transition-colors on the close button", () => {
    renderModal();
    const closeBtn = screen.getByLabelText("Close dialog");
    expect(closeBtn.className).toContain("transition-colors");
  });

  it("has transition-colors on the Save button", () => {
    renderModal();
    const saveBtn = screen.getByRole("button", { name: /save/i });
    expect(saveBtn.className).toContain("transition-colors");
  });

  it("has transition-colors on the Cancel button", () => {
    renderModal();
    const cancelBtn = screen.getByRole("button", { name: /cancel/i });
    expect(cancelBtn.className).toContain("transition-colors");
  });

  it("has transition-colors on the Test Connection button", () => {
    renderModal();
    const testBtn = screen.getByRole("button", { name: /test connection/i });
    expect(testBtn.className).toContain("transition-colors");
  });

  // ── Focus rings ──

  it("has focus:ring-2 on the API key input", () => {
    renderModal();
    const input = screen.getByLabelText("API Key");
    expect(input.className).toContain("focus:ring-2");
    expect(input.className).toContain("focus:ring-blue-500");
  });

  it("has focus:ring-2 on the Save button", () => {
    renderModal();
    const saveBtn = screen.getByRole("button", { name: /save/i });
    expect(saveBtn.className).toContain("focus:ring-2");
    expect(saveBtn.className).toContain("focus:ring-blue-500");
  });

  // ── Color tokens ──

  it("uses bg-blue-600 for the primary Save button", () => {
    renderModal();
    const saveBtn = screen.getByRole("button", { name: /save/i });
    expect(saveBtn.className).toContain("bg-blue-600");
    expect(saveBtn.className).toContain("hover:bg-blue-700");
  });

  it("uses border-gray-300 for secondary buttons", () => {
    renderModal();
    const cancelBtn = screen.getByRole("button", { name: /cancel/i });
    expect(cancelBtn.className).toContain("border-gray-300");
  });

  // ── Spacing ──

  it("uses space-y-4 for the modal body content", () => {
    renderModal();
    const dialog = screen.getByRole("dialog");
    const body = dialog.querySelector(".space-y-4");
    expect(body).not.toBeNull();
  });
});
