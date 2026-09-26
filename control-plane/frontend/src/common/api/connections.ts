import client from "./client";
import type {
  Connection,
  InitiateConnectionResponse,
  ConfirmConnectionResponse,
  Toolkit,
} from "@common/types/connection";

export async function fetchToolkits(): Promise<Toolkit[]> {
  const { data } = await client.get<Toolkit[]>("/connections/toolkits");
  return data;
}

export async function fetchConnections(
  instanceId: number,
): Promise<Connection[]> {
  const { data } = await client.get<Connection[]>(
    `/instances/${instanceId}/connections`,
  );
  return data;
}

export async function initiateConnection(
  instanceId: number,
  toolkit: Toolkit,
  callbackUrl: string,
): Promise<InitiateConnectionResponse> {
  const { data } = await client.post<InitiateConnectionResponse>(
    `/instances/${instanceId}/connections`,
    {
      toolkit_slug: toolkit.slug,
      toolkit_name: toolkit.name,
      callback_url: callbackUrl,
    },
  );
  return data;
}

export async function confirmConnection(
  instanceId: number,
  toolkit: Toolkit,
  connectedAccountId: string,
): Promise<ConfirmConnectionResponse> {
  const { data } = await client.post<ConfirmConnectionResponse>(
    `/instances/${instanceId}/connections/confirm`,
    {
      connected_account_id: connectedAccountId,
      toolkit_slug: toolkit.slug,
      toolkit_name: toolkit.name,
    },
  );
  return data;
}

export async function deleteConnection(
  instanceId: number,
  connectionId: number,
): Promise<void> {
  await client.delete(`/instances/${instanceId}/connections/${connectionId}`);
}
