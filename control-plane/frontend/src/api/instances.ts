import client from "./client";
import type {
  Instance,
  InstanceDetail,
  InstanceCreatePayload,
  InstanceUpdatePayload,
  InstanceConfig,
  InstanceConfigUpdate,
} from "@/types/instance";

export async function fetchInstances(): Promise<Instance[]> {
  const { data } = await client.get<Instance[]>("/instances");
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

export async function updateInstanceImage(
  id: number,
  containerImage: string,
): Promise<{ status: string }> {
  const { data } = await client.post<{ status: string }>(
    `/instances/${id}/update-image`,
    { container_image: containerImage },
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

export async function updateOpenClaw(
  id: number,
): Promise<{ status: string; output: string }> {
  const { data } = await client.post<{ status: string; output: string }>(
    `/instances/${id}/update-openclaw`,
  );
  return data;
}

export async function fetchOpenClawVersion(
  id: number,
): Promise<{ installed: string; latest: string }> {
  const { data } = await client.get<{ installed: string; latest: string }>(
    `/instances/${id}/openclaw-version`,
  );
  return data;
}
