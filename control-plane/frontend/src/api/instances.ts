import client from "./client";
import type {
  Instance,
  InstanceDetail,
  InstanceCreatePayload,
  InstanceUpdatePayload,
  InstanceConfig,
  InstanceConfigUpdate,
  InstanceStats,
} from "@/types/instance";

export async function fetchInstances(teamId?: number): Promise<Instance[]> {
  const params = teamId ? { team_id: teamId } : undefined;
  const { data } = await client.get<Instance[]>("/instances", { params });
  return data;
}

export async function fetchInstance(id: number): Promise<InstanceDetail> {
  const { data } = await client.get<InstanceDetail>(`/instances/${id}`);
  return data;
}

export async function createInstance(
  payload: InstanceCreatePayload,
): Promise<InstanceDetail> {
  const { data } = await client.post<InstanceDetail>("/instances", payload);
  return data;
}

export async function updateInstance(
  id: number,
  payload: InstanceUpdatePayload,
): Promise<InstanceDetail> {
  const { data } = await client.put<InstanceDetail>(`/instances/${id}`, payload);
  return data;
}

export async function setInstanceBrowserActive(
  id: number,
  active: boolean,
): Promise<{ browser_active: boolean }> {
  const { data } = await client.patch<{ browser_active: boolean }>(
    `/instances/${id}/browser-active`,
    { browser_active: active },
  );
  return data;
}

export async function deleteInstance(id: number): Promise<void> {
  await client.delete(`/instances/${id}`);
}

export async function startInstance(
  id: number,
): Promise<{ status: string }> {
  const { data } = await client.post<{ status: string }>(
    `/instances/${id}/start`,
  );
  return data;
}

export async function stopInstance(
  id: number,
): Promise<{ status: string }> {
  const { data } = await client.post<{ status: string }>(
    `/instances/${id}/stop`,
  );
  return data;
}

export async function restartInstance(
  id: number,
): Promise<{ status: string }> {
  const { data } = await client.post<{ status: string }>(
    `/instances/${id}/restart`,
  );
  return data;
}

export async function fetchInstanceConfig(
  id: number,
): Promise<InstanceConfig> {
  const { data } = await client.get<InstanceConfig>(`/instances/${id}/config`);
  return data;
}

export async function updateInstanceConfig(
  id: number,
  config: string,
): Promise<InstanceConfigUpdate> {
  const { data } = await client.put<InstanceConfigUpdate>(
    `/instances/${id}/config`,
    { config },
  );
  return data;
}

export async function cloneInstance(
  id: number,
): Promise<InstanceDetail> {
  const { data } = await client.post<InstanceDetail>(
    `/instances/${id}/clone`,
  );
  return data;
}

export async function reorderInstances(orderedIds: number[]): Promise<void> {
  await client.put("/instances/reorder", { ordered_ids: orderedIds });
}

export async function fetchInstanceStats(
  id: number,
): Promise<InstanceStats> {
  const { data } = await client.get<InstanceStats>(`/instances/${id}/stats`);
  return data;
}

export async function updateInstanceImage(id: number): Promise<void> {
  await client.post(`/instances/${id}/update-image`);
}
