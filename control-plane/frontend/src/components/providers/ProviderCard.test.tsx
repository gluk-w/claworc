import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

function renderCard(props: Partial<React.ComponentProps<typeof ProviderCard>> = {}) {
  const defaultProps = {
    provider: testProvider,
    isConfigured: false,
    maskedKey: null,
    onConfigure: vi.fn(),
    onDelete: vi.fn(),
  };
  return render(<ProviderCard {...defaultProps} {...props} />);
}

describe("ProviderCard – rendering", () => {
  it("renders provider name", () => {
    renderCard();
    expect(screen.getByText("Anthropic")).toBeInTheDocument();
  });

  it("renders provider description", () => {
    renderCard();
    expect(
      screen.getByText("Claude models for advanced reasoning and analysis."),
    ).toBeInTheDocument();
  });

  it("shows green border when isConfigured=true", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    const card = screen.getByLabelText("Anthropic provider (configured)");
    expect(card.className).toContain("border-green-500");
    expect(card.className).not.toContain("border-gray-200");
  });

  it("shows gray border when isConfigured=false", () => {
    renderCard({ isConfigured: false });
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("border-gray-200");
    expect(card.className).not.toContain("border-green-500");
  });

  it("shows masked key badge when configured", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    expect(screen.getByText("****7890")).toBeInTheDocument();
  });

  it("does not show masked key badge when not configured", () => {
    renderCard({ isConfigured: false, maskedKey: null });
    expect(screen.queryByText(/\*{4}/)).not.toBeInTheDocument();
  });

  it("shows 'Update' button text when configured", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    expect(screen.getByRole("button", { name: "Update" })).toBeInTheDocument();
  });

  it("shows 'Configure' button text when not configured", () => {
    renderCard({ isConfigured: false });
    expect(
      screen.getByRole("button", { name: "Configure" }),
    ).toBeInTheDocument();
  });

  it("shows delete button only when configured", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    expect(
      screen.getByLabelText("Remove Anthropic API key"),
    ).toBeInTheDocument();
  });

  it("does not show delete button when not configured", () => {
    renderCard({ isConfigured: false });
    expect(
      screen.queryByLabelText("Remove Anthropic API key"),
    ).not.toBeInTheDocument();
  });
});

describe("ProviderCard – interactions", () => {
  it("configure button calls onConfigure callback", async () => {
    const onConfigure = vi.fn();
    renderCard({ onConfigure });
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Configure" }));
    expect(onConfigure).toHaveBeenCalledTimes(1);
  });

  it("update button calls onConfigure callback when configured", async () => {
    const onConfigure = vi.fn();
    renderCard({ isConfigured: true, maskedKey: "****7890", onConfigure });
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: "Update" }));
    expect(onConfigure).toHaveBeenCalledTimes(1);
  });

  it("delete button calls onDelete callback", async () => {
    const onDelete = vi.fn();
    renderCard({ isConfigured: true, maskedKey: "****7890", onDelete });
    const user = userEvent.setup();

    await user.click(screen.getByLabelText("Remove Anthropic API key"));
    expect(onDelete).toHaveBeenCalledTimes(1);
  });

  it("external link opens provider docs URL", () => {
    renderCard();
    const link = screen.getByLabelText("Anthropic API key documentation");
    expect(link).toHaveAttribute(
      "href",
      "https://console.anthropic.com/settings/keys",
    );
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });
});

describe("ProviderCard – accessibility", () => {
  it("has tabIndex=0 for keyboard focus", () => {
    renderCard();
    const card = screen.getByLabelText("Anthropic provider");
    expect(card).toHaveAttribute("tabindex", "0");
  });

  it("has descriptive aria-label", () => {
    renderCard();
    const card = screen.getByLabelText("Anthropic provider");
    expect(card).toBeInTheDocument();
  });

  it("includes configured status in aria-label when configured", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    const card = screen.getByLabelText("Anthropic provider (configured)");
    expect(card).toBeInTheDocument();
  });

  it("opens configure modal on Enter key when card is focused", async () => {
    const onConfigure = vi.fn();
    renderCard({ onConfigure });
    const user = userEvent.setup();

    const card = screen.getByLabelText("Anthropic provider");
    card.focus();
    await user.keyboard("{Enter}");

    expect(onConfigure).toHaveBeenCalledTimes(1);
  });

  it("opens configure modal on Space key when card is focused", async () => {
    const onConfigure = vi.fn();
    renderCard({ onConfigure });
    const user = userEvent.setup();

    const card = screen.getByLabelText("Anthropic provider");
    card.focus();
    await user.keyboard(" ");

    expect(onConfigure).toHaveBeenCalledTimes(1);
  });

  it("does not trigger card handler when child button receives keydown", async () => {
    const onConfigure = vi.fn();
    renderCard({ onConfigure });
    const user = userEvent.setup();

    // Focus the Configure button directly, not the card
    const configButton = screen.getByRole("button", { name: "Configure" });
    configButton.focus();
    await user.keyboard("{Enter}");

    // onConfigure called once by the button's click, not by the card handler
    expect(onConfigure).toHaveBeenCalledTimes(1);
  });

  it("has focus ring class on the card", () => {
    renderCard();
    const card = screen.getByLabelText("Anthropic provider");
    expect(card.className).toContain("focus:ring-2");
    expect(card.className).toContain("focus:ring-blue-500");
  });

  it("has aria-label on docs link", () => {
    renderCard();
    const link = screen.getByLabelText("Anthropic API key documentation");
    expect(link).toBeInTheDocument();
  });

  it("has aria-label on delete button when configured", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    const deleteBtn = screen.getByLabelText("Remove Anthropic API key");
    expect(deleteBtn).toBeInTheDocument();
  });

  it("marks green checkmark as aria-hidden", () => {
    renderCard({ isConfigured: true, maskedKey: "****7890" });
    const checkmark = document.querySelector('[aria-hidden="true"]');
    expect(checkmark).toBeInTheDocument();
    expect(checkmark?.className).toContain("bg-green-500");
  });
});
