import { vi, describe, it, expect } from "vitest";

vi.mock("./models.json", () => ({
  default: [
    {
      name: "anthropic",
      label: "Anthropic",
      icon_key: "anthropic",
      api_format: "anthropic-messages",
      base_url: "https://api.anthropic.com/",
      models: [
        {
          model_id: "claude-3-opus-20240229",
          model_name: "Claude 3 Opus",
          reasoning: false,
          vision: true,
          context_window: 200000,
          max_tokens: 4096,
          input_cost: 5,
          output_cost: 25,
          cached_read_cost: 0.5,
          cached_write_cost: null,
          tag: "flagship",
          description: "Most capable model",
        },
        {
          model_id: "claude-3-sonnet-20240229",
          model_name: "Claude 3 Sonnet",
          reasoning: false,
          vision: true,
          context_window: 200000,
          max_tokens: 4096,
          input_cost: 3,
          output_cost: 15,
          cached_read_cost: null,
          cached_write_cost: null,
          tag: null,
          description: null,
        },
      ],
    },
    {
      name: "openai",
      label: "OpenAI",
      icon_key: "openai",
      api_format: "openai",
      base_url: "https://api.openai.com/",
      models: [
        {
          model_id: "gpt-4o",
          model_name: "GPT-4o",
          reasoning: false,
          vision: true,
          context_window: 128000,
          max_tokens: 4096,
          input_cost: 2.5,
          output_cost: 10,
          cached_read_cost: null,
          cached_write_cost: null,
          tag: null,
          description: null,
        },
      ],
    },
  ],
}));

import worker from "./index";

async function get(path: string): Promise<Response> {
  return worker.fetch(new Request(`https://example.com${path}`), {} as any, {} as any);
}

describe("Provider list", () => {
  it("GET /providers/ returns 200", async () => {
    const res = await get("/providers/");
    expect(res.status).toBe(200);
  });

  it("GET /providers returns same as /providers/", async () => {
    const res = await get("/providers");
    expect(res.status).toBe(200);
    const data = await res.json();
    expect(Array.isArray(data)).toBe(true);
  });

  it("response is array of providers with expected fields", async () => {
    const res = await get("/providers/");
    const data = (await res.json()) as Record<string, unknown>[];
    expect(data[0]).toHaveProperty("name");
    expect(data[0]).toHaveProperty("label");
    expect(data[0]).toHaveProperty("api_format");
    expect(data[0]).toHaveProperty("models");
    expect(Array.isArray(data[0].models)).toBe(true);
  });

  it("providers are sorted alphabetically", async () => {
    const res = await get("/providers/");
    const data = (await res.json()) as { name: string }[];
    expect(data.map((p) => p.name)).toEqual(["anthropic", "openai"]);
  });

  it("model count per provider is correct", async () => {
    const res = await get("/providers/");
    const data = (await res.json()) as { name: string; models: unknown[] }[];
    const anthropic = data.find((p) => p.name === "anthropic")!;
    const openai = data.find((p) => p.name === "openai")!;
    expect(anthropic.models.length).toBe(2);
    expect(openai.models.length).toBe(1);
  });

  it("models include cost and tag/description fields", async () => {
    const res = await get("/providers/");
    const data = (await res.json()) as { name: string; models: Record<string, unknown>[] }[];
    const anthropic = data.find((p) => p.name === "anthropic")!;
    const m = anthropic.models[0];
    expect(m).toHaveProperty("input_cost");
    expect(m).toHaveProperty("output_cost");
    expect(m).toHaveProperty("cached_read_cost");
    expect(m).toHaveProperty("cached_write_cost");
    expect(m).toHaveProperty("tag");
    expect(m).toHaveProperty("description");
    expect(m.input_cost).toBe(5);
    expect(m.output_cost).toBe(25);
    expect(m.cached_read_cost).toBe(0.5);
    expect(m.cached_write_cost).toBeNull();
    expect(m.tag).toBe("flagship");
    expect(m.description).toBe("Most capable model");
  });
});

describe("Analytics collect", () => {
  function makeEnv() {
    const points: any[] = [];
    return {
      env: {
        ANALYTICS: {
          writeDataPoint: (p: any) => points.push(p),
        },
      },
      points,
    };
  }

  async function post(path: string, body: unknown, env: any = {}) {
    return worker.fetch(
      new Request(`https://example.com${path}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: typeof body === "string" ? body : JSON.stringify(body),
      }),
      env,
      {} as any,
    );
  }

  const validPayload = {
    installation_id: "a".repeat(32),
    event: "instance_created",
    props: { total_instances: 3 },
    ts: 1700000000,
    version: "1.2.3",
  };

  it("accepts a well-formed payload and writes one data point", async () => {
    const { env, points } = makeEnv();
    const res = await post("/stats/collect", validPayload, env);
    expect(res.status).toBe(204);
    expect(points.length).toBe(1);
    expect(points[0].indexes).toEqual([validPayload.installation_id]);
    expect(points[0].blobs?.[0]).toBe("instance_created");
  });

  it("rejects unknown events", async () => {
    const { env, points } = makeEnv();
    const res = await post("/stats/collect", { ...validPayload, event: "bogus_event" }, env);
    expect(res.status).toBe(400);
    expect(points.length).toBe(0);
  });

  it("rejects malformed installation_id", async () => {
    const { env } = makeEnv();
    const res = await post("/stats/collect", { ...validPayload, installation_id: "short" }, env);
    expect(res.status).toBe(400);
  });

  it("rejects malformed JSON", async () => {
    const { env } = makeEnv();
    const res = await post("/stats/collect", "{not-json", env);
    expect(res.status).toBe(400);
  });

  it("rejects GET", async () => {
    const res = await worker.fetch(new Request("https://example.com/stats/collect"), {} as any, {} as any);
    expect(res.status).toBe(405);
  });

  it("answers OPTIONS preflight", async () => {
    const res = await worker.fetch(
      new Request("https://example.com/stats/collect", { method: "OPTIONS" }),
      {} as any,
      {} as any,
    );
    expect(res.status).toBe(204);
    expect(res.headers.get("Access-Control-Allow-Origin")).toBe("*");
  });
});

describe("404 routes", () => {
  it("GET /something-else returns 404", async () => {
    const res = await get("/something-else");
    expect(res.status).toBe(404);
  });

  it("GET /providers/anthropic returns 404", async () => {
    const res = await get("/providers/anthropic");
    expect(res.status).toBe(404);
  });

  it("GET /providers/anthropic/model returns 404", async () => {
    const res = await get("/providers/anthropic/model");
    expect(res.status).toBe(404);
  });
});
