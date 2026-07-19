import client from "./client";

export interface UserSSHKey {
  id: number;
  name: string;
  fingerprint: string;
  created_at: string;
  last_used_at?: string;
}

export interface GenerateSSHKeyResult {
  key: UserSSHKey;
  private_key: string;
}

export interface SSHGatewayInfo {
  enabled: boolean;
  port: number;
  host: string;
}

export async function listSSHKeys(): Promise<UserSSHKey[]> {
  const res = await client.get("/auth/ssh-keys");
  return res.data;
}

export async function generateSSHKey(name?: string): Promise<GenerateSSHKeyResult> {
  const res = await client.post("/auth/ssh-keys/generate", { name });
  return res.data;
}

export async function uploadSSHKey(name: string, publicKey: string): Promise<{ key: UserSSHKey }> {
  const res = await client.post("/auth/ssh-keys", { name, public_key: publicKey });
  return res.data;
}

export async function deleteSSHKey(id: number): Promise<void> {
  await client.delete(`/auth/ssh-keys/${id}`);
}

export async function getSSHGatewayInfo(): Promise<SSHGatewayInfo> {
  const res = await client.get("/ssh-gateway/info");
  return res.data;
}
