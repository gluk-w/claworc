import client from "./client";
import type {
  SSHStatusResponse,
  SSHEventsResponse,
  SSHTestResult,
  SSHReconnectResponse,
  SSHFingerprintResponse,
  GlobalSSHStatusResponse,
} from "@/types/ssh";

export async function fetchSSHStatus(
  instanceId: number,
): Promise<SSHStatusResponse> {
  const { data } = await client.get<SSHStatusResponse>(
    `/instances/${instanceId}/ssh-status`,
  );
  return data;
}

export async function fetchSSHEvents(
  instanceId: number,
): Promise<SSHEventsResponse> {
  const { data } = await client.get<SSHEventsResponse>(
    `/instances/${instanceId}/ssh-events`,
  );
  return data;
}

export async function testSSHConnection(
  instanceId: number,
): Promise<SSHTestResult> {
  const { data } = await client.post<SSHTestResult>(
    `/instances/${instanceId}/ssh-test`,
  );
  return data;
}

export async function reconnectSSH(
  instanceId: number,
): Promise<SSHReconnectResponse> {
  const { data } = await client.post<SSHReconnectResponse>(
    `/instances/${instanceId}/ssh-reconnect`,
  );
  return data;
}

export async function fetchSSHFingerprint(
  instanceId: number,
): Promise<SSHFingerprintResponse> {
  const { data } = await client.get<SSHFingerprintResponse>(
    `/instances/${instanceId}/ssh-fingerprint`,
  );
  return data;
}

export async function fetchGlobalSSHStatus(): Promise<GlobalSSHStatusResponse> {
  const { data } = await client.get<GlobalSSHStatusResponse>("/ssh-status");
  return data;
}
