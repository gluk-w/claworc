# LLM Catalog Caching Architecture

## Where the catalog comes from

`models.csv` at the repo root is the source of truth. It is compiled into the worker at build time:

```
models.csv  --(website/worker/build-models.mjs)-->  website/worker/models.json
            --(deploy)-->  claworc.com/providers/
            --(1h in-process cache)-->  control-plane/internal/handlers/providers.go
```

**Editing `models.csv` has no effect until the worker is redeployed.** `models.json` is generated
(gitignored) and embedded into the worker bundle, so a merged CSV change sits dormant until a deploy
happens — this is what caused [#209](https://github.com/gluk-w/claworc/issues/209), where a fixed
model ID stayed invisible for two releases. Deployment is handled on the Cloudflare side.

### Keeping models.csv current

`make models` refreshes everything. It walks each source in turn and prompts for that source's
credential, and pressing Enter skips it — so you can refresh one provider or all of them from the same
command. Credentials already in the environment (`OPENAI_API_KEY`, `OPENROUTER_API_KEY`) are used
without prompting, which is also how it runs unattended. Nothing is echoed or passed on a command line.

The individual targets, if you want just one:

| Provider rows | Refreshed by |
|---|---|
| `openai` | `make openai-models` (`scripts/openai_to_csv.py openai`) |
| `openai-codex` | `make codex-models` — signs in via the Codex CLI, then probes |
| `openrouter` | `python3 scripts/openrouter_to_csv.py <api-key>` |
| Providers defined in the OpenClaw TS source | `make extract-models` (`scripts/extract_models.py`) |
| Everything else | by hand |

`scripts/openai_to_csv.py` has two modes. `openai` lists models from `GET /v1/models` with an API key
and enriches the pricing/context columns from OpenRouter's public catalog, because OpenAI's endpoint
returns nothing but model IDs. `openai-codex` has no list endpoint at all, so it probes each candidate
slug against `https://chatgpt.com/backend-api/codex/responses` with a ChatGPT OAuth access token and
keeps only the ones the backend does not reject — the only reliable way to learn which models a
ChatGPT-account Codex login actually accepts. The token is read out of the Codex CLI's
`$CODEX_HOME/auth.json` rather than passed as an argument, so it never reaches shell history or the
process table. Every `-codex`-suffixed slug is rejected on a ChatGPT account, which is the bug in #209.

`scripts/extract_models.py` merges rather than overwrites: providers it cannot find in the OpenClaw
source keep their existing rows and are reported at the end, and the cost/tag/description columns are
carried forward per model since the TS source does not carry them.

## External Catalog API

The external catalog lives at `https://claworc.com/providers/`. The root endpoint (`/`) returns a JSON array of all providers, each containing their full model list.

## In-Process Cache

A single cache entry keyed by `"/"` stores the raw JSON response from the root endpoint. It expires after 1 hour. All provider and model lookups are derived from this single cache entry — there are no per-provider cache entries.

### Key functions

- **`ensureRootCatalog()`** — Returns parsed `[]catalogRootEntry` from cache, fetching from the external API only if the cache is missing or expired.
- **`getCatalogRoot()`** — Force-refreshes the root cache by deleting the existing entry and fetching anew. Used by `SyncAllProviderModels`.
- **`getCatalogEntryByKey(key)`** — Finds a single provider entry by name from the cached root catalog.

### How handlers use the cache

| Handler | Behavior |
|---|---|
| `GetCatalogProviders` | Serves `catalogCache["/"]` directly via `proxyCatalog` |
| `GetCatalogProviderDetail` | Calls `getCatalogEntryByKey` → returns single entry from root cache, 404 if not found |
| `getCatalogModels(key)` | Calls `getCatalogEntryByKey` → converts models via `catalogModelToProviderModel` |
| `CreateProvider` | Calls `getCatalogModels` to auto-populate models for catalog providers |
| `SyncProviderModels` | Invalidates root cache, then calls `getCatalogModels` to re-fetch |
| `SyncAllProviderModels` | Calls `getCatalogRoot()` (force-refresh), iterates all providers |
