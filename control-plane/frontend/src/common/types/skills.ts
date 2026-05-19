export interface Skill {
  id: number;
  slug: string;
  name: string;
  summary: string;
  /** Env var names this skill declares it needs (parsed from SKILL.md frontmatter). */
  required_env_vars: string[];
  created_at: string;
  updated_at: string;
}

export interface ClawhubResult {
  score: number;
  slug: string;
  displayName: string;
  summary: string;
  version: string;
  updatedAt: string;
}

export interface ClawhubSearchResponse {
  results: ClawhubResult[];
}

export interface DeployResult {
  instance_id: number;
  status: "ok" | "error";
  error?: string;
  /** Names of env vars the skill requires that are not defined on this instance (globally or per-instance). */
  missing_env_vars?: string[];
}

export interface DeployResponse {
  /** Synchronous fallback (no TaskMgr wired). */
  results?: DeployResult[];
  /** Async path: one task ID per target instance. Per-instance results arrive via SSE/toasts. */
  task_ids?: string[];
}
