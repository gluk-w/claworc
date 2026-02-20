export type ProviderCategory =
  | "Major Providers"
  | "Open Source / Inference"
  | "Specialized"
  | "Aggregators"
  | "Search & Tools";

export interface Provider {
  id: string;
  name: string;
  envVarName: string;
  category: ProviderCategory;
  description: string;
  docsUrl: string;
  supportsBaseUrl: boolean;
  /** Placeholder hint shown in the API key input, e.g. "sk-ant-..." */
  apiKeyPlaceholder?: string;
  /** Placeholder hint shown in the base URL input */
  baseUrlPlaceholder?: string;
}

export const PROVIDERS: Provider[] = [
  // Major Providers
  {
    id: "anthropic",
    name: "Anthropic",
    envVarName: "ANTHROPIC_API_KEY",
    category: "Major Providers",
    description: "Power advanced reasoning and code generation with Claude.",
    docsUrl: "https://console.anthropic.com/settings/keys",
    supportsBaseUrl: false,
    apiKeyPlaceholder: "sk-ant-api03-...",
  },
  {
    id: "openai",
    name: "OpenAI",
    envVarName: "OPENAI_API_KEY",
    category: "Major Providers",
    description: "Access GPT and o-series models for versatile AI tasks.",
    docsUrl: "https://platform.openai.com/api-keys",
    supportsBaseUrl: true,
    apiKeyPlaceholder: "sk-...",
    baseUrlPlaceholder: "https://api.openai.com/v1",
  },
  {
    id: "google",
    name: "Google",
    envVarName: "GOOGLE_API_KEY",
    category: "Major Providers",
    description: "Run multimodal AI tasks with Gemini models.",
    docsUrl: "https://aistudio.google.com/apikey",
    supportsBaseUrl: false,
    apiKeyPlaceholder: "AIza...",
  },
  // Open Source / Inference
  {
    id: "mistral",
    name: "Mistral",
    envVarName: "MISTRAL_API_KEY",
    category: "Open Source / Inference",
    description: "Deploy efficient open-weight models for fast results.",
    docsUrl: "https://console.mistral.ai/api-keys/",
    supportsBaseUrl: false,
  },
  {
    id: "groq",
    name: "Groq",
    envVarName: "GROQ_API_KEY",
    category: "Open Source / Inference",
    description: "Get ultra-fast inference powered by LPU hardware.",
    docsUrl: "https://console.groq.com/keys",
    supportsBaseUrl: false,
    apiKeyPlaceholder: "gsk_...",
  },
  {
    id: "deepseek",
    name: "DeepSeek",
    envVarName: "DEEPSEEK_API_KEY",
    category: "Open Source / Inference",
    description: "Build cost-effective reasoning and coding pipelines.",
    docsUrl: "https://platform.deepseek.com/api_keys",
    supportsBaseUrl: false,
    apiKeyPlaceholder: "sk-...",
  },
  {
    id: "together",
    name: "Together AI",
    envVarName: "TOGETHER_API_KEY",
    category: "Open Source / Inference",
    description: "Run open-source models via scalable serverless inference.",
    docsUrl: "https://api.together.xyz/settings/api-keys",
    supportsBaseUrl: false,
  },
  {
    id: "fireworks",
    name: "Fireworks AI",
    envVarName: "FIREWORKS_API_KEY",
    category: "Open Source / Inference",
    description: "Host open-source models with fast, affordable inference.",
    docsUrl: "https://fireworks.ai/api-keys",
    supportsBaseUrl: false,
    apiKeyPlaceholder: "fw_...",
  },
  {
    id: "cerebras",
    name: "Cerebras",
    envVarName: "CEREBRAS_API_KEY",
    category: "Open Source / Inference",
    description: "Achieve blazing-fast generation with wafer-scale inference.",
    docsUrl: "https://cloud.cerebras.ai/",
    supportsBaseUrl: false,
    apiKeyPlaceholder: "csk-...",
  },
  // Specialized
  {
    id: "xai",
    name: "xAI",
    envVarName: "XAI_API_KEY",
    category: "Specialized",
    description: "Tap into real-time knowledge with Grok models.",
    docsUrl: "https://console.x.ai/",
    supportsBaseUrl: false,
    apiKeyPlaceholder: "xai-...",
  },
  {
    id: "cohere",
    name: "Cohere",
    envVarName: "COHERE_API_KEY",
    category: "Specialized",
    description: "Build enterprise search, RAG, and embedding pipelines.",
    docsUrl: "https://dashboard.cohere.com/api-keys",
    supportsBaseUrl: false,
  },
  // Aggregators
  {
    id: "perplexity",
    name: "Perplexity",
    envVarName: "PERPLEXITY_API_KEY",
    category: "Aggregators",
    description: "Get search-augmented AI answers with source citations.",
    docsUrl: "https://www.perplexity.ai/settings/api",
    supportsBaseUrl: false,
    apiKeyPlaceholder: "pplx-...",
  },
  {
    id: "openrouter",
    name: "OpenRouter",
    envVarName: "OPENROUTER_API_KEY",
    category: "Aggregators",
    description: "Access hundreds of models through one unified API.",
    docsUrl: "https://openrouter.ai/keys",
    supportsBaseUrl: false,
    apiKeyPlaceholder: "sk-or-...",
  },
  // Search & Tools
  {
    id: "brave",
    name: "Brave",
    envVarName: "BRAVE_API_KEY",
    category: "Search & Tools",
    description: "Ground AI responses with real-time web search data.",
    docsUrl: "https://brave.com/search/api/",
    supportsBaseUrl: false,
  },
];
