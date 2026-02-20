import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import ProviderConfigModal from "./ProviderConfigModal";
import { PROVIDERS } from "./providerData";
import type { Provider } from "./providerData";

// ── Mocks ──────────────────────────────────────────────────────────────

vi.mock("react-hot-toast", () => ({
  default: {
    success: vi.fn(),
    error: vi.fn(),
  },
}));

// ── Helpers ────────────────────────────────────────────────────────────

function renderModal(provider: Provider) {
  return render(
    <ProviderConfigModal
      provider={provider}
      isOpen={true}
      onClose={vi.fn()}
      onSave={vi.fn()}
      currentMaskedKey={null}
    />,
  );
}

function getProvider(id: string): Provider {
  const p = PROVIDERS.find((p) => p.id === id);
  if (!p) throw new Error(`Provider ${id} not found`);
  return p;
}

// ── Tests ──────────────────────────────────────────────────────────────

describe("ProviderConfigModal – placeholder hints", () => {
  it("shows 'sk-ant-api03-...' placeholder for Anthropic", () => {
    renderModal(getProvider("anthropic"));
    expect(screen.getByPlaceholderText("sk-ant-api03-...")).toBeInTheDocument();
  });

  it("shows 'sk-...' placeholder for OpenAI", () => {
    renderModal(getProvider("openai"));
    expect(screen.getByPlaceholderText("sk-...")).toBeInTheDocument();
  });

  it("shows 'AIza...' placeholder for Google", () => {
    renderModal(getProvider("google"));
    expect(screen.getByPlaceholderText("AIza...")).toBeInTheDocument();
  });

  it("shows 'gsk_...' placeholder for Groq", () => {
    renderModal(getProvider("groq"));
    expect(screen.getByPlaceholderText("gsk_...")).toBeInTheDocument();
  });

  it("shows 'sk-...' placeholder for DeepSeek", () => {
    renderModal(getProvider("deepseek"));
    expect(screen.getByPlaceholderText("sk-...")).toBeInTheDocument();
  });

  it("shows 'fw_...' placeholder for Fireworks AI", () => {
    renderModal(getProvider("fireworks"));
    expect(screen.getByPlaceholderText("fw_...")).toBeInTheDocument();
  });

  it("shows 'csk-...' placeholder for Cerebras", () => {
    renderModal(getProvider("cerebras"));
    expect(screen.getByPlaceholderText("csk-...")).toBeInTheDocument();
  });

  it("shows 'xai-...' placeholder for xAI", () => {
    renderModal(getProvider("xai"));
    expect(screen.getByPlaceholderText("xai-...")).toBeInTheDocument();
  });

  it("shows 'pplx-...' placeholder for Perplexity", () => {
    renderModal(getProvider("perplexity"));
    expect(screen.getByPlaceholderText("pplx-...")).toBeInTheDocument();
  });

  it("shows 'sk-or-...' placeholder for OpenRouter", () => {
    renderModal(getProvider("openrouter"));
    expect(screen.getByPlaceholderText("sk-or-...")).toBeInTheDocument();
  });

  it("shows default 'Enter API key' placeholder for providers without custom placeholder", () => {
    renderModal(getProvider("brave"));
    expect(screen.getByPlaceholderText("Enter API key")).toBeInTheDocument();
  });

  it("shows default 'Enter API key' placeholder for Mistral (no custom placeholder)", () => {
    renderModal(getProvider("mistral"));
    expect(screen.getByPlaceholderText("Enter API key")).toBeInTheDocument();
  });
});

describe("ProviderConfigModal – base URL placeholder", () => {
  it("shows 'https://api.openai.com/v1' as base URL placeholder for OpenAI", () => {
    renderModal(getProvider("openai"));
    expect(
      screen.getByPlaceholderText("https://api.openai.com/v1"),
    ).toBeInTheDocument();
  });
});
