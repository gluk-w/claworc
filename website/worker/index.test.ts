import { vi, describe, it, expect } from "vitest";

vi.mock("./models.csv", () => ({
  default: [
    "provider_key,provider_label,icon_key,api_format,base_url,model_id,model_name,reasoning,vision,context_window,max_tokens,input_cost,output_cost,cached_read_cost,cached_write_cost",
    "anthropic,Anthropic,anthropic,anthropic,https://api.anthropic.com,claude-3-opus-20240229,Claude 3 Opus,FALSE,TRUE,200000,4096,5,25,0.5,",
    "anthropic,Anthropic,anthropic,anthropic,https://api.anthropic.com,claude-3-sonnet-20240229,Claude 3 Sonnet,FALSE,TRUE,200000,4096,3,15,,",
    "openai,OpenAI,openai,openai,https://api.openai.com,gpt-4o,GPT-4o,FALSE,TRUE,128000,4096,2.5,10,,",
  ].join("\n"),
}));

import worker from "./index";

async function get(path: string): Promise<Response> {
  return worker.fetch(new Request(`https://example.com${path}`));
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

  it("provider list has name, label, api_format, model_count but no slug/boolean caps", async () => {
    const res = await get("/providers/");
    const data = (await res.json()) as Record<string, unknown>[];
    expect(data[0]).toHaveProperty("name");
    expect(data[0]).toHaveProperty("label");
    expect(data[0]).toHaveProperty("api_format");
    expect(data[0]).toHaveProperty("model_count");
    expect(data[0]).not.toHaveProperty("slug");
    expect(data[0]).not.toHaveProperty("has_reasoning");
    expect(data[0]).not.toHaveProperty("has_vision");
  });

  it("providers are sorted alphabetically", async () => {
    const res = await get("/providers/");
    const data = (await res.json()) as { name: string }[];
    expect(data.map((p) => p.name)).toEqual(["anthropic", "openai"]);
  });

  it("model_count is correct per provider", async () => {
    const res = await get("/providers/");
    const data = (await res.json()) as { name: string; model_count: number }[];
    const anthropic = data.find((p) => p.name === "anthropic")!;
    const openai = data.find((p) => p.name === "openai")!;
    expect(anthropic.model_count).toBe(2);
    expect(openai.model_count).toBe(1);
  });
});

describe("Provider detail", () => {
  it("GET /providers/anthropic/ returns 200 with correct shape", async () => {
    const res = await get("/providers/anthropic/");
    expect(res.status).toBe(200);
    const data = (await res.json()) as Record<string, unknown>;
    expect(data).toMatchObject({
      key: "anthropic",
      label: "Anthropic",
      api_format: "anthropic",
      base_url: "https://api.anthropic.com",
    });
    expect(Array.isArray(data.models)).toBe(true);
    expect((data.models as unknown[]).length).toBe(2);
  });

  it("provider detail models omit base_url, api_format, slug", async () => {
    const res = await get("/providers/anthropic/");
    const data = (await res.json()) as { models: Record<string, unknown>[] };
    const m = data.models[0];
    expect(m).not.toHaveProperty("base_url");
    expect(m).not.toHaveProperty("api_format");
    expect(m).not.toHaveProperty("slug");
  });

  it("GET /providers/unknown returns 404 JSON", async () => {
    const res = await get("/providers/unknown");
    expect(res.status).toBe(404);
    const data = (await res.json()) as { error: string };
    expect(data).toHaveProperty("error");
  });
});

describe("Model detail", () => {
  it("GET /providers/anthropic/claude-3-opus-20240229 returns 200", async () => {
    const res = await get("/providers/anthropic/claude-3-opus-20240229");
    expect(res.status).toBe(200);
    const data = (await res.json()) as Record<string, unknown>;
    expect(data).toMatchObject({
      provider_key: "anthropic",
      provider_label: "Anthropic",
      model_id: "claude-3-opus-20240229",
    });
  });

  it("boolean fields normalized: uppercase TRUE → true, FALSE → false", async () => {
    const res = await get("/providers/anthropic/claude-3-opus-20240229");
    const data = (await res.json()) as { vision: boolean; reasoning: boolean };
    expect(data.vision).toBe(true);
    expect(data.reasoning).toBe(false);
  });

  it("numeric fields are integers", async () => {
    const res = await get("/providers/anthropic/claude-3-opus-20240229");
    const data = (await res.json()) as {
      context_window: number;
      max_tokens: number;
    };
    expect(data.context_window).toBe(200000);
    expect(data.max_tokens).toBe(4096);
  });

  it("cost fields are floats or null", async () => {
    const res = await get("/providers/anthropic/claude-3-opus-20240229");
    const data = (await res.json()) as {
      input_cost: number | null;
      output_cost: number | null;
      cached_read_cost: number | null;
      cached_write_cost: number | null;
    };
    expect(data.input_cost).toBe(5);
    expect(data.output_cost).toBe(25);
    expect(data.cached_read_cost).toBe(0.5);
    expect(data.cached_write_cost).toBeNull();
  });

  it("GET /providers/anthropic/no-such-model returns 404 JSON", async () => {
    const res = await get("/providers/anthropic/no-such-model");
    expect(res.status).toBe(404);
    const data = (await res.json()) as { error: string };
    expect(data).toHaveProperty("error");
  });
});

describe("404 routes", () => {
  it("GET /something-else returns 404", async () => {
    const res = await get("/something-else");
    expect(res.status).toBe(404);
  });

  it("GET /providers/unknown/model returns 404", async () => {
    const res = await get("/providers/unknown/model");
    expect(res.status).toBe(404);
  });
});
