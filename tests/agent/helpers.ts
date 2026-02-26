import { execFileSync } from "node:child_process";

export interface ExecResult {
  stdout: string;
  stderr: string;
  exitCode: number;
}

export interface ContainerInfo {
  name: string;
  image: string;
}

export type ContainerMap = Record<string, ContainerInfo>;

export function exec(container: string, cmd: string[]): ExecResult {
  try {
    const stdout = execFileSync("docker", ["exec", container, ...cmd], {
      encoding: "utf-8",
      timeout: 120_000,
    });
    return { stdout, stderr: "", exitCode: 0 };
  } catch (err: any) {
    return {
      stdout: err.stdout ?? "",
      stderr: err.stderr ?? "",
      exitCode: err.status ?? 1,
    };
  }
}

export function execAsUser(container: string, cmd: string): ExecResult {
  return exec(container, ["su", "-", "claworc", "-c", cmd]);
}

export function hostExec(args: string[]): string {
  try {
    return execFileSync(args[0], args.slice(1), {
      encoding: "utf-8",
      timeout: 30_000,
    });
  } catch (err: any) {
    return (err.stdout ?? "") + (err.stderr ?? "") || `(exit ${err.status})`;
  }
}

export function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

export function dumpDiagnostics(container: string): void {
  console.error("=== Container logs ===");
  try {
    console.error(
      execFileSync("docker", ["logs", "--tail", "50", container], {
        encoding: "utf-8",
        timeout: 30_000,
      }),
    );
  } catch (err: any) {
    console.error(err.stderr || err.stdout || "(no logs)");
  }
  console.error("=== Process list ===");
  console.error(exec(container, ["ps", "aux"]).stdout || "(empty)");
}

/**
 * Read the container map written by global-setup.ts via process.env.
 * Returns an empty object when running outside the global setup harness.
 */
export function getContainers(): ContainerMap {
  const raw = process.env.AGENT_TEST_CONTAINERS;
  if (!raw) return {};
  try {
    return JSON.parse(raw) as ContainerMap;
  } catch {
    return {};
  }
}
