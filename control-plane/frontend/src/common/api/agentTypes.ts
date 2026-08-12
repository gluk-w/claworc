import client from "./client";

export interface AgentTypeInfo {
  /** Agent type identifier stored on instances (e.g. "openclaw"). */
  type: string;
  /** Human-readable agent name (e.g. "OpenClaw"). */
  display_name: string;
  /** Configured default container image for this type ("" when unset). */
  default_image: string;
  /** Whether this agent type serves a web control UI. */
  has_control_ui: boolean;
}

export async function fetchAgentTypes(): Promise<AgentTypeInfo[]> {
  const { data } = await client.get<AgentTypeInfo[]>("/agent-types");
  return data;
}
