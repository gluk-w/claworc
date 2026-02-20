import { describe, it, expect } from "vitest";
import { validateApiKey } from "./validateApiKey";
import { PROVIDERS } from "./providerData";

function findProvider(id: string) {
  const p = PROVIDERS.find((p) => p.id === id);
  if (!p) throw new Error(`Provider not found: ${id}`);
  return p;
}

describe("validateApiKey", () => {
  describe("empty / whitespace keys", () => {
    it("rejects empty string for any provider", () => {
      const result = validateApiKey(findProvider("anthropic"), "");
      expect(result.valid).toBe(false);
      expect(result.message).toMatch(/empty/i);
    });

    it("rejects whitespace-only string", () => {
      const result = validateApiKey(findProvider("openai"), "   ");
      expect(result.valid).toBe(false);
      expect(result.message).toMatch(/empty/i);
    });
  });

  describe("Anthropic", () => {
    const provider = findProvider("anthropic");

    it("accepts key starting with sk-ant-", () => {
      const result = validateApiKey(provider, "sk-ant-abc123def456ghi789");
      expect(result.valid).toBe(true);
    });

    it("rejects key without sk-ant- prefix", () => {
      const result = validateApiKey(provider, "sk-proj-abc123def456ghi789");
      expect(result.valid).toBe(false);
      expect(result.message).toMatch(/sk-ant-/);
    });

    it("rejects random string", () => {
      const result = validateApiKey(provider, "randomstringhere");
      expect(result.valid).toBe(false);
    });
  });

  describe("OpenAI", () => {
    const provider = findProvider("openai");

    it("accepts key starting with sk-", () => {
      const result = validateApiKey(provider, "sk-proj-abc123def456ghi789");
      expect(result.valid).toBe(true);
    });

    it("rejects key without sk- prefix", () => {
      const result = validateApiKey(provider, "pk-abc123def456ghi789");
      expect(result.valid).toBe(false);
      expect(result.message).toMatch(/sk-/);
    });
  });

  describe("Google", () => {
    const provider = findProvider("google");

    it("accepts key starting with AI", () => {
      const result = validateApiKey(provider, "AIzaSyB_abc123def456");
      expect(result.valid).toBe(true);
    });

    it("rejects key without AI prefix", () => {
      const result = validateApiKey(provider, "GOOG-abc123def456");
      expect(result.valid).toBe(false);
      expect(result.message).toMatch(/AI/);
    });
  });

  describe("Brave", () => {
    const provider = findProvider("brave");

    it("accepts 32-character alphanumeric key", () => {
      const result = validateApiKey(provider, "abcdefghijklmnopqrstuvwxyz123456");
      expect(result.valid).toBe(true);
    });

    it("rejects key shorter than 32 characters", () => {
      const result = validateApiKey(provider, "abc123");
      expect(result.valid).toBe(false);
      expect(result.message).toMatch(/32/);
    });

    it("rejects key longer than 32 characters", () => {
      const result = validateApiKey(provider, "abcdefghijklmnopqrstuvwxyz1234567");
      expect(result.valid).toBe(false);
    });

    it("rejects key with special characters", () => {
      const result = validateApiKey(provider, "abcdefghijklmnopqrstuvwxyz12345!");
      expect(result.valid).toBe(false);
    });
  });

  describe("other providers (default validation)", () => {
    it("accepts keys with 8+ characters for Mistral", () => {
      const result = validateApiKey(findProvider("mistral"), "abcdefgh");
      expect(result.valid).toBe(true);
    });

    it("rejects keys shorter than 8 characters for Groq", () => {
      const result = validateApiKey(findProvider("groq"), "short");
      expect(result.valid).toBe(false);
      expect(result.message).toMatch(/short/i);
    });

    it("accepts long keys for DeepSeek", () => {
      const result = validateApiKey(
        findProvider("deepseek"),
        "sk-abcdefghijklmnopqrstuvwxyz123456789",
      );
      expect(result.valid).toBe(true);
    });

    it("accepts keys for OpenRouter", () => {
      const result = validateApiKey(
        findProvider("openrouter"),
        "sk-or-v1-abc123def456ghi789",
      );
      expect(result.valid).toBe(true);
    });
  });
});
