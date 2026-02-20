import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import ProviderCard from "./ProviderCard";
import type { Provider } from "./providerData";

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

function renderCard(
  props: Partial<React.ComponentProps<typeof ProviderCard>> = {},
) {
  const defaultProps = {
    provider: testProvider,
    isConfigured: false,
    maskedKey: null,
    onConfigure: vi.fn(),
    onDelete: vi.fn(),
  };
  return render(<ProviderCard {...defaultProps} {...props} />);
}

describe("ProviderCard – animation states", () => {
  // ── Default state ──

  it("does not apply animation classes when animationState is idle", () => {
    renderCard({ animationState: "idle" });
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).not.toContain("animate-provider-fade-in");
    expect(card.className).not.toContain("animate-provider-fade-out");
  });

  it("does not apply animation classes when animationState is not provided", () => {
    renderCard();
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).not.toContain("animate-provider-fade-in");
    expect(card.className).not.toContain("animate-provider-fade-out");
  });

  // ── Added animation ──

  it("applies animate-provider-fade-in when animationState is added", () => {
    renderCard({ animationState: "added", isConfigured: true, maskedKey: "****7890" });
    const card = screen.getByLabelText("Anthropic provider (configured)");
    expect(card.className).toContain("animate-provider-fade-in");
    expect(card.className).not.toContain("animate-provider-fade-out");
  });

  // ── Deleted animation ──

  it("applies animate-provider-fade-out when animationState is deleted", () => {
    renderCard({ animationState: "deleted" });
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("animate-provider-fade-out");
    expect(card.className).not.toContain("animate-provider-fade-in");
  });

  // ── Transition utilities ──

  it("has ease-in-out on the card for smooth animations", () => {
    renderCard();
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("ease-in-out");
  });

  it("has transition-all duration-200 ease-in-out for CSS transitions", () => {
    renderCard();
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("transition-all");
    expect(card.className).toContain("duration-200");
    expect(card.className).toContain("ease-in-out");
  });
});
