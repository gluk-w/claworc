export interface User {
  id: number;
  username: string;
  role: "admin" | "user";
}

export interface LoginRequest {
  username: string;
  password: string;
}

export interface SetupRequest {
  username: string;
  password: string;
}

export interface WebAuthnCredential {
  id: string;
  name: string;
  created_at: string;
}
