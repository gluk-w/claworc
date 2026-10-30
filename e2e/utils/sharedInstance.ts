import * as fs from "node:fs";
import * as path from "node:path";

// globalSetup creates one long-lived instance per agent type and records them
// here so every spec in that type's project can reuse it.
const stateFile = path.resolve(__dirname, "..", ".shared-instances.json");

export interface SharedInstanceState {
  id: number;
  display_name: string;
  agent_type: string;
  // Set when globalSetup could wire an LLM provider/model onto the instance;
  // chat and webhook specs skip without it.
  llm_model?: string;
}

type State = Record<string, SharedInstanceState>;

function readAll(): State {
  if (!fs.existsSync(stateFile)) return {};
  try {
    return JSON.parse(fs.readFileSync(stateFile, "utf-8"));
  } catch {
    return {};
  }
}

export function readSharedInstance(agentType = "openclaw"): SharedInstanceState | null {
  return readAll()[agentType] ?? null;
}

export function readAllSharedInstances(): SharedInstanceState[] {
  return Object.values(readAll());
}

export function writeSharedInstances(state: State): void {
  fs.writeFileSync(stateFile, JSON.stringify(state, null, 2));
}

export function clearSharedInstance(): void {
  if (fs.existsSync(stateFile)) fs.unlinkSync(stateFile);
}
