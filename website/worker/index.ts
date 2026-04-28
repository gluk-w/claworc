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
// Analytics
// ---------------------------------------------------------------------------

// Allowlist mirrors control-plane/internal/analytics/events.go. Unknown event
// names are rejected so a misbehaving or compromised installation can't spray
// arbitrary strings at our analytics dataset.
const ALLOWED_EVENTS = new Set([
  "instance_created",
  "instance_deleted",
  "skill_uploaded",
  "skill_deleted",
  "shared_folder_created",
  "shared_folder_deleted",
  "backup_schedule_created",
  "backup_created_manual",
  "user_created",
  "user_updated",
  "user_deleted",
  "password_changed",
  "provider_added",
  "provider_deleted",
  "ssh_key_rotated",
  "global_env_vars_edited",
  "instance_env_vars_edited",
  "opt_out",
]);

const INSTALLATION_ID_RE = /^[a-f0-9]{32}$/;

interface AnalyticsPayload {
  installation_id: string;
  event: string;
  props?: Record<string, unknown>;
  ts: number;
  version: string;
}

interface Env {
  ANALYTICS?: {
    writeDataPoint(point: {
      blobs?: string[];
      doubles?: number[];
      indexes?: string[];
    }): void;
  };
}

function corsHeaders(): HeadersInit {
  // Customer deployments live on arbitrary infra, so the collector accepts
  // any origin. The endpoint never returns sensitive data.
  return {
    "Access-Control-Allow-Origin": "*",
    "Access-Control-Allow-Methods": "POST, OPTIONS",
    "Access-Control-Allow-Headers": "Content-Type",
  };
}

async function handleStatsCollect(request: Request, env: Env): Promise<Response> {
  if (request.method === "OPTIONS") {
    return new Response(null, { status: 204, headers: corsHeaders() });
  }
  if (request.method !== "POST") {
    return new Response("Method not allowed", { status: 405, headers: corsHeaders() });
  }

  let body: AnalyticsPayload;
  try {
    body = (await request.json()) as AnalyticsPayload;
  } catch {
    return jsonResponse({ error: "Invalid JSON" }, 400);
  }

  if (!body || typeof body !== "object") {
    return jsonResponse({ error: "Invalid payload" }, 400);
  }
  if (typeof body.installation_id !== "string" || !INSTALLATION_ID_RE.test(body.installation_id)) {
    return jsonResponse({ error: "Invalid installation_id" }, 400);
  }
  if (typeof body.event !== "string" || !ALLOWED_EVENTS.has(body.event)) {
    return jsonResponse({ error: "Unknown event" }, 400);
  }
  if (typeof body.ts !== "number") {
    return jsonResponse({ error: "Invalid ts" }, 400);
  }
  if (typeof body.version !== "string") {
    return jsonResponse({ error: "Invalid version" }, 400);
  }

  // Serialize props to a single blob — Analytics Engine doesn't take maps.
  const propsBlob = body.props ? JSON.stringify(body.props) : "";

  if (env.ANALYTICS) {
    env.ANALYTICS.writeDataPoint({
      blobs: [body.event, body.version, propsBlob],
      doubles: [body.ts],
      indexes: [body.installation_id],
    });
  }

  return new Response(null, { status: 204, headers: corsHeaders() });
}

// ---------------------------------------------------------------------------
// Response helpers
// ---------------------------------------------------------------------------

function jsonResponse(data: unknown, status = 200): Response {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json", ...corsHeaders() },
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
  async fetch(request: Request, env: Env): Promise<Response> {
    const { pathname } = new URL(request.url);

    if (pathname === "/providers/" || pathname === "/providers") {
      return handleProviderList();
    }

    if (pathname === "/stats/collect") {
      return handleStatsCollect(request, env);
    }

    return jsonResponse({ error: "Not found" }, 404);
  },
} satisfies ExportedHandler<Env>;
