// Knobs for the per-agent-type (@e2e) suites. Everything is env-driven so the
// same specs run against a fresh Helm install (run.sh) or the local dev
// dashboard (BASE_URL=http://localhost:5173).

export const ALL_AGENT_TYPES = ["openclaw", "hermes", "nanoclaw"] as const;
export type AgentType = (typeof ALL_AGENT_TYPES)[number];

// E2E_AGENT_TYPES=hermes,nanoclaw limits which instances globalSetup creates
// (pair it with --project to limit which suites run).
export function enabledAgentTypes(): AgentType[] {
  const raw = process.env.E2E_AGENT_TYPES;
  if (!raw) return [...ALL_AGENT_TYPES];
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter((s): s is AgentType => (ALL_AGENT_TYPES as readonly string[]).includes(s));
}

// E2E_IMAGE_OPENCLAW / E2E_IMAGE_HERMES / E2E_IMAGE_NANOCLAW override the
// container image per type (e.g. a locally built image). Unset = the
// dashboard's configured default for that type.
export function imageOverride(type: AgentType): string | undefined {
  return process.env[`E2E_IMAGE_${type.toUpperCase()}`] || undefined;
}

// LLM wiring for chat/webhook tests: a provider key already configured in the
// dashboard (Settings → Model API Keys) and one of its models. The model
// should be cheap — every run makes a handful of real LLM calls per type.
export const LLM_PROVIDER_KEY = process.env.E2E_PROVIDER_KEY ?? "anthropic";
export const LLM_MODEL = process.env.E2E_MODEL ?? "claude-haiku-4-5";

// Where each image's shim installs skills (the `skills_dir` its `meta` verb
// declares — agent/<type>/shim/meta).
export const SKILLS_DIR: Record<AgentType, string> = {
  openclaw: "/home/claworc/.openclaw/skills",
  hermes: "/home/claworc/.hermes/skills",
  nanoclaw: "/home/claworc/.claude/skills",
};

// Every shim image logs to this file (`log_files[0]` in meta); the Logs tab
// streams it.
export const AGENT_LOG_PATH = "/var/log/claworc/agent.log";
