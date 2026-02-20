import { describe, it, expect } from "vitest";
import { PROVIDERS, type Provider, type ProviderCategory } from "./providerData";

describe("providerData", () => {
  describe("PROVIDERS array", () => {
    it("has 14 entries (13 LLM + 1 Brave)", () => {
      expect(PROVIDERS).toHaveLength(14);

      const braveProviders = PROVIDERS.filter((p) => p.id === "brave");
      expect(braveProviders).toHaveLength(1);

      const llmProviders = PROVIDERS.filter((p) => p.id !== "brave");
      expect(llmProviders).toHaveLength(13);
    });
  });

  describe("required fields", () => {
    it.each(PROVIDERS.map((p) => [p.name, p]))(
      "%s has all required fields",
      (_name, provider) => {
        const p = provider as Provider;
        expect(p.id).toBeDefined();
        expect(typeof p.id).toBe("string");
        expect(p.id.length).toBeGreaterThan(0);

        expect(p.name).toBeDefined();
        expect(typeof p.name).toBe("string");
        expect(p.name.length).toBeGreaterThan(0);

        expect(p.envVarName).toBeDefined();
        expect(typeof p.envVarName).toBe("string");
        expect(p.envVarName.length).toBeGreaterThan(0);

        expect(p.category).toBeDefined();
        expect(typeof p.category).toBe("string");

        expect(p.description).toBeDefined();
        expect(typeof p.description).toBe("string");
        expect(p.description.length).toBeGreaterThan(0);

        expect(p.docsUrl).toBeDefined();
        expect(typeof p.docsUrl).toBe("string");
        expect(p.docsUrl.length).toBeGreaterThan(0);
      },
    );
  });

  describe("docsUrl values are valid URLs", () => {
    it.each(PROVIDERS.map((p) => [p.name, p.docsUrl]))(
      "%s has a valid docsUrl",
      (_name, docsUrl) => {
        expect(() => new URL(docsUrl as string)).not.toThrow();
      },
    );
  });

  describe("category grouping", () => {
    const grouped = PROVIDERS.reduce(
      (acc, p) => {
        if (!acc[p.category]) acc[p.category] = [];
        acc[p.category].push(p);
        return acc;
      },
      {} as Record<ProviderCategory, Provider[]>,
    );

    it("has 5 categories", () => {
      expect(Object.keys(grouped)).toHaveLength(5);
    });

    it("Major Providers has Anthropic, OpenAI, Google", () => {
      const ids = grouped["Major Providers"].map((p) => p.id);
      expect(ids).toEqual(
        expect.arrayContaining(["anthropic", "openai", "google"]),
      );
      expect(ids).toHaveLength(3);
    });

    it("Open Source / Inference has Mistral, Groq, DeepSeek, Together AI, Fireworks AI, Cerebras", () => {
      const ids = grouped["Open Source / Inference"].map((p) => p.id);
      expect(ids).toEqual(
        expect.arrayContaining([
          "mistral",
          "groq",
          "deepseek",
          "together",
          "fireworks",
          "cerebras",
        ]),
      );
      expect(ids).toHaveLength(6);
    });

    it("Specialized has xAI and Cohere", () => {
      const ids = grouped["Specialized"].map((p) => p.id);
      expect(ids).toEqual(expect.arrayContaining(["xai", "cohere"]));
      expect(ids).toHaveLength(2);
    });

    it("Aggregators has Perplexity and OpenRouter", () => {
      const ids = grouped["Aggregators"].map((p) => p.id);
      expect(ids).toEqual(
        expect.arrayContaining(["perplexity", "openrouter"]),
      );
      expect(ids).toHaveLength(2);
    });

    it("Search & Tools has Brave", () => {
      const ids = grouped["Search & Tools"].map((p) => p.id);
      expect(ids).toEqual(expect.arrayContaining(["brave"]));
      expect(ids).toHaveLength(1);
    });
  });

  describe("Brave provider", () => {
    it("is in the Search & Tools category", () => {
      const brave = PROVIDERS.find((p) => p.id === "brave");
      expect(brave).toBeDefined();
      expect(brave!.category).toBe("Search & Tools");
    });
  });

  describe("OpenAI provider", () => {
    it("has supportsBaseUrl set to true", () => {
      const openai = PROVIDERS.find((p) => p.id === "openai");
      expect(openai).toBeDefined();
      expect(openai!.supportsBaseUrl).toBe(true);
    });
  });

  describe("unique ids", () => {
    it("all providers have unique ids", () => {
      const ids = PROVIDERS.map((p) => p.id);
      const uniqueIds = new Set(ids);
      expect(uniqueIds.size).toBe(ids.length);
    });
  });

  describe("brand colors", () => {
    it.each(PROVIDERS.map((p) => [p.name, p]))(
      "%s has a valid hex brand color",
      (_name, provider) => {
        const p = provider as Provider;
        expect(p.brandColor).toBeDefined();
        expect(p.brandColor).toMatch(/^#[0-9A-Fa-f]{6}$/);
      },
    );
  });
});
