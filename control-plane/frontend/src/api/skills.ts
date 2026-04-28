import client from "./client";
import type {
  Skill,
  ClawhubSearchResponse,
  DeployResponse,
} from "@/types/skills";

export async function listSkills(): Promise<Skill[]> {
  const res = await client.get<Skill[]>("/skills");
  return res.data;
}

export async function uploadSkill(file: File, overwrite = false): Promise<Skill> {
  const form = new FormData();
  form.append("file", file);
  const res = await client.post<Skill>(`/skills${overwrite ? "?overwrite=true" : ""}`, form, {
    headers: { "Content-Type": "multipart/form-data" },
  });
  return res.data;
}

export async function deleteSkill(slug: string): Promise<void> {
  await client.delete(`/skills/${slug}`);
}

export async function searchClawhub(
  q: string,
  limit = 20,
): Promise<ClawhubSearchResponse> {
  const res = await client.get<ClawhubSearchResponse>("/skills/clawhub/search", {
    params: { q, limit },
  });
  return res.data;
}

export interface SkillFileEntry {
  path: string;
  size: number;
  binary: boolean;
}

export interface SkillFileContent {
  path: string;
  content: string;
  binary: boolean;
}

function encodeSkillPath(path: string): string {
  return path
    .split("/")
    .map((segment) => encodeURIComponent(segment))
    .join("/");
}

export async function listSkillFiles(slug: string): Promise<SkillFileEntry[]> {
  const res = await client.get<SkillFileEntry[]>(`/skills/${slug}/files`);
  return res.data;
}

export async function getSkillFile(slug: string, path: string): Promise<SkillFileContent> {
  const res = await client.get<SkillFileContent>(`/skills/${slug}/files/${encodeSkillPath(path)}`);
  return res.data;
}

export async function saveSkillFile(slug: string, path: string, content: string): Promise<void> {
  await client.put(`/skills/${slug}/files/${encodeSkillPath(path)}`, { content });
}

export async function deploySkill(
  slug: string,
  instanceIds: number[],
  source: "library" | "clawhub",
  version?: string,
): Promise<DeployResponse> {
  const res = await client.post<DeployResponse>(`/skills/${slug}/deploy`, {
    instance_ids: instanceIds,
    source,
    version,
  });
  return res.data;
}
