export interface UserTeamMembership {
  id: number;
  name: string;
  role: "user" | "manager";
}

export interface User {
  id: number;
  username: string;
  email?: string;
  role: "admin" | "user";
  teams?: UserTeamMembership[];
}

export interface AuthConfig {
  cf_access_enabled: boolean;
  logout_url?: string;
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
