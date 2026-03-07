# OpenClaw Provider & Model Extraction

This document describes how OpenClaw provider and model catalog data is structured in the upstream
source and how `scripts/extract_models.py` extracts it into `models.csv`.

## Source Location

All provider and model definitions live in the `openclaw-github` repository under `src/`:

```
~/openclaw-github/src/
  agents/
    together-models.ts          # Together AI catalog
    venice-models.ts            # Venice AI catalog (+ live API discovery)
    synthetic-models.ts         # Synthetic catalog
    huggingface-models.ts       # Hugging Face catalog
    doubao-models.ts            # Volcano Engine (Doubao) catalog
    byteplus-models.ts          # BytePlus catalog
    volc-models.shared.ts       # Shared Volc constants (bare refs)
    models-config.providers.ts  # Inline provider builder functions
  providers/
    kilocode-shared.ts          # Kilocode catalog
```

## Provider Inventory

| Provider Key   | Label                    | Icon Key (`@lobehub/icons`) | Source Type |
|----------------|--------------------------|------------------------------|-------------|
| `together`     | Together AI              | `togetherai`                 | file        |
| `venice`       | Venice AI                | —                            | file        |
| `synthetic`    | Synthetic                | —                            | file        |
| `kilocode`     | Kilocode                 | —                            | file        |
| `huggingface`  | Hugging Face             | `huggingface`                | file        |
| `volcengine`   | Volcano Engine (Doubao)  | `volcengine`                 | file        |
| `byteplus`     | BytePlus                 | `bytedance`                  | file        |
| `openai`       | OpenAI                   | `openai`                     | hardcoded   |
| `openai-codex` | OpenAI Codex             | `openai`                     | hardcoded   |
| `minimax`      | MiniMax                  | `minimax`                    | func        |
| `moonshot`     | Moonshot (Kimi)          | `moonshot`                   | func        |
| `kimi-coding`  | Kimi Coding              | `moonshot`                   | func        |
| `qwen-portal`  | Qwen Portal              | `qwen`                       | func        |
| `openrouter`   | OpenRouter               | `openrouter`                 | func        |
| `qianfan`      | Qianfan (Baidu)          | `wenxin`                     | func        |
| `nvidia`       | NVIDIA                   | `nvidia`                     | func        |
| `xiaomi`       | Xiaomi MiMo              | `xiaomimimo`                 | func        |
| `anthropic`    | Anthropic                | `anthropic`                  | hardcoded   |

**Source types:**
- `file` — models are in a standalone `*_MODEL_CATALOG` array in a dedicated file
- `func` — models are in a `buildXxxProvider()` function body in `models-config.providers.ts`
- `hardcoded` — models are defined directly in the extraction script (no upstream TS source)

**Icon keys** are `ModelProvider` enum values from
[`@lobehub/icons`](https://github.com/lobehub/lobe-icons). Empty means no icon exists in that
library for the provider.

## Model Catalog Patterns

### Pattern 1: File-based catalog array

```typescript
// e.g. src/agents/together-models.ts
export const TOGETHER_MODEL_CATALOG = [
  {
    id: "meta-llama/Llama-3.3-70B-Instruct-Turbo",
    name: "Llama 3.3 70B Instruct Turbo",
    reasoning: false,
    input: ["text"],
    contextWindow: 131072,
    maxTokens: 8192,
  },
  // ...
] as const;
```

The `input` array determines vision support: presence of `"image"` means `vision: true`.

### Pattern 2: Named constant references

Some catalogs use string constants for `id` or `name`:

```typescript
// src/providers/kilocode-shared.ts
export const KILOCODE_DEFAULT_MODEL_ID = "anthropic/claude-opus-4.6";
export const KILOCODE_DEFAULT_MODEL_NAME = "Claude Opus 4.6";

export const KILOCODE_MODEL_CATALOG = [
  {
    id: KILOCODE_DEFAULT_MODEL_ID,   // resolved from constant
    name: KILOCODE_DEFAULT_MODEL_NAME,
    // ...
  },
];
```

The script resolves these by pre-extracting all `const NAME = "value"` declarations.

### Pattern 3: Bare ref (volc shared objects)

`doubao-models.ts` and `byteplus-models.ts` include bare identifier references to objects defined
in `volc-models.shared.ts`:

```typescript
// src/agents/volc-models.shared.ts
export const VOLC_MODEL_KIMI_K2_5 = {
  id: "kimi-k2-5-260127",
  name: "Kimi K2.5",
  // ...
} as const;

// src/agents/doubao-models.ts
export const DOUBAO_MODEL_CATALOG = [
  { id: "ark-code-latest", name: "Ark Coding Plan", ... },
  VOLC_MODEL_KIMI_K2_5,   // bare ref — resolved by pre-parsing shared file
  VOLC_MODEL_GLM_4_7,
];
```

### Pattern 4: Function-body inline models (`models-config.providers.ts`)

Providers like `minimax`, `moonshot`, `nvidia`, etc. are defined as builder functions:

```typescript
export function buildMinimaxProvider(...): ModelProvider {
  return {
    models: [
      buildMinimaxTextModel({ id: "MiniMax-Text-01", name: "MiniMax Text 01", ... }),
      buildMinimaxModel({ id: "MiniMax-M2.1", name: "MiniMax M2.1", reasoning: true, ... }),
    ],
    // ...
  };
}
```

The script extracts the `models: [...]` array from each function body and applies
function-specific defaults (`buildMinimaxTextModel` → text-only input).

## CSV Output

Running `make extract-models` (or `python3 scripts/extract_models.py`) produces `models.csv` in
the project root with these columns:

| Column           | Description |
|------------------|-------------|
| `provider_key`   | OpenClaw internal provider key |
| `provider_label` | Human-readable provider name |
| `icon_key`       | `@lobehub/icons` `ModelProvider` enum value (empty if no icon) |
| `api_format`     | API protocol (`openai-completions` or `anthropic-messages`) |
| `base_url`       | Provider API base URL |
| `model_id`       | Model ID string used in API calls |
| `model_name`     | Display name |
| `reasoning`      | `true` / `false` |
| `vision`         | `true` / `false` (derived from `input` containing `"image"`) |
| `context_window` | Context window in tokens (empty if unknown) |
| `max_tokens`     | Max output tokens (empty if unknown) |

## Adding a New Provider

### File-based provider

1. Add a new `*-models.ts` file in `~/openclaw-github/src/agents/` following the catalog array pattern.
2. Add an entry to `PROVIDERS` in `scripts/extract_models.py`:

```python
{
    "key": "myprovider",
    "label": "My Provider",
    "icon_key": "myprovider",          # @lobehub/icons ModelProvider key, or "" if none
    "api_format": "openai-completions",
    "base_url": "https://api.myprovider.com/v1",
    "type": "file",
    "file": "agents/myprovider-models.ts",
    "catalog_var": "MYPROVIDER_MODEL_CATALOG",
},
```

### Function-based provider

1. Add a `buildMyProviderProvider()` function to `models-config.providers.ts` with a `models: [...]` array.
2. Add an entry to `PROVIDERS`:

```python
{
    "key": "myprovider",
    "label": "My Provider",
    "icon_key": "",
    "api_format": "openai-completions",
    "base_url": "https://api.myprovider.com/v1",
    "type": "func",
    "func": "buildMyProviderProvider",
},
```

### Hardcoded provider (no upstream TS catalog)

Add an entry with `"type": "hardcoded"` and an explicit `"models"` list:

```python
{
    "key": "myprovider",
    "label": "My Provider",
    "icon_key": "anthropic",
    "api_format": "anthropic-messages",
    "base_url": "https://api.myprovider.com",
    "type": "hardcoded",
    "models": [
        {
            "model_id": "my-model-1",
            "model_name": "My Model 1",
            "reasoning": "true",
            "vision": "false",
            "context_window": "200000",
            "max_tokens": "64000",
        },
    ],
},
```
