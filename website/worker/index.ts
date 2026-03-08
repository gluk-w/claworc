import modelsCsv from "./models.csv";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface ModelRow {
  provider_key: string;
  provider_label: string;
  icon_key: string;
  api_format: string;
  base_url: string;
  model_id: string;
  model_name: string;
  reasoning: string;
  vision: string;
  context_window: string;
  max_tokens: string;
  input_cost: string;
  output_cost: string;
  cached_read_cost: string;
  cached_write_cost: string;
}

interface Provider {
  label: string;
  icon_key: string;
  api_format: string;
  models: ModelRow[];
}

// ---------------------------------------------------------------------------
// CSV parsing
// ---------------------------------------------------------------------------

function parseCsv(text: string): ModelRow[] {
  const lines = text.trim().split("\n");
  const headers = lines[0].split(",");
  return lines
    .slice(1)
    .map((line) => {
      const values = line.split(",");
      const row: Record<string, string> = {};
      headers.forEach((h, i) => {
        row[h.trim()] = (values[i] ?? "").trim();
      });
      return row as unknown as ModelRow;
    })
    .filter((r) => r.model_id && r.provider_key);
}

function toSlug(s: string): string {
  return s.toLowerCase().replace(/[/.]/g, "-");
}

function modelSlug(providerKey: string, modelId: string): string {
  const prefix = providerKey + "/";
  const id = modelId.startsWith(prefix) ? modelId.slice(prefix.length) : modelId;
  return toSlug(id);
}

// ---------------------------------------------------------------------------
// Build lookup maps (module scope — parsed once)
// ---------------------------------------------------------------------------

const rows = parseCsv(modelsCsv);

const providers: Record<string, Provider> = {};
for (const row of rows) {
  const key = row.provider_key;
  if (!providers[key]) {
    providers[key] = { label: row.provider_label, icon_key: row.icon_key ?? "", api_format: row.api_format ?? "", models: [] };
  }
  providers[key].models.push(row);
}

const modelsBySlug: Record<string, ModelRow> = {};
for (const row of rows) {
  modelsBySlug[`${row.provider_key}/${modelSlug(row.provider_key, row.model_id)}`] = row;
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function notFound(message: string): Response {
  return jsonResponse({ error: message }, 404);
}

function serializeModel(m: ModelRow) {
  return {
    model_id: m.model_id,
    model_name: m.model_name,
    reasoning: m.reasoning.toLowerCase() === "true",
    vision: m.vision.toLowerCase() === "true",
    context_window: m.context_window ? parseInt(m.context_window, 10) : null,
    max_tokens: m.max_tokens ? parseInt(m.max_tokens, 10) : null,
    input_cost: m.input_cost ? parseFloat(m.input_cost) : null,
    output_cost: m.output_cost ? parseFloat(m.output_cost) : null,
    cached_read_cost: m.cached_read_cost ? parseFloat(m.cached_read_cost) : null,
    cached_write_cost: m.cached_write_cost ? parseFloat(m.cached_write_cost) : null,
  };
}

// ---------------------------------------------------------------------------
// Route handlers
// ---------------------------------------------------------------------------

function handleProviderList(): Response {
  const data = Object.entries(providers)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, prov]) => ({
      name: key,
      label: prov.label,
      icon_key: prov.icon_key || null,
      api_format: prov.api_format,
      model_count: prov.models.length,
    }));
  return jsonResponse(data);
}

function handleProviderDetail(providerKey: string): Response {
  const prov = providers[providerKey];
  if (!prov) return notFound(`Provider "${providerKey}" not found`);
  const base_url = prov.models[0]?.base_url || null;
  return jsonResponse({
    key: providerKey,
    label: prov.label,
    icon_key: prov.icon_key || null,
    api_format: prov.api_format,
    base_url,
    models: prov.models.map(serializeModel),
  });
}

function handleModelDetail(providerKey: string, slug: string): Response {
  const prov = providers[providerKey];
  if (!prov) return notFound(`Provider "${providerKey}" not found`);

  const m = modelsBySlug[`${providerKey}/${slug}`];
  if (!m) return notFound(`Model "${slug}" not found in provider "${providerKey}"`);

  return jsonResponse({
    provider_key: providerKey,
    provider_label: prov.label,
    provider_icon_key: prov.icon_key || null,
    ...serializeModel(m),
  });
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

export default {
  async fetch(request: Request): Promise<Response> {
    const { pathname } = new URL(request.url);

    if (pathname === "/providers/" || pathname === "/providers") {
      return handleProviderList();
    }

    const provMatch = pathname.match(/^\/providers\/([^/]+)\/?$/);
    if (provMatch) return handleProviderDetail(provMatch[1]);

    const modelMatch = pathname.match(/^\/providers\/([^/]+)\/([^/]+)\/?$/);
    if (modelMatch) return handleModelDetail(modelMatch[1], modelMatch[2]);

    return notFound("Not found");
  },
} satisfies ExportedHandler;
