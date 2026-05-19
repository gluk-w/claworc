import client from "./client";
import type { InstanceWebhook, WebhookApiKey, WebhookLog } from "@common/types/webhook";

export async function fetchInstanceWebhook(instanceId: number): Promise<InstanceWebhook> {
  const { data } = await client.get<InstanceWebhook>(`/instances/${instanceId}/webhook`);
  return data;
}

export async function fetchInstanceWebhookLogs(
  instanceId: number,
  params?: { session_name?: string; limit?: number },
): Promise<WebhookLog[]> {
  const { data } = await client.get<WebhookLog[]>(`/instances/${instanceId}/webhook/logs`, {
    params,
  });
  return data;
}

export async function createInstanceWebhookKey(
  instanceId: number,
  payload: { label?: string; is_private?: boolean },
): Promise<WebhookApiKey> {
  const { data } = await client.post<WebhookApiKey>(
    `/instances/${instanceId}/webhook/keys`,
    payload,
  );
  return data;
}

export async function updateInstanceWebhookKey(
  instanceId: number,
  keyId: number,
  payload: { label?: string; is_private?: boolean },
): Promise<WebhookApiKey> {
  const { data } = await client.patch<WebhookApiKey>(
    `/instances/${instanceId}/webhook/keys/${keyId}`,
    payload,
  );
  return data;
}

export async function regenerateInstanceWebhookKey(
  instanceId: number,
  keyId: number,
  payload?: { label?: string; is_private?: boolean },
): Promise<WebhookApiKey> {
  const { data } = await client.post<WebhookApiKey>(
    `/instances/${instanceId}/webhook/keys/${keyId}/regenerate`,
    payload ?? {},
  );
  return data;
}

export async function deleteInstanceWebhookKey(
  instanceId: number,
  keyId: number,
): Promise<void> {
  await client.delete(`/instances/${instanceId}/webhook/keys/${keyId}`);
}
