#!/usr/bin/env node
// Build script: reads ../../models.csv and writes models.json for the worker.
// Run via: node build-models.mjs

import { readFileSync, writeFileSync } from "fs";
import { fileURLToPath } from "url";
import { join, dirname } from "path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const csvPath = join(__dirname, "../../models.csv");
const outPath = join(__dirname, "models.json");

const text = readFileSync(csvPath, "utf8");
const lines = text.trim().split("\n");
const headers = lines[0].split(",").map((h) => h.trim());

const numCols = headers.length;

const rows = lines
  .slice(1)
  .map((line) => {
    const parts = line.split(",");
    // Join any extra trailing parts back into the last column (handles commas in description)
    const values =
      parts.length > numCols
        ? [...parts.slice(0, numCols - 1), parts.slice(numCols - 1).join(",")]
        : parts;
    const row = {};
    headers.forEach((h, i) => {
      row[h] = (values[i] ?? "").trim();
    });
    return row;
  })
  .filter((r) => r.model_id && r.provider_key);

// Group by provider
const providerMap = new Map();
for (const row of rows) {
  const key = row.provider_key;
  if (!providerMap.has(key)) {
    providerMap.set(key, {
      name: key,
      label: row.provider_label,
      icon_key: row.icon_key || null,
      api_format: row.api_format || null,
      base_url: row.base_url || null,
      models: [],
    });
  }
  const prov = providerMap.get(key);
  prov.models.push({
    model_id: row.model_id,
    model_name: row.model_name,
    reasoning: row.reasoning.toLowerCase() === "true",
    vision: row.vision.toLowerCase() === "true",
    context_window: row.context_window ? parseInt(row.context_window, 10) : null,
    max_tokens: row.max_tokens ? parseInt(row.max_tokens, 10) : null,
    input_cost: row.input_cost ? parseFloat(row.input_cost) : null,
    output_cost: row.output_cost ? parseFloat(row.output_cost) : null,
    cached_read_cost: row.cached_read_cost ? parseFloat(row.cached_read_cost) : null,
    cached_write_cost: row.cached_write_cost ? parseFloat(row.cached_write_cost) : null,
    tag: row.tag || null,
    description: row.description || null,
  });
}

// Sort providers alphabetically
const providers = Array.from(providerMap.values()).sort((a, b) =>
  a.name.localeCompare(b.name)
);

const json = JSON.stringify(providers, null, 2);
writeFileSync(outPath, json);
console.log(`Wrote ${providers.length} providers to ${outPath}`);

// Also write the embedded catalog for the Go control plane
const catalogPath = join(__dirname, "../../control-plane/internal/handlers/catalog_embed.json");
writeFileSync(catalogPath, json);
console.log(`Wrote ${providers.length} providers to ${catalogPath}`);
