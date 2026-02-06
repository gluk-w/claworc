import axios from "axios";

interface HealthResponse {
  status: string;
  orchestrator: "connected" | "disconnected";
  orchestrator_backend: "kubernetes" | "docker" | "none";
  database: string;
}

export async function fetchHealth(): Promise<HealthResponse> {
  const { data } = await axios.get<HealthResponse>("/health");
  return data;
}
