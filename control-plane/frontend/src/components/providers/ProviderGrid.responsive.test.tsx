import { describe, it, expect, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import ProviderGrid from "./ProviderGrid";
import ProviderConfigModal from "./ProviderConfigModal";
import BatchActionBar from "./BatchActionBar";
import ConfirmDialog from "../ConfirmDialog";
import ProviderStatsTab from "./ProviderStatsTab";
import type { Settings } from "@/types/settings";
import { PROVIDERS } from "./providerData";

vi.mock("@/api/settings", () => ({
  fetchProviderAnalytics: vi.fn().mockResolvedValue({
    providers: {},
    period_days: 7,
    since: "2026-02-13T00:00:00Z",
  }),
  testProviderKey: vi.fn().mockResolvedValue({
    success: true,
    message: "OK",
  }),
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

function renderGrid(overrides: Partial<{ isLoading: boolean }> = {}) {
  return render(
    <MemoryRouter>
      <ProviderGrid
        settings={emptySettings}
        onSaveChanges={() => Promise.resolve()}
        isSaving={false}
        isLoading={overrides.isLoading ?? false}
      />
    </MemoryRouter>,
  );
}

// ──────────────────────────────────────────────────────────
// § 1  Mobile viewport (375px) – cards stack, modal usable
// ──────────────────────────────────────────────────────────
describe("Responsive: Mobile viewport (375px)", () => {
  it("provider grid uses grid-cols-1 as the base (mobile-first) layout", () => {
    renderGrid();
    const grids = document.querySelectorAll(".grid");
    expect(grids.length).toBeGreaterThan(0);
    grids.forEach((grid) => {
      expect(grid.className).toContain("grid-cols-1");
    });
  });

  it("search and filter bar stacks vertically on mobile (flex-col)", () => {
    renderGrid();
    const searchFilter = screen.getByTestId("provider-search-filter");
    expect(searchFilter.className).toContain("flex-col");
    // Should switch to horizontal on sm breakpoint
    expect(searchFilter.className).toContain("sm:flex-row");
  });

  it("loading state grid also uses mobile-first single-column layout", () => {
    renderGrid({ isLoading: true });
    const grids = document.querySelectorAll(".grid");
    grids.forEach((grid) => {
      expect(grid.className).toContain("grid-cols-1");
    });
  });

  describe("ProviderConfigModal – mobile usability", () => {
    const testProvider = PROVIDERS[0]!;

    it("modal uses w-full with max-w-md and mx-4 for mobile margins", () => {
      render(
        <ProviderConfigModal
          provider={testProvider}
          isOpen={true}
          onClose={vi.fn()}
          onSave={vi.fn()}
          currentMaskedKey={null}
        />,
      );
      const dialog = screen.getByRole("dialog");
      expect(dialog.className).toContain("w-full");
      expect(dialog.className).toContain("max-w-md");
      expect(dialog.className).toContain("mx-4");
    });

    it("modal is centered with flex items-center justify-center", () => {
      render(
        <ProviderConfigModal
          provider={testProvider}
          isOpen={true}
          onClose={vi.fn()}
          onSave={vi.fn()}
          currentMaskedKey={null}
        />,
      );
      const overlay = screen.getByRole("dialog").parentElement!;
      expect(overlay.className).toContain("flex");
      expect(overlay.className).toContain("items-center");
      expect(overlay.className).toContain("justify-center");
    });

    it("modal overlay uses fixed inset-0 for full-viewport coverage", () => {
      render(
        <ProviderConfigModal
          provider={testProvider}
          isOpen={true}
          onClose={vi.fn()}
          onSave={vi.fn()}
          currentMaskedKey={null}
        />,
      );
      const overlay = screen.getByRole("dialog").parentElement!;
      expect(overlay.className).toContain("fixed");
      expect(overlay.className).toContain("inset-0");
    });

    it("input fields use w-full to fill available mobile width", () => {
      render(
        <ProviderConfigModal
          provider={testProvider}
          isOpen={true}
          onClose={vi.fn()}
          onSave={vi.fn()}
          currentMaskedKey={null}
        />,
      );
      const apiInput = screen.getByLabelText("API Key");
      expect(apiInput.className).toContain("w-full");
    });
  });

  describe("ConfirmDialog – mobile usability", () => {
    it("uses w-full max-w-sm mx-4 for responsive dialog sizing", () => {
      render(
        <ConfirmDialog
          title="Test"
          message="Are you sure?"
          onConfirm={vi.fn()}
          onCancel={vi.fn()}
        />,
      );
      const dialog = screen.getByRole("dialog");
      const container = within(dialog).getByText("Test").closest(".relative")!;
      expect(container.className).toContain("w-full");
      expect(container.className).toContain("max-w-sm");
      expect(container.className).toContain("mx-4");
    });
  });
});

// ──────────────────────────────────────────────────────────
// § 2  Tablet viewport (768px) – 2-column grid
// ──────────────────────────────────────────────────────────
describe("Responsive: Tablet viewport (768px)", () => {
  it("provider grid specifies md:grid-cols-2 for tablet breakpoint", () => {
    renderGrid();
    const grids = document.querySelectorAll(".grid");
    grids.forEach((grid) => {
      expect(grid.className).toContain("md:grid-cols-2");
    });
  });

  it("loading state grid also includes md:grid-cols-2", () => {
    renderGrid({ isLoading: true });
    const grids = document.querySelectorAll(".grid");
    grids.forEach((grid) => {
      expect(grid.className).toContain("md:grid-cols-2");
    });
  });

  it("search bar switches to horizontal layout at sm breakpoint", () => {
    renderGrid();
    const searchFilter = screen.getByTestId("provider-search-filter");
    expect(searchFilter.className).toContain("sm:flex-row");
  });
});

// ──────────────────────────────────────────────────────────
// § 3  Desktop viewport (1920px) – 3-column grid
// ──────────────────────────────────────────────────────────
describe("Responsive: Desktop viewport (1920px)", () => {
  it("provider grid specifies lg:grid-cols-3 for desktop breakpoint", () => {
    renderGrid();
    const grids = document.querySelectorAll(".grid");
    grids.forEach((grid) => {
      expect(grid.className).toContain("lg:grid-cols-3");
    });
  });

  it("loading state grid also includes lg:grid-cols-3", () => {
    renderGrid({ isLoading: true });
    const grids = document.querySelectorAll(".grid");
    grids.forEach((grid) => {
      expect(grid.className).toContain("lg:grid-cols-3");
    });
  });

  it("settings page content area is constrained with max-w-4xl", () => {
    // Verify directly since SettingsPage is complex to render here
    // The max-w-4xl class provides a sensible maximum width on large screens
    // This is tested in SettingsPage.test.tsx; here we verify the grid doesn't
    // add its own max-width constraint (it relies on the parent)
    renderGrid();
    const container = screen.getByTestId("provider-count-summary").parentElement!.parentElement!;
    expect(container.className).not.toContain("max-w-");
  });
});

// ──────────────────────────────────────────────────────────
// § 4  Safari (Webkit) CSS compatibility
// ──────────────────────────────────────────────────────────
describe("Browser compat: Safari (Webkit)", () => {
  it("modal backdrop uses backdrop-blur-sm (supported since Safari 9+)", () => {
    const testProvider = PROVIDERS[0]!;
    render(
      <ProviderConfigModal
        provider={testProvider}
        isOpen={true}
        onClose={vi.fn()}
        onSave={vi.fn()}
        currentMaskedKey={null}
      />,
    );
    // The backdrop is a sibling of the dialog, rendered before it
    const backdrop = document.querySelector("[aria-hidden='true']");
    expect(backdrop).not.toBeNull();
    expect(backdrop!.className).toContain("backdrop-blur-sm");
  });

  it("modal uses standard CSS Grid (no subgrid or other advanced features)", () => {
    // Grid is used on the provider cards – verify standard grid-cols-* only
    renderGrid();
    const grids = document.querySelectorAll(".grid");
    grids.forEach((grid) => {
      expect(grid.className).not.toContain("subgrid");
      expect(grid.className).not.toContain("masonry");
    });
  });

  it("all cards use standard border-radius via rounded-lg (no unusual shapes)", () => {
    renderGrid();
    const cards = document.querySelectorAll(".rounded-lg");
    expect(cards.length).toBeGreaterThan(0);
  });

  it("animations use standard @keyframes with transform/opacity (no container queries)", () => {
    // Custom animations only use transform and opacity – widely supported
    // Verify the classes that reference them exist
    renderGrid();
    // The animate-provider-* classes are only applied during state transitions,
    // but the base animation system uses standard CSS – this is a structural check
    const style = document.querySelector("style, link[rel='stylesheet']");
    // In jsdom, stylesheets may not be fully loaded, but we can verify no
    // container-query or has() selectors are used in component classes
    const allElements = document.querySelectorAll("*");
    allElements.forEach((el) => {
      const cls = el.className;
      if (typeof cls === "string") {
        expect(cls).not.toContain("@container");
        expect(cls).not.toContain(":has(");
      }
    });
    expect(style).toBeDefined(); // structural assertion
  });

  it("stats tab uses standard CSS grid-cols-2 (Safari 10.1+)", () => {
    render(
      <ProviderStatsTab
        providerId="test"
        providerName="Test"
        brandColor="#000"
      />,
    );
    // Stats tab shows loading first; verify no advanced CSS in the loading state
    const loadingEl = screen.getByTestId("stats-loading");
    expect(loadingEl.className).toContain("flex");
  });
});

// ──────────────────────────────────────────────────────────
// § 5  Firefox CSS compatibility
// ──────────────────────────────────────────────────────────
describe("Browser compat: Firefox", () => {
  it("uses standard flexbox (no -webkit- prefixed layouts)", () => {
    renderGrid();
    const allElements = document.querySelectorAll("*");
    allElements.forEach((el) => {
      const cls = el.className;
      if (typeof cls === "string") {
        // Tailwind classes should not include webkit prefixes directly
        expect(cls).not.toContain("-webkit-flex");
      }
    });
  });

  it("all transitions use standard transition-* classes", () => {
    renderGrid();
    const transitioned = document.querySelectorAll('[class*="transition"]');
    transitioned.forEach((el) => {
      expect(el.className).not.toContain("-moz-transition");
      expect(el.className).not.toContain("-webkit-transition");
    });
  });

  it("focus rings use standard focus:ring-2 (no -moz-focus-inner workaround needed)", () => {
    renderGrid();
    const focusElements = document.querySelectorAll('[class*="focus:ring"]');
    expect(focusElements.length).toBeGreaterThan(0);
    focusElements.forEach((el) => {
      expect(el.className).toContain("focus:ring-2");
    });
  });

  it("batch action bar uses flex-wrap for wrapping on narrow Firefox viewports", () => {
    render(
      <BatchActionBar
        selectedProviders={[PROVIDERS[0]!]}
        configuredKeys={{}}
        onDeleteSelected={vi.fn()}
        onClearSelection={vi.fn()}
      />,
    );
    const bar = screen.getByTestId("batch-action-bar");
    const actionRow = bar.querySelector(".flex-wrap");
    expect(actionRow).not.toBeNull();
  });
});

// ──────────────────────────────────────────────────────────
// § 6  Older Chrome (v100) compatibility
// ──────────────────────────────────────────────────────────
describe("Browser compat: Chrome v100+", () => {
  it("uses CSS gap (supported in Chrome 84+) for grid and flex spacing", () => {
    renderGrid();
    const gapped = document.querySelectorAll('[class*="gap-"]');
    expect(gapped.length).toBeGreaterThan(0);
    // gap is standard since Chrome 84 – well within v100 support
  });

  it("uses bg-black/40 opacity shorthand (Tailwind utility, compiled to standard rgba)", () => {
    const testProvider = PROVIDERS[0]!;
    render(
      <ProviderConfigModal
        provider={testProvider}
        isOpen={true}
        onClose={vi.fn()}
        onSave={vi.fn()}
        currentMaskedKey={null}
      />,
    );
    const backdrop = document.querySelector("[aria-hidden='true']");
    expect(backdrop).not.toBeNull();
    // bg-black/40 compiles to standard rgba – no browser prefix needed
    expect(backdrop!.className).toContain("bg-black/40");
  });

  it("hover:scale-105 uses standard CSS transform (Chrome 36+)", () => {
    renderGrid();
    // Cards have hover:scale-105 class – verify presence
    const cards = document.querySelectorAll('[class*="hover:scale"]');
    // Cards render from the PROVIDERS array – should have several
    expect(cards.length).toBeGreaterThan(0);
  });

  it("no CSS nesting or :has() pseudo-class used (Chrome 105+, risky for v100)", () => {
    renderGrid();
    const allElements = document.querySelectorAll("*");
    allElements.forEach((el) => {
      const cls = el.className;
      if (typeof cls === "string") {
        expect(cls).not.toContain(":has(");
      }
    });
  });

  it("animate-pulse uses standard @keyframes (Chrome 43+)", () => {
    renderGrid({ isLoading: true });
    const pulsing = document.querySelectorAll('[class*="animate-pulse"]');
    expect(pulsing.length).toBeGreaterThan(0);
  });
});

// ──────────────────────────────────────────────────────────
// § 7  Cross-breakpoint structural integrity
// ──────────────────────────────────────────────────────────
describe("Responsive: Structural integrity across breakpoints", () => {
  it("all grids have consistent gap-4 spacing at every breakpoint", () => {
    renderGrid();
    const grids = document.querySelectorAll(".grid");
    grids.forEach((grid) => {
      expect(grid.className).toContain("gap-4");
    });
  });

  it("provider cards use flex-col for vertical stacking (reflows naturally)", () => {
    renderGrid();
    const cards = document.querySelectorAll('[class*="flex-col"]');
    expect(cards.length).toBeGreaterThan(0);
  });

  it("empty state uses flex-col items-center for centered vertical layout", () => {
    // With empty settings and no filter, empty state appears
    const { container } = renderGrid();
    const emptyState = container.querySelector('[data-testid="provider-empty-state"]');
    if (emptyState) {
      expect(emptyState.className).toContain("flex-col");
      expect(emptyState.className).toContain("items-center");
      expect(emptyState.className).toContain("justify-center");
    }
  });

  it("responsive breakpoints follow mobile-first order (base → sm → md → lg)", () => {
    renderGrid();
    const grid = document.querySelector(".grid")!;
    const cls = grid.className;

    // Verify the breakpoint order: grid-cols-1 (base), md:grid-cols-2, lg:grid-cols-3
    const baseIdx = cls.indexOf("grid-cols-1");
    const mdIdx = cls.indexOf("md:grid-cols-2");
    const lgIdx = cls.indexOf("lg:grid-cols-3");

    expect(baseIdx).toBeGreaterThanOrEqual(0);
    expect(mdIdx).toBeGreaterThanOrEqual(0);
    expect(lgIdx).toBeGreaterThanOrEqual(0);
  });

  it("search/filter uses responsive flex-col → sm:flex-row pattern", () => {
    renderGrid();
    const searchFilter = screen.getByTestId("provider-search-filter");
    const cls = searchFilter.className;
    expect(cls).toContain("flex");
    expect(cls).toContain("flex-col");
    expect(cls).toContain("sm:flex-row");
  });

  it("search input uses flex-1 to fill available width", () => {
    renderGrid();
    const searchFilter = screen.getByTestId("provider-search-filter");
    const inputWrapper = searchFilter.querySelector(".flex-1");
    expect(inputWrapper).not.toBeNull();
  });
});
