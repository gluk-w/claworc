import { APIRequestContext, request } from "@playwright/test";
import {
  readSharedInstance,
  readAllSharedInstances,
  writeSharedInstances,
  SharedInstanceState,
} from "./utils/sharedInstance";
import { SHARED_ENV_VARS } from "./utils/sharedEnv";
import {
  AgentType,
  enabledAgentTypes,
  imageOverride,
  LLM_MODEL,
  LLM_PROVIDER_KEY,
} from "./utils/e2eConfig";

const baseURL = process.env.BASE_URL ?? "http://localhost:18001";

async function findProviderId(api: APIRequestContext): Promise<number | null> {
  const res = await api.get("/api/v1/llm/providers");
  if (!res.ok()) return null;
  const providers = (await res.json()) as Array<{ id: number; key: string }>;
  return providers.find((p) => p.key === LLM_PROVIDER_KEY)?.id ?? null;
}

// Instances belong to a team; use E2E_TEAM_ID or the first team listed
// (a fresh install seeds "Default Team").
async function findTeamId(api: APIRequestContext): Promise<number | undefined> {
  if (process.env.E2E_TEAM_ID) return Number(process.env.E2E_TEAM_ID);
  const res = await api.get("/api/v1/teams");
  if (!res.ok()) return undefined;
  const teams = (await res.json()) as Array<{ id: number }>;
  return teams[0]?.id;
}

async function createAndWait(
  api: APIRequestContext,
  type: AgentType,
  providerId: number | null,
  teamId: number | undefined,
): Promise<SharedInstanceState> {
  const display_name = `e2e-${type}-${Date.now()}`;
  const model = `${LLM_PROVIDER_KEY}/${LLM_MODEL}`;
  const data: Record<string, unknown> = {
    display_name,
    agent_type: type,
    env_vars_set: SHARED_ENV_VARS,
    team_id: teamId,
  };
  const image = imageOverride(type);
  if (image) data.container_image = image;
  if (providerId !== null) {
    data.enabled_providers = [providerId];
    data.models = { disabled: [], extra: [model] };
    data.default_model = model;
  }

  // eslint-disable-next-line no-console
  console.log(`[globalSetup] creating ${type} instance ${display_name}${image ? ` (${image})` : ""}`);
  const created = await api.post("/api/v1/instances", {
    headers: { "Content-Type": "application/json" },
    data,
  });
  if (!created.ok()) {
    throw new Error(`globalSetup: ${type} create failed: ${created.status()} ${await created.text()}`);
  }
  const { id } = (await created.json()) as { id: number };

  const deadline = Date.now() + 6 * 60_000;
  while (Date.now() < deadline) {
    const res = await api.get(`/api/v1/instances/${id}`);
    if (res.ok() && ((await res.json()) as { status: string }).status === "running") {
      // eslint-disable-next-line no-console
      console.log(`[globalSetup] ${type} instance ${id} is running`);
      return {
        id,
        display_name,
        agent_type: type,
        llm_model: providerId !== null ? model : undefined,
      };
    }
    await new Promise((r) => setTimeout(r, 2000));
  }
  throw new Error(`globalSetup: ${type} instance ${id} never reached status=running`);
}

// E2E_REUSE_INSTANCES=1 keeps instances from a previous run (see
// E2E_KEEP_INSTANCES in globalTeardown) when they are still running — handy
// when iterating on a single spec.
async function reusable(api: APIRequestContext, type: AgentType): Promise<SharedInstanceState | null> {
  if (!process.env.E2E_REUSE_INSTANCES) return null;
  const prev = readSharedInstance(type);
  if (!prev) return null;
  const res = await api.get(`/api/v1/instances/${prev.id}`);
  if (!res.ok() || ((await res.json()) as { status: string }).status !== "running") return null;
  // eslint-disable-next-line no-console
  console.log(`[globalSetup] reusing ${type} instance ${prev.id}`);
  return prev;
}

export default async function globalSetup() {
  const api = await request.newContext({ baseURL });
  try {
    const providerId = await findProviderId(api);
    const teamId = await findTeamId(api);
    if (providerId === null) {
      // eslint-disable-next-line no-console
      console.warn(
        `[globalSetup] no LLM provider with key "${LLM_PROVIDER_KEY}" — chat and webhook specs will skip`,
      );
    }
    // Agent boots take minutes; create every type concurrently. Record the
    // ones that come up even if another fails, so teardown can clean them.
    const types = enabledAgentTypes();
    const results = await Promise.allSettled(
      types.map(async (t) => (await reusable(api, t)) ?? createAndWait(api, t, providerId, teamId)),
    );
    // Keep other types' entries (a run limited by E2E_AGENT_TYPES must not
    // forget instances kept from an earlier run).
    const state: Record<string, SharedInstanceState> = process.env.E2E_REUSE_INSTANCES
      ? Object.fromEntries(readAllSharedInstances().map((i) => [i.agent_type, i]))
      : {};
    const errors: string[] = [];
    results.forEach((r, i) => {
      if (r.status === "fulfilled") state[types[i]] = r.value;
      else errors.push(String(r.reason));
    });
    writeSharedInstances(state);
    if (errors.length > 0) throw new Error(errors.join("\n"));
  } finally {
    await api.dispose();
  }
}
