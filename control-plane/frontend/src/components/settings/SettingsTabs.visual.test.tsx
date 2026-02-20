import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import ResourceLimitsTab from "./ResourceLimitsTab";
import AgentImageTab from "./AgentImageTab";
import type { Settings } from "@/types/settings";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

const testSettings: Settings = {
  brave_api_key: "",
  api_keys: {},
  base_urls: {},
  default_models: [],
  default_container_image: "ghcr.io/example/agent:latest",
  default_vnc_resolution: "1920x1080",
  default_cpu_request: "500m",
  default_cpu_limit: "2",
  default_memory_request: "512Mi",
  default_memory_limit: "4Gi",
  default_storage_homebrew: "10Gi",
  default_storage_clawd: "10Gi",
  default_storage_chrome: "5Gi",
};

function createWrapper() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return ({ children }: { children: React.ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
}

describe("ResourceLimitsTab – visual design consistency", () => {
  it("uses rounded-lg for the card wrapper", () => {
    render(
      <ResourceLimitsTab settings={testSettings} onFieldChange={vi.fn()} />,
      { wrapper: createWrapper() },
    );
    const card = document.querySelector(".rounded-lg");
    expect(card).not.toBeNull();
  });

  it("uses gap-4 for the fields grid", () => {
    render(
      <ResourceLimitsTab settings={testSettings} onFieldChange={vi.fn()} />,
      { wrapper: createWrapper() },
    );
    const grid = document.querySelector(".grid");
    expect(grid?.className).toContain("gap-4");
  });

  it("save button has focus:ring-2 and transition-colors", () => {
    render(
      <ResourceLimitsTab settings={testSettings} onFieldChange={vi.fn()} />,
      { wrapper: createWrapper() },
    );
    const btn = screen.getByRole("button", { name: /save settings/i });
    expect(btn.className).toContain("focus:ring-2");
    expect(btn.className).toContain("focus:ring-blue-500");
    expect(btn.className).toContain("transition-colors");
    expect(btn.className).toContain("rounded-md");
  });

  it("inputs use rounded-md and focus:ring-2", () => {
    render(
      <ResourceLimitsTab settings={testSettings} onFieldChange={vi.fn()} />,
      { wrapper: createWrapper() },
    );
    const inputs = screen.getAllByRole("textbox");
    inputs.forEach((input) => {
      expect(input.className).toContain("rounded-md");
      expect(input.className).toContain("focus:ring-2");
    });
  });
});

describe("AgentImageTab – visual design consistency", () => {
  it("uses rounded-lg for the card wrapper", () => {
    render(
      <AgentImageTab settings={testSettings} onFieldChange={vi.fn()} />,
      { wrapper: createWrapper() },
    );
    const card = document.querySelector(".rounded-lg");
    expect(card).not.toBeNull();
  });

  it("uses space-y-4 for vertical stacking", () => {
    render(
      <AgentImageTab settings={testSettings} onFieldChange={vi.fn()} />,
      { wrapper: createWrapper() },
    );
    const stack = document.querySelector(".space-y-4");
    expect(stack).not.toBeNull();
  });

  it("save button has focus:ring-2 and transition-colors", () => {
    render(
      <AgentImageTab settings={testSettings} onFieldChange={vi.fn()} />,
      { wrapper: createWrapper() },
    );
    const btn = screen.getByRole("button", { name: /save settings/i });
    expect(btn.className).toContain("focus:ring-2");
    expect(btn.className).toContain("focus:ring-blue-500");
    expect(btn.className).toContain("transition-colors");
    expect(btn.className).toContain("rounded-md");
  });

  it("inputs use rounded-md and focus:ring-2", () => {
    render(
      <AgentImageTab settings={testSettings} onFieldChange={vi.fn()} />,
      { wrapper: createWrapper() },
    );
    const inputs = screen.getAllByRole("textbox");
    inputs.forEach((input) => {
      expect(input.className).toContain("rounded-md");
      expect(input.className).toContain("focus:ring-2");
    });
  });
});
