# LLM Catalog Caching Architecture

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
