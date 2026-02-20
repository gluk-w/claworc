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

describe("ProviderCard – visual design consistency", () => {
  // ── Border radius ──

  it("uses rounded-lg for the card container", () => {
    renderCard();
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("rounded-lg");
  });

  it("uses rounded-md for the Configure button", () => {
    renderCard();
    const btn = screen.getByRole("button", { name: "Configure" });
    expect(btn.className).toContain("rounded-md");
  });

  it("uses rounded-md for the delete button", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    const btn = screen.getByLabelText("Remove Anthropic API key");
    expect(btn.className).toContain("rounded-md");
  });

  it("uses rounded-md for the docs link", () => {
    renderCard();
    const link = screen.getByLabelText("Anthropic API key documentation");
    expect(link.className).toContain("rounded-md");
  });

  // ── Hover / transition effects ──

  it("has hover:shadow-md and hover:scale-105 on the card", () => {
    renderCard();
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("hover:shadow-md");
    expect(card.className).toContain("hover:scale-105");
  });

  it("has transition-all duration-200 on the card", () => {
    renderCard();
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("transition-all");
    expect(card.className).toContain("duration-200");
  });

  it("has transition-colors on the Configure button", () => {
    renderCard();
    const btn = screen.getByRole("button", { name: "Configure" });
    expect(btn.className).toContain("transition-colors");
  });

  it("has transition-colors on the delete button", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    const btn = screen.getByLabelText("Remove Anthropic API key");
    expect(btn.className).toContain("transition-colors");
  });

  // ── Configured badge (top-right corner) ──

  it("renders a green checkmark badge at top-right when configured", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    const badge = screen.getByTestId("configured-badge");
    expect(badge).toBeInTheDocument();
    expect(badge.className).toContain("absolute");
    expect(badge.className).toContain("-top-2");
    expect(badge.className).toContain("-right-2");
    expect(badge.className).toContain("bg-green-500");
    expect(badge.className).toContain("rounded-full");
  });

  it("does not render configured badge when not configured", () => {
    renderCard({ isConfigured: false });
    expect(screen.queryByTestId("configured-badge")).not.toBeInTheDocument();
  });

  it("card has relative positioning for badge overlay", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    const card = screen.getByLabelText("Anthropic provider (configured)");
    expect(card.className).toContain("relative");
  });

  // ── Color tokens ──

  it("uses Tailwind gray tokens for unconfigured border", () => {
    renderCard({ isConfigured: false });
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("border-gray-200");
  });

  it("uses green-500 border for configured cards", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    const card = screen.getByLabelText("Anthropic provider (configured)");
    expect(card.className).toContain("border-green-500");
  });

  // ── Spacing ──

  it("uses gap-3 inside the card for consistent element spacing", () => {
    renderCard();
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("gap-3");
  });

  it("uses p-4 padding on the card", () => {
    renderCard();
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("p-4");
  });
});
