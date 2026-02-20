# Claworc Frontend

React 18 + TypeScript + Vite + TailwindCSS v4 frontend for the Claworc control plane.

## Quick Start

```bash
npm install      # Install dependencies
npm run dev      # Start Vite dev server (proxies API to backend on :8000)
npm run build    # Production build (output in dist/)
npm run test     # Run all tests
npm run lint     # ESLint check
```

The dev server runs at http://localhost:5173. The production build is embedded into the Go binary via `//go:embed`.

## Project Structure

```
src/
├── pages/
│   ├── SettingsPage.tsx          # Settings page with 3 tabs
│   └── InstanceDetailPage.tsx    # Per-instance config (Monaco editor)
├── components/
│   ├── providers/                # LLM provider management (see below)
│   ├── settings/                 # Settings tab components
│   │   ├── LLMProvidersTab.tsx   # Providers tab (Brave key normalization)
│   │   ├── ResourceLimitsTab.tsx # CPU/memory/storage limits
│   │   └── AgentImageTab.tsx     # Container image config
│   └── ...                       # Other shared components
├── hooks/
│   └── useSettings.ts            # TanStack React Query hooks for settings
├── api/
│   └── settings.ts               # Axios API client for settings endpoints
└── types/
    └── settings.ts               # TypeScript interfaces for settings data
```

## Provider Management System

The provider management system allows users to configure API keys for 14 LLM and tool providers through a card-based UI.

### Component Architecture

```
SettingsPage
├── Tab: "LLM Providers"
│   └── LLMProvidersTab
│       └── ProviderGrid
│           ├── Search & filter controls
│           ├── BatchActionBar (when providers selected)
│           ├── ProviderCard (×14, grouped by category)
│           │   └── onClick → ProviderConfigModal
│           │       ├── Tab: "Configure" (API key + base URL inputs)
│           │       └── Tab: "Stats" → ProviderStatsTab
│           └── ProviderCardSkeleton (loading state)
├── Tab: "Resource Limits"
│   └── ResourceLimitsTab
└── Tab: "Agent Image"
    └── AgentImageTab
```

### Provider Components (`src/components/providers/`)

| File | Purpose |
|------|---------|
| `ProviderGrid.tsx` | Main grid layout with filtering, batch operations, and category grouping |
| `ProviderCard.tsx` | Individual card showing provider name, status, masked key, and actions |
| `ProviderConfigModal.tsx` | Modal for entering/updating API keys with validation and connection testing |
| `ProviderStatsTab.tsx` | Analytics visualization (requests, error rate, latency) |
| `BatchActionBar.tsx` | Batch test, export, and delete operations for selected providers |
| `ProviderCardSkeleton.tsx` | Loading placeholder with pulse animation |
| `providerData.ts` | Provider registry — the `PROVIDERS` array and `Provider` interface |
| `providerIcons.tsx` | Lucide icon mappings for providers and categories |
| `providerHealth.ts` | Health status derivation from analytics data |
| `validateApiKey.ts` | Client-side API key format validation rules |
| `index.ts` | Public exports |

### Supported Providers

| Category | Providers |
|----------|-----------|
| Major Providers | Anthropic, OpenAI, Google |
| Open Source / Inference | Mistral, Groq, DeepSeek, Together AI, Fireworks AI, Cerebras |
| Specialized | xAI, Cohere |
| Aggregators | Perplexity, OpenRouter |
| Search & Tools | Brave |

### How to Add a New Provider

Adding a provider requires changes in **one file** (optionally two):

**1. Add to the `PROVIDERS` array** in `src/components/providers/providerData.ts`:

```typescript
{
  id: "newprovider",                    // Unique lowercase slug
  name: "New Provider",                 // Display name
  envVarName: "NEWPROVIDER_API_KEY",    // Env var the agent reads
  category: "Major Providers",          // One of 5 ProviderCategory values
  description: "Short description of what this provider does.",
  docsUrl: "https://newprovider.com/api-keys",
  supportsBaseUrl: false,
  apiKeyPlaceholder: "np-...",          // Optional: key format hint
  brandColor: "#FF0000",               // Hex color for card accent
},
```

**2. Add an icon** in `src/components/providers/providerIcons.tsx`:

```typescript
import { SomeIcon } from "lucide-react";

export const PROVIDER_ICONS: Record<string, LucideIcon> = {
  // ...existing entries
  newprovider: SomeIcon,
};
```

**3. (Optional) Add format validation** in `src/components/providers/validateApiKey.ts`:

```typescript
case "newprovider":
  if (!trimmed.startsWith("np-")) {
    return { valid: false, message: 'Keys must start with "np-".' };
  }
  return { valid: true, message: "Valid key format." };
```

No backend changes are needed — the settings handler dynamically stores any key under `api_keys.*`.

After adding, update provider count assertions in tests and run `npm run test`.

### Brave API Key Special Case

The Brave key is stored separately on the backend as `brave_api_key` (not in `api_keys`). `LLMProvidersTab` normalizes it into `api_keys.BRAVE_API_KEY` on load and denormalizes it back on save, so `ProviderGrid` treats all providers uniformly.

### API Endpoints

| Method | Endpoint | Purpose |
|--------|----------|---------|
| `GET` | `/api/v1/settings` | Fetch all settings (keys are masked) |
| `PUT` | `/api/v1/settings` | Partial update (add/update/delete keys) |
| `POST` | `/api/v1/settings/test-provider-key` | Test a provider API key |
| `GET` | `/api/v1/analytics/providers` | Provider usage analytics (7-day window) |

## Testing

```bash
npm run test              # Run all tests
npm run test:coverage     # Run with coverage report
npm run lint              # ESLint
npx tsc --noEmit          # TypeScript type checking
```

Test files follow the naming convention `Component.{aspect}.test.tsx` (e.g., `ProviderGrid.batch.test.tsx`, `ProviderCard.health.test.tsx`).

## Further Reading

- [Development Guide](../../docs/development.md) — Local dev setup and commands
- [API Documentation](../../docs/api.md) — Full backend API reference
- [Architecture](../../docs/architecture.md) — System architecture overview
- [Provider Management Dev Docs](../../../claworc/Auto%20Run%20Docs/Wizard-2026-02-19-2/Working/Provider-Management-Dev-Docs.md) — Detailed developer reference for the provider system
