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

/**
 * Like `exec`, but feeds `input` to the command's stdin (`docker exec -i`) and
 * takes an explicit timeout. Used for shim verbs that read their payload on
 * stdin (chat-send, config-set, configure-llm). Note: execFileSync blocks the
 * event loop, so this timeout — not vitest's testTimeout — is what actually
 * bounds a test; a timed-out `docker exec` does NOT kill the in-container
 * process.
 */
export function execInput(
  container: string,
  cmd: string[],
  input: string,
  timeoutMs = 120_000,
): ExecResult {
  try {
    const stdout = execFileSync("docker", ["exec", "-i", container, ...cmd], {
      encoding: "utf-8",
      input,
      timeout: timeoutMs,
      maxBuffer: 16 * 1024 * 1024,
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

export const SHIM_DIR = "/opt/claworc/shim";

/**
 * Invoke a Claworc agent-shim verb (docs/shim.md) as root, the way the
 * control plane does over SSH. `input` is piped to stdin when given.
 */
export function shim(
  container: string,
  verb: string,
  args: string[] = [],
  input?: string,
  timeoutMs = 120_000,
): ExecResult {
  return execInput(container, [`${SHIM_DIR}/${verb}`, ...args], input ?? "", timeoutMs);
}

/**
 * Parse shim chat-send output: one JSON object per line. Ignores a trailing
 * empty line; throws naming the offending line otherwise.
 */
export function parseJsonl(stdout: string): any[] {
  const lines = stdout.split("\n");
  if (lines.length && lines[lines.length - 1] === "") lines.pop();
  return lines.map((line, i) => {
    try {
      return JSON.parse(line);
    } catch {
      throw new Error(`JSONL line ${i + 1} is not valid JSON: ${line}`);
    }
  });
}

/** Poll `predicate` until it returns true or `timeoutMs` elapses. */
export async function waitFor(
  predicate: () => boolean,
  timeoutMs: number,
  intervalMs = 2_000,
): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (predicate()) return true;
    await sleep(intervalMs);
  }
  return predicate();
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
  // s6 images log almost nothing to `docker logs`; the service states and the
  // agent log are where boot failures actually show up.
  console.error("=== s6 services ===");
  console.error(
    exec(container, [
      "sh",
      "-c",
      "for s in /run/service/*; do printf '%s: ' \"$s\"; /command/s6-svstat \"$s\" 2>&1; done",
    ]).stdout || "(no /run/service)",
  );
  console.error("=== /var/log/claworc/agent.log (tail) ===");
  console.error(
    exec(container, ["sh", "-c", "tail -n 50 /var/log/claworc/agent.log 2>&1"]).stdout ||
      "(empty)",
  );
}

/**
 * Returns true if `cmd` is on PATH inside `container`. Used by capability-
 * gated test suites: openclaw / cron live in the claworc-agent image, while
 * the browser images only ship Xvfb / VNC / chromium. Suites that require
 * one of those features should skip when probing returns false.
 */
export function hasCommand(container: string, cmd: string): boolean {
  return exec(container, ["sh", "-c", `command -v ${cmd}`]).exitCode === 0;
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
