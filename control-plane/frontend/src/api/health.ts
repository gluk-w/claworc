import axios from "axios";

export type OrchestratorReason =
  | "daemon_unreachable"
  | "client_error"
  | "config_missing"
  | "namespace_missing"
  | "invalid_setting"
  | "unavailable"
  | "unknown"
  | "";

export interface BackendAttempt {
  backend: string;
  ok: boolean;
  reason?: OrchestratorReason;
  message?: string;
  at: string;
}

export interface OrchestratorStatus {
  backend: "kubernetes" | "docker" | "none";
  available: boolean;
  last_attempt: string;
  attempts?: BackendAttempt[];
}

export interface HealthResponse {
  status: string;
  orchestrator: "connected" | "disconnected";
  orchestrator_backend: "kubernetes" | "docker" | "none";
  orchestrator_status?: OrchestratorStatus;
  database: string;
  build_date?: string;
}

export async function fetchHealth(): Promise<HealthResponse> {
  const { data } = await axios.get<HealthResponse>("/health");
  return data;
}

export async function reinitializeOrchestrator(): Promise<OrchestratorStatus> {
  const { data } = await axios.post<OrchestratorStatus>(
    "/api/v1/orchestrator/reinitialize",
  );
  return data;
}
