/**
 * Shared fixtures for the shim-contract image suites (hermes.test.ts,
 * nanoclaw.test.ts) and global-setup.ts. Single source of truth for the
 * boot-time LLM routing document each container is started with, and for
 * the exact files the shims derive from it — so the suites can assert the
 * CLAWORC_INITIAL_LLM_CONFIG boot path without duplicating literals.
 */

export const PROXY_URL = "http://127.0.0.1:40001";
export const DEFAULT_MODEL = "anthropic/claude-sonnet-4-5";

export interface LlmProvider {
  key: string;
  api_key: string;
  api_type?: string;
  models?: { id: string }[];
}

/** The generic `configure-llm` routing document (docs/shim.md). */
export interface LlmRoutingDoc {
  proxy_url: string;
  style: "openai" | "anthropic";
  default_model: string;
  fallback_models: string[];
  providers: LlmProvider[];
}

export type ShimRole = "hermes" | "nanoclaw";

/**
 * Routing document injected as CLAWORC_INITIAL_LLM_CONFIG at container start
 * (global-setup.ts). Nothing listens on PROXY_URL during tests, so chat turns
 * fail fast with connection refused instead of hanging on auth.
 */
export function llmConfigFor(role: ShimRole): LlmRoutingDoc {
  return {
    proxy_url: PROXY_URL,
    // Each image declares exactly one dialect in meta.llm.styles.
    style: role === "hermes" ? "openai" : "anthropic",
    default_model: DEFAULT_MODEL,
    fallback_models: [],
    providers: [
      {
        key: "anthropic",
        api_key: `claworc-vk-test-${role}`,
        api_type: "anthropic-messages",
        models: [{ id: DEFAULT_MODEL }],
      },
    ],
  };
}

/**
 * User-facing agent names. Must match `DisplayName` for the corresponding
 * agent type in control-plane/internal/agentshim/registry.go — the image's
 * agent.txt is what the instance list shows once the shim is probed.
 */
export const DISPLAY_NAMES: Record<ShimRole, string> = {
  hermes: "Hermes",
  nanoclaw: "NanoClaw",
};

/** `language` values allowed for meta.config_files[].language (docs/shim.md). */
export const CONFIG_LANGUAGES = ["json", "yaml", "toml", "ini", "shell", "plaintext"];

// ── Hermes ──────────────────────────────────────────────────────────────

/**
 * What agent/hermes/shim/lib/configure-llm.py derives from a routing
 * document: Hermes' "custom" provider is an OpenAI client, so base_url gets
 * /v1, the model loses its "<provider>/" prefix, and the virtual key comes
 * from the provider that prefix names (first provider as fallback).
 */
export function hermesModelConfig(doc: LlmRoutingDoc) {
  const prefix = doc.default_model.includes("/") ? doc.default_model.split("/")[0] : "";
  const provider = doc.providers.find((p) => prefix && p.key === prefix) ?? doc.providers[0];
  const key = provider?.key ?? "";
  const model =
    key && doc.default_model.startsWith(`${key}/`) ? doc.default_model.slice(key.length + 1) : doc.default_model;
  return {
    provider: "custom",
    base_url: doc.proxy_url ? `${doc.proxy_url.replace(/\/+$/, "")}/v1` : "",
    api_key: provider?.api_key ?? "",
    default: model,
  };
}

/**
 * The managed block agent/hermes/shim/lib/configure-llm.py writes into
 * ~/.hermes/config.yaml. Values are json.dumps'd (valid YAML double-quoted
 * scalars), so mirror that with JSON.stringify.
 */
export function hermesManagedBlock(doc: LlmRoutingDoc): string[] {
  const m = hermesModelConfig(doc);
  return [
    "# BEGIN claworc-managed",
    "# Managed by the Claworc shim configure-llm verb - do not edit inside this block.",
    "# Routes all Hermes LLM traffic to the Claworc LLM proxy with a virtual key.",
    "model:",
    '  provider: "custom"',
    `  base_url: ${JSON.stringify(m.base_url)}`,
    `  api_key: ${JSON.stringify(m.api_key)}`,
    `  default: ${JSON.stringify(m.default)}`,
    "# END claworc-managed",
  ];
}

/** Expected `meta` document — copied from agent/hermes/shim/meta. */
export function hermesMeta(version: string) {
  return {
    contract: 1,
    shim_version: "0.1.0",
    agent: { name: "hermes", version },
    capabilities: ["chat", "chat.stream", "chat.abort", "session.reset", "config", "configure-llm", "restart", "skills"],
    config_files: [
      {
        id: "config",
        path: "/home/claworc/.hermes/config.yaml",
        language: "yaml",
        label: "config.yaml",
        restart_required: false,
      },
      {
        id: "env",
        path: "/home/claworc/.hermes/.env",
        language: "shell",
        label: ".env",
        restart_required: false,
      },
    ],
    workspace_dir: "/home/claworc/hermes-workspace",
    skills_dir: "/home/claworc/.hermes/skills",
    log_files: [{ path: "/var/log/claworc/agent.log", label: "Agent" }],
    llm: { styles: ["openai"] },
    session_persistence: "native",
  };
}

// ── NanoClaw ────────────────────────────────────────────────────────────

/**
 * The fully-managed llm.json agent/nanoclaw/shim/lib/configure-llm.mjs
 * writes (deterministic key order, whole-file rewrite).
 */
export function nanoclawLlmJson(doc: LlmRoutingDoc) {
  const provider =
    doc.providers.find((p) => p.api_type?.includes("anthropic")) ??
    doc.providers.find((p) => p.key === "anthropic") ??
    doc.providers[0];
  return {
    _comment: "Managed by the Claworc shim configure-llm verb - do not edit.",
    style: "anthropic",
    proxy_url: doc.proxy_url,
    api_key: provider?.api_key ?? "",
    default_model: doc.default_model,
    fallback_models: doc.fallback_models,
  };
}

/** Expected `meta` document — copied from agent/nanoclaw/shim/meta. */
export function nanoclawMeta(version: string) {
  return {
    contract: 1,
    shim_version: "0.1.0",
    agent: { name: "nanoclaw", version },
    capabilities: ["chat", "chat.stream", "chat.abort", "session.reset", "config", "configure-llm", "restart", "skills"],
    config_files: [
      {
        id: "main",
        path: "/home/claworc/workspace/container.json",
        language: "json",
        label: "container.json",
        restart_required: true,
      },
    ],
    workspace_dir: "/home/claworc/workspace",
    skills_dir: "/home/claworc/.claude/skills",
    log_files: [{ path: "/var/log/claworc/agent.log", label: "Agent" }],
    llm: { styles: ["anthropic"] },
    session_persistence: "native",
    chat_end_detection: "exact",
  };
}
