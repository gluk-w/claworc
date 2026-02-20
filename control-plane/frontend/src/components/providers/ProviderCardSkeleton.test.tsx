import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import ProviderCardSkeleton from "./ProviderCardSkeleton";

describe("ProviderCardSkeleton", () => {
  it("renders with data-testid for querying", () => {
    render(<ProviderCardSkeleton />);
    expect(screen.getByTestId("provider-card-skeleton")).toBeInTheDocument();
  });

  it("is hidden from screen readers with aria-hidden", () => {
    render(<ProviderCardSkeleton />);
    const skeleton = screen.getByTestId("provider-card-skeleton");
    expect(skeleton).toHaveAttribute("aria-hidden", "true");
  });

  it("has the animate-pulse class for loading animation", () => {
    render(<ProviderCardSkeleton />);
    const skeleton = screen.getByTestId("provider-card-skeleton");
    expect(skeleton.className).toContain("animate-pulse");
  });

  it("matches the same card shape (rounded-lg, border, p-4)", () => {
    render(<ProviderCardSkeleton />);
    const skeleton = screen.getByTestId("provider-card-skeleton");
    expect(skeleton.className).toContain("rounded-lg");
    expect(skeleton.className).toContain("border");
    expect(skeleton.className).toContain("p-4");
  });
});
