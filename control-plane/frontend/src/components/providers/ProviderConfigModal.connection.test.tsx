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
  testProviderKey: vi.fn(),
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

// ── Tests: Real connection testing ─────────────────────────────────────

describe("ProviderConfigModal – connection testing", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("calls backend testProviderKey on valid format key", async () => {
    mockTestProviderKey.mockResolvedValue({
      success: true,
      message: "API key is valid. Connection successful!",
    });

    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-valid-key-data");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(mockTestProviderKey).toHaveBeenCalledWith({
        provider: "anthropic",
        api_key: "sk-ant-valid-key-data",
        base_url: undefined,
      });
    });
  });

  it("shows success message from backend on valid key", async () => {
    mockTestProviderKey.mockResolvedValue({
      success: true,
      message: "API key is valid. Connection successful!",
    });

    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-valid-key-data");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByRole("status")).toBeInTheDocument();
      expect(screen.getByText("API key verified successfully.")).toBeInTheDocument();
    });
  });

  it("shows failure message from backend on invalid key", async () => {
    mockTestProviderKey.mockResolvedValue({
      success: false,
      message: "Invalid API key",
      details: "The provider rejected the API key. Please verify you copied the full key.",
    });

    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-invalid-key-xx");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
      expect(screen.getByText("Connection test failed.")).toBeInTheDocument();
      expect(screen.getByText(/The provider rejected the API key/)).toBeInTheDocument();
    });
  });

  it("shows error details when backend returns them", async () => {
    mockTestProviderKey.mockResolvedValue({
      success: false,
      message: "Rate limited",
      details: "The API key is valid but rate-limited. Try again in a moment.",
    });

    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-rate-limited-k");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByText(/rate-limited/)).toBeInTheDocument();
    });
  });

  it("does not call backend when client-side format validation fails", async () => {
    renderModal();
    const user = userEvent.setup();

    // "bad-key" doesn't match Anthropic's "sk-ant-" prefix
    await user.type(screen.getByLabelText("API Key"), "bad-key");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    // Should not call the backend API
    expect(mockTestProviderKey).not.toHaveBeenCalled();

    // Should still show invalid state
    await vi.waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });
  });

  it("handles network error gracefully", async () => {
    mockTestProviderKey.mockRejectedValue(new Error("Network Error"));

    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-valid-key-data");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByRole("alert")).toBeInTheDocument();
      expect(screen.getByText(/Could not reach the server/)).toBeInTheDocument();
    });
  });

  it("shows spinner during backend call", async () => {
    // Mock a delayed response
    mockTestProviderKey.mockImplementation(
      () => new Promise((resolve) => setTimeout(() => resolve({
        success: true,
        message: "API key is valid.",
      }), 100)),
    );

    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-valid-key-data");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    // Should show the "Testing..." text while waiting
    expect(screen.getByRole("button", { name: /Testing/ })).toBeInTheDocument();

    // Wait for completion
    await vi.waitFor(() => {
      expect(screen.getByRole("button", { name: "Test Connection" })).toBeInTheDocument();
    });
  });

  it("disables test button during testing", async () => {
    mockTestProviderKey.mockImplementation(
      () => new Promise((resolve) => setTimeout(() => resolve({
        success: true,
        message: "API key is valid.",
      }), 100)),
    );

    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-valid-key-data");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    // Button should be disabled during testing
    expect(screen.getByRole("button", { name: /Testing/ })).toBeDisabled();

    await vi.waitFor(() => {
      expect(screen.getByRole("button", { name: "Test Connection" })).toBeEnabled();
    });
  });

  it("sends base_url when provider supports it", async () => {
    mockTestProviderKey.mockResolvedValue({
      success: true,
      message: "API key is valid.",
    });

    renderModal({ provider: testProviderWithBaseUrl });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-openai-test-key12");
    await user.type(screen.getByLabelText(/Base URL/), "https://my-proxy.com/v1");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(mockTestProviderKey).toHaveBeenCalledWith({
        provider: "openai",
        api_key: "sk-openai-test-key12",
        base_url: "https://my-proxy.com/v1",
      });
    });
  });

  it("does not send base_url when empty", async () => {
    mockTestProviderKey.mockResolvedValue({
      success: true,
      message: "API key is valid.",
    });

    renderModal({ provider: testProviderWithBaseUrl });
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-openai-test-key12");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(mockTestProviderKey).toHaveBeenCalledWith({
        provider: "openai",
        api_key: "sk-openai-test-key12",
        base_url: undefined,
      });
    });
  });

  it("clears error details when key input changes", async () => {
    mockTestProviderKey.mockResolvedValue({
      success: false,
      message: "Invalid API key",
      details: "Some error details here",
    });

    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-invalid-key-xx");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      expect(screen.getByText(/Some error details here/)).toBeInTheDocument();
    });

    // Type a new character to reset
    await user.type(screen.getByLabelText("API Key"), "x");

    expect(screen.queryByText(/Some error details here/)).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("disables save button after backend reports invalid key", async () => {
    mockTestProviderKey.mockResolvedValue({
      success: false,
      message: "Invalid API key",
    });

    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-invalid-key-xx");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      const saveBtn = screen.getByRole("button", { name: "Save" });
      expect(saveBtn).toBeDisabled();
    });
  });

  it("enables save button after backend reports valid key", async () => {
    mockTestProviderKey.mockResolvedValue({
      success: true,
      message: "API key is valid.",
    });

    renderModal();
    const user = userEvent.setup();

    await user.type(screen.getByLabelText("API Key"), "sk-ant-valid-key-data");
    await user.click(screen.getByRole("button", { name: "Test Connection" }));

    await vi.waitFor(() => {
      const saveBtn = screen.getByRole("button", { name: "Save" });
      expect(saveBtn).toBeEnabled();
    });
  });
});
