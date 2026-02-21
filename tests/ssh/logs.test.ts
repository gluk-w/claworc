import { describe, it, expect, beforeAll, afterAll } from "vitest";
import { execFileSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  type ContainerInfo,
  agentImage,
  startAgentContainer,
  stopAgentContainer,
  waitForSSH,
} from "./setup";

const __dirname = dirname(fileURLToPath(import.meta.url));
const PROJECT_ROOT = resolve(__dirname, "../..");
const CONTROL_PLANE_DIR = resolve(PROJECT_ROOT, "control-plane");

interface GoTestResult {
  stdout: string;
  stderr: string;
  exitCode: number;
}

/**
 * Runs a Go test function from the sshlogs package with docker_integration tag,
 * passing the external container info via environment variables.
 */
function runGoTest(testPattern: string, info: ContainerInfo): GoTestResult {
  const args = [
    "test",
    "-v",
    "-count=1",
    "-timeout",
    "120s",
    "-tags",
    "docker_integration",
    "-run",
    testPattern,
    "./internal/sshlogs/",
  ];

  try {
    const stdout = execFileSync("go", args, {
      encoding: "utf-8",
      timeout: 150_000,
      cwd: CONTROL_PLANE_DIR,
      env: {
        ...process.env,
        AGENT_CONTAINER_ID: info.containerId,
        AGENT_SSH_HOST: info.sshHost,
        AGENT_SSH_PORT: String(info.sshPort),
      },
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

/** Logs Go test output when a test fails. */
function logFailure(result: GoTestResult): void {
  console.error("--- Go test output ---");
  console.error(result.stdout);
  if (result.stderr) console.error(result.stderr);
  console.error("--- end ---");
}

let container: ContainerInfo;

describe("SSH log streaming integration", () => {
  beforeAll(async () => {
    // Verify the agent image exists
    const image = agentImage();
    try {
      execFileSync("docker", ["inspect", "--type=image", image], {
        stdio: "ignore",
      });
    } catch {
      throw new Error(
        `Agent image "${image}" not found. Build it first:\n` +
          `  docker build -t ${image} ./agent/\n` +
          `Or set AGENT_TEST_IMAGE to an existing image.`,
      );
    }

    container = startAgentContainer("logs");
    console.log(
      `Agent container started: ${container.name} (${container.sshHost}:${container.sshPort})`,
    );

    await waitForSSH(container);
    console.log("SSH daemon ready");
  }, 180_000);

  afterAll(() => {
    if (container) {
      stopAgentContainer(container);
      console.log(`Container ${container.name} removed`);
    }
  });

  it(
    "streams log lines without follow mode",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_StreamLogsNonFollow",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "limits initial lines with tail parameter",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_StreamLogsTailParameter",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "streams log lines in real-time with follow mode",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_StreamLogsFollowRealTime",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "cleans up SSH session on client disconnect",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_StreamLogsClientDisconnect",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "streams multiple log files simultaneously",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_StreamLogsMultipleSimultaneous",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "handles non-existent log file gracefully",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_StreamLogsNonExistentFile",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "does not leak goroutines across multiple stream cycles",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_StreamLogsMemoryStability",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "discovers available log files on the agent",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_GetAvailableLogFiles",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "handles rapid cancel/restart cycles in follow mode",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_StreamLogsFollowWithCancel",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );
});
