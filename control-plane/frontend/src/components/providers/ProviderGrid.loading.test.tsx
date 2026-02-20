import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import ProviderGrid from "./ProviderGrid";
import { PROVIDERS } from "./providerData";
import type { Settings } from "@/types/settings";

vi.mock("@/api/settings", () => ({
  fetchProviderAnalytics: vi.fn().mockResolvedValue({ providers: {}, period_days: 7, since: "2026-02-13T00:00:00Z" }),
}));

const emptySettings: Settings = {
  brave_api_key: "",
  api_keys: {},
  base_urls: {},
  default_models: [],
  default_container_image: "",
  default_vnc_resolution: "",
  default_cpu_request: "",
  default_cpu_limit: "",
  default_memory_request: "",
  default_memory_limit: "",
  default_storage_homebrew: "",
  default_storage_clawd: "",
  default_storage_chrome: "",
};

describe("ProviderGrid – loading state", () => {
  it("shows skeleton cards when isLoading is true", () => {
    render(
      <MemoryRouter>
        <ProviderGrid
          settings={emptySettings}
          onSaveChanges={() => Promise.resolve()}
          isSaving={false}
          isLoading={true}
        />
      </MemoryRouter>,
    );

    expect(screen.getByTestId("provider-grid-loading")).toBeInTheDocument();
    const skeletons = screen.getAllByTestId("provider-card-skeleton");
    expect(skeletons.length).toBe(PROVIDERS.length);
  });

  it("does not show provider names when loading", () => {
    render(
      <MemoryRouter>
        <ProviderGrid
          settings={emptySettings}
          onSaveChanges={() => Promise.resolve()}
          isSaving={false}
          isLoading={true}
        />
      </MemoryRouter>,
    );

    expect(screen.queryByText("Anthropic")).not.toBeInTheDocument();
    expect(screen.queryByText("OpenAI")).not.toBeInTheDocument();
  });

  it("does not show provider count summary when loading", () => {
    render(
      <MemoryRouter>
        <ProviderGrid
          settings={emptySettings}
          onSaveChanges={() => Promise.resolve()}
          isSaving={false}
          isLoading={true}
        />
      </MemoryRouter>,
    );

    expect(screen.queryByText(/providers configured/i)).not.toBeInTheDocument();
  });

  it("does not show Save Changes button when loading", () => {
    render(
      <MemoryRouter>
        <ProviderGrid
          settings={emptySettings}
          onSaveChanges={() => Promise.resolve()}
          isSaving={false}
          isLoading={true}
        />
      </MemoryRouter>,
    );

    expect(screen.queryByText("Save Changes")).not.toBeInTheDocument();
  });

  it("shows real content when isLoading is false", () => {
    render(
      <MemoryRouter>
        <ProviderGrid
          settings={emptySettings}
          onSaveChanges={() => Promise.resolve()}
          isSaving={false}
          isLoading={false}
        />
      </MemoryRouter>,
    );

    expect(screen.queryByTestId("provider-grid-loading")).not.toBeInTheDocument();
    expect(screen.getByTestId("provider-count-summary")).toBeInTheDocument();
    expect(screen.getByText("Anthropic")).toBeInTheDocument();
  });

  it("defaults isLoading to false when prop is omitted", () => {
    render(
      <MemoryRouter>
        <ProviderGrid
          settings={emptySettings}
          onSaveChanges={() => Promise.resolve()}
          isSaving={false}
        />
      </MemoryRouter>,
    );

    expect(screen.queryByTestId("provider-grid-loading")).not.toBeInTheDocument();
    expect(screen.getByText("Anthropic")).toBeInTheDocument();
  });
});
