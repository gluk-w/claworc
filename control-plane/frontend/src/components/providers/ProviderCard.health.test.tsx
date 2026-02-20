import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import ProviderCard from "./ProviderCard";
import type { Provider } from "./providerData";
import { HEALTH_COLORS } from "./providerHealth";

const testProvider: Provider = {
  id: "openai",
  name: "OpenAI",
  envVarName: "OPENAI_API_KEY",
  category: "Major Providers",
  description: "Access GPT and o-series models.",
  docsUrl: "https://platform.openai.com/api-keys",
  supportsBaseUrl: true,
  brandColor: "#10A37F",
};

function renderCard(props: Partial<React.ComponentProps<typeof ProviderCard>> = {}) {
  const defaultProps = {
    provider: testProvider,
    isConfigured: true,
    maskedKey: "****1234",
    onConfigure: vi.fn(),
    onDelete: vi.fn(),
  };
  return render(<ProviderCard {...defaultProps} {...props} />);
}

describe("ProviderCard – health indicator", () => {
  it("shows green health indicator when healthy", () => {
    renderCard({ healthStatus: "healthy" });
    const indicator = screen.getByTestId("health-indicator");
    expect(indicator).toBeInTheDocument();
    expect(indicator).toHaveStyle({ backgroundColor: HEALTH_COLORS.healthy });
    expect(indicator).toHaveAttribute("title", "Health: Healthy");
  });

  it("shows yellow health indicator when warning", () => {
    renderCard({ healthStatus: "warning" });
    const indicator = screen.getByTestId("health-indicator");
    expect(indicator).toBeInTheDocument();
    expect(indicator).toHaveStyle({ backgroundColor: HEALTH_COLORS.warning });
    expect(indicator).toHaveAttribute("title", "Health: Warnings");
  });

  it("shows red health indicator when error", () => {
    renderCard({ healthStatus: "error" });
    const indicator = screen.getByTestId("health-indicator");
    expect(indicator).toBeInTheDocument();
    expect(indicator).toHaveStyle({ backgroundColor: HEALTH_COLORS.error });
    expect(indicator).toHaveAttribute("title", "Health: Errors");
  });

  it("does not show health indicator when status is unknown", () => {
    renderCard({ healthStatus: "unknown" });
    expect(screen.queryByTestId("health-indicator")).not.toBeInTheDocument();
  });

  it("does not show health indicator when healthStatus is undefined", () => {
    renderCard({ healthStatus: undefined });
    expect(screen.queryByTestId("health-indicator")).not.toBeInTheDocument();
  });

  it("has correct aria-label for accessibility", () => {
    renderCard({ healthStatus: "healthy" });
    const indicator = screen.getByTestId("health-indicator");
    expect(indicator).toHaveAttribute("aria-label", "Provider health: Healthy");
  });
});
