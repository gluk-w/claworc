import type { Provider } from "./providerData";

export interface ValidationResult {
  valid: boolean;
  message: string;
}

/**
 * Client-side API key format validation by provider.
 * This only checks format/prefix — it does not call the provider API.
 */
export function validateApiKey(
  provider: Provider,
  key: string,
): ValidationResult {
  const trimmed = key.trim();

  if (!trimmed) {
    return { valid: false, message: "API key cannot be empty." };
  }

  switch (provider.id) {
    case "anthropic":
      if (!trimmed.startsWith("sk-ant-")) {
        return {
          valid: false,
          message: 'Anthropic keys must start with "sk-ant-".',
        };
      }
      return { valid: true, message: "Valid Anthropic key format." };

    case "openai":
      if (!trimmed.startsWith("sk-")) {
        return {
          valid: false,
          message: 'OpenAI keys must start with "sk-".',
        };
      }
      return { valid: true, message: "Valid OpenAI key format." };

    case "google":
      if (!trimmed.startsWith("AI")) {
        return {
          valid: false,
          message: 'Google keys must start with "AI".',
        };
      }
      return { valid: true, message: "Valid Google key format." };

    case "brave":
      if (!/^[a-zA-Z0-9]{32}$/.test(trimmed)) {
        return {
          valid: false,
          message: "Brave keys must be exactly 32 alphanumeric characters.",
        };
      }
      return { valid: true, message: "Valid Brave key format." };

    default:
      if (trimmed.length < 8) {
        return {
          valid: false,
          message: "Key seems too short. Please check and try again.",
        };
      }
      return { valid: true, message: "Key format looks valid." };
  }
}
