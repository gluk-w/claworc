export interface Skill {
  id: number;
  slug: string;
  name: string;
  summary: string;
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
}

export interface DeployResponse {
  results: DeployResult[];
}
