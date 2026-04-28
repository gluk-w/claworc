import providersJson from "./models.json";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface Model {
  model_id: string;
  model_name: string;
  reasoning: boolean;
  vision: boolean;
  context_window: number | null;
  max_tokens: number | null;
  input_cost: number | null;
  output_cost: number | null;
  cached_read_cost: number | null;
  cached_write_cost: number | null;
  tag: string | null;
  description: string | null;
}

interface Provider {
  name: string;
  label: string;
  icon_key: string | null;
  api_format: string | null;
  base_url: string | null;
  models: Model[];
}

const providers = providersJson as Provider[];

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// ---------------------------------------------------------------------------
// Route handlers
// ---------------------------------------------------------------------------

function handleProviderList(): Response {
  return jsonResponse(providers);
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

    return jsonResponse({ error: "Not found" }, 404);
  },
} satisfies ExportedHandler;
