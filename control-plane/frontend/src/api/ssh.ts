import client from "./client";
import type { SSHStatusResponse, SSHTestResponse, SSHEventsResponse, SSHReconnectResponse, SSHFingerprintResponse } from "@/types/ssh";

export async function fetchSSHStatus(instanceId: number): Promise<SSHStatusResponse> {
  const { data } = await client.get<SSHStatusResponse>(`/instances/${instanceId}/ssh-status`);
  return data;
}

export type SSHTarget = "agent" | "browser";

export async function testSSHConnection(instanceId: number, target?: SSHTarget): Promise<SSHTestResponse> {
  const url = target ? `/instances/${instanceId}/ssh-test?target=${target}` : `/instances/${instanceId}/ssh-test`;
  const { data } = await client.get<SSHTestResponse>(url);
  return data;
}

export async function fetchSSHEvents(instanceId: number): Promise<SSHEventsResponse> {
  const { data } = await client.get<SSHEventsResponse>(`/instances/${instanceId}/ssh-events`);
  return data;
}

export async function reconnectSSH(instanceId: number, target?: SSHTarget): Promise<SSHReconnectResponse> {
  const url = target ? `/instances/${instanceId}/ssh-reconnect?target=${target}` : `/instances/${instanceId}/ssh-reconnect`;
  const { data } = await client.post<SSHReconnectResponse>(url);
  return data;
}

export async function fetchSSHFingerprint(): Promise<SSHFingerprintResponse> {
  const { data } = await client.get<SSHFingerprintResponse>(`/ssh-fingerprint`);
  return data;
}

export async function rotateSSHKey(): Promise<any> {
  const { data } = await client.post("/settings/rotate-ssh-key");
  return data;
}
