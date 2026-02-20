import { describe, it, expect } from "vitest";
import { PROVIDERS } from "./providerData";
import type { ProviderCategory } from "./providerData";
import { PROVIDER_ICONS, CATEGORY_ICONS } from "./providerIcons";

describe("providerIcons", () => {
  describe("PROVIDER_ICONS", () => {
    it("has an icon for every provider in PROVIDERS", () => {
      for (const provider of PROVIDERS) {
        expect(PROVIDER_ICONS[provider.id]).toBeDefined();
      }
    });

    it("all icon values are valid React components", () => {
      for (const [, icon] of Object.entries(PROVIDER_ICONS)) {
        expect(icon).toBeDefined();
        // lucide-react icons are forwardRef objects with a render function
        expect(icon.$$typeof || typeof icon === "function").toBeTruthy();
      }
    });
  });

  describe("CATEGORY_ICONS", () => {
    const allCategories: ProviderCategory[] = [
      "Major Providers",
      "Open Source / Inference",
      "Specialized",
      "Aggregators",
      "Search & Tools",
    ];

    it("has an icon for every category", () => {
      for (const category of allCategories) {
        expect(CATEGORY_ICONS[category]).toBeDefined();
      }
    });

    it("all category icon values are valid React components", () => {
      for (const [, icon] of Object.entries(CATEGORY_ICONS)) {
        expect(icon).toBeDefined();
        expect(icon.$$typeof || typeof icon === "function").toBeTruthy();
      }
    });
  });
});
