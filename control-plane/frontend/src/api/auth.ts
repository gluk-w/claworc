import client from "./client";
import type {
  User,
  LoginRequest,
  SetupRequest,
  WebAuthnCredential,
} from "@/types/auth";

export async function login(data: LoginRequest): Promise<User> {
  const res = await client.post("/auth/login", data);
  return res.data;
}

export async function logout(): Promise<void> {
  await client.post("/auth/logout");
}

export async function getCurrentUser(): Promise<User> {
  const res = await client.get("/auth/me");
  return res.data;
}

export async function checkSetupRequired(): Promise<boolean> {
  const res = await client.get("/auth/setup-required");
  return res.data.setup_required;
}

export async function setupCreateAdmin(data: SetupRequest): Promise<User> {
  const res = await client.post("/auth/setup", data);
  return res.data;
}

// WebAuthn

export async function webAuthnRegisterBegin(): Promise<unknown> {
  const res = await client.post("/auth/webauthn/register/begin");
  return res.data;
}

export async function webAuthnRegisterFinish(
  body: unknown,
  name: string,
): Promise<void> {
  await client.post(`/auth/webauthn/register/finish?name=${encodeURIComponent(name)}`, body);
}

export async function webAuthnLoginBegin(): Promise<unknown> {
  const res = await client.post("/auth/webauthn/login/begin");
  return res.data;
}

export async function webAuthnLoginFinish(body: unknown): Promise<User> {
  const res = await client.post("/auth/webauthn/login/finish", body);
  return res.data;
}

export async function listWebAuthnCredentials(): Promise<WebAuthnCredential[]> {
  const res = await client.get("/auth/webauthn/credentials");
  return res.data;
}

export async function deleteWebAuthnCredential(id: string): Promise<void> {
  await client.delete(`/auth/webauthn/credentials/${encodeURIComponent(id)}`);
}
