import * as fs from "node:fs";
import * as path from "node:path";

const stateFile = path.resolve(process.cwd(), ".shared-instance.json");

export interface SharedInstanceState {
  id: number;
  display_name: string;
}

export function readSharedInstance(): SharedInstanceState | null {
  if (!fs.existsSync(stateFile)) return null;
  try {
    return JSON.parse(fs.readFileSync(stateFile, "utf-8"));
  } catch {
    return null;
  }
}

export function writeSharedInstance(state: SharedInstanceState): void {
  fs.writeFileSync(stateFile, JSON.stringify(state, null, 2));
}

export function clearSharedInstance(): void {
  if (fs.existsSync(stateFile)) fs.unlinkSync(stateFile);
}
