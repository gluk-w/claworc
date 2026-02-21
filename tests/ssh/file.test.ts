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
 * Runs a Go test function from the sshfiles package with docker_integration tag,
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
    "./internal/sshfiles/",
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

describe("SSH file operations integration", () => {
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

    container = startAgentContainer("file-ops");
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
    "lists directories at /root, /tmp, /etc and handles non-existent paths",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_ListDirectory",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "reads text files and handles non-existent files",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_ReadFile",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "writes and reads back files with various content types (text, JSON, unicode, binary, empty)",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_WriteAndReadFile",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "writes and verifies a large file (1MB) with integrity check",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_LargeFile",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "creates directories including nested paths",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_CreateDirectory",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "performs full workflow: create dir → write file → list → read",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_CreateDirThenWriteAndList",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "handles permission-denied errors correctly",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_PermissionDenied",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "overwrites existing files correctly",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_FileOverwrite",
        container,
      );

      if (result.exitCode !== 0) logFailure(result);
      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );
});
