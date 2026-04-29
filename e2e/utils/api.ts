import { APIRequestContext, request } from "@playwright/test";

const baseURL = process.env.BASE_URL ?? "http://localhost:18001";

export async function apiClient(): Promise<APIRequestContext> {
  return request.newContext({ baseURL });
}

export async function listInstances(api: APIRequestContext) {
  const res = await api.get("/api/v1/instances");
  if (!res.ok()) throw new Error(`listInstances failed: ${res.status()}`);
  return res.json();
}

export async function getInstance(api: APIRequestContext, id: number | string) {
  const res = await api.get(`/api/v1/instances/${id}`);
  if (!res.ok()) throw new Error(`getInstance ${id} failed: ${res.status()}`);
  return res.json();
}

export async function deleteInstance(api: APIRequestContext, id: number | string) {
  const res = await api.delete(`/api/v1/instances/${id}`);
  if (!res.ok() && res.status() !== 404) {
    throw new Error(`deleteInstance ${id} failed: ${res.status()}`);
  }
}

export async function getHealth(api: APIRequestContext) {
  const res = await api.get("/health");
  return { ok: res.ok(), status: res.status() };
}
