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
}

export const PROVIDERS: Provider[] = [
  // Major Providers
  {
    id: "anthropic",
    name: "Anthropic",
    envVarName: "ANTHROPIC_API_KEY",
    category: "Major Providers",
    description: "Claude models for advanced reasoning and analysis.",
    docsUrl: "https://console.anthropic.com/settings/keys",
    supportsBaseUrl: false,
  },
  {
    id: "openai",
    name: "OpenAI",
    envVarName: "OPENAI_API_KEY",
    category: "Major Providers",
    description: "GPT and o-series models. Supports Azure and proxy base URLs.",
    docsUrl: "https://platform.openai.com/api-keys",
    supportsBaseUrl: true,
  },
  {
    id: "google",
    name: "Google",
    envVarName: "GOOGLE_API_KEY",
    category: "Major Providers",
    description: "Gemini models for multimodal AI tasks.",
    docsUrl: "https://aistudio.google.com/apikey",
    supportsBaseUrl: false,
  },
  // Open Source / Inference
  {
    id: "mistral",
    name: "Mistral",
    envVarName: "MISTRAL_API_KEY",
    category: "Open Source / Inference",
    description: "Open-weight models optimized for efficiency.",
    docsUrl: "https://console.mistral.ai/api-keys/",
    supportsBaseUrl: false,
  },
  {
    id: "groq",
    name: "Groq",
    envVarName: "GROQ_API_KEY",
    category: "Open Source / Inference",
    description: "Ultra-fast inference on LPU hardware.",
    docsUrl: "https://console.groq.com/keys",
    supportsBaseUrl: false,
  },
  {
    id: "deepseek",
    name: "DeepSeek",
    envVarName: "DEEPSEEK_API_KEY",
    category: "Open Source / Inference",
    description: "Cost-effective reasoning and coding models.",
    docsUrl: "https://platform.deepseek.com/api_keys",
    supportsBaseUrl: false,
  },
  {
    id: "together",
    name: "Together AI",
    envVarName: "TOGETHER_API_KEY",
    category: "Open Source / Inference",
    description: "Run leading open-source models via serverless inference.",
    docsUrl: "https://api.together.xyz/settings/api-keys",
    supportsBaseUrl: false,
  },
  {
    id: "fireworks",
    name: "Fireworks AI",
    envVarName: "FIREWORKS_API_KEY",
    category: "Open Source / Inference",
    description: "Fast and affordable open-source model hosting.",
    docsUrl: "https://fireworks.ai/api-keys",
    supportsBaseUrl: false,
  },
  {
    id: "cerebras",
    name: "Cerebras",
    envVarName: "CEREBRAS_API_KEY",
    category: "Open Source / Inference",
    description: "Wafer-scale inference for blazing-fast generation.",
    docsUrl: "https://cloud.cerebras.ai/",
    supportsBaseUrl: false,
  },
  // Specialized
  {
    id: "xai",
    name: "xAI",
    envVarName: "XAI_API_KEY",
    category: "Specialized",
    description: "Grok models with real-time knowledge.",
    docsUrl: "https://console.x.ai/",
    supportsBaseUrl: false,
  },
  {
    id: "cohere",
    name: "Cohere",
    envVarName: "COHERE_API_KEY",
    category: "Specialized",
    description: "Enterprise NLP, RAG, and embedding models.",
    docsUrl: "https://dashboard.cohere.com/api-keys",
    supportsBaseUrl: false,
  },
  // Aggregators
  {
    id: "perplexity",
    name: "Perplexity",
    envVarName: "PERPLEXITY_API_KEY",
    category: "Aggregators",
    description: "Search-augmented AI responses with citations.",
    docsUrl: "https://www.perplexity.ai/settings/api",
    supportsBaseUrl: false,
  },
  {
    id: "openrouter",
    name: "OpenRouter",
    envVarName: "OPENROUTER_API_KEY",
    category: "Aggregators",
    description: "Unified API gateway to hundreds of models.",
    docsUrl: "https://openrouter.ai/keys",
    supportsBaseUrl: false,
  },
  // Search & Tools
  {
    id: "brave",
    name: "Brave",
    envVarName: "BRAVE_API_KEY",
    category: "Search & Tools",
    description: "Web search API for grounded AI responses.",
    docsUrl: "https://brave.com/search/api/",
    supportsBaseUrl: false,
  },
];
