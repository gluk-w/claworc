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
  startMockServices,
  sleep,
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
 * Runs a Go test function from the sshproxy package with docker_integration tag,
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
    "./internal/sshproxy/",
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

let container: ContainerInfo;

describe("SSH tunnel proxy integration", () => {
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

    container = startAgentContainer("proxy");
    console.log(
      `Agent container started: ${container.name} (${container.sshHost}:${container.sshPort})`,
    );

    await waitForSSH(container);
    console.log("SSH daemon ready");

    startMockServices(container);
    await sleep(2_000); // Allow mock services to bind
    console.log("Mock services started (HTTP:3000, TCP echo:8080)");
  }, 180_000);

  afterAll(() => {
    if (container) {
      stopAgentContainer(container);
      console.log(`Container ${container.name} removed`);
    }
  });

  it(
    "establishes SSH tunnels and proxies HTTP/TCP traffic end-to-end",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_TunnelProxyEndToEnd",
        container,
      );

      if (result.exitCode !== 0) {
        console.error("--- Go test output ---");
        console.error(result.stdout);
        if (result.stderr) console.error(result.stderr);
        console.error("--- end ---");
      }

      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "verifies tunnel reuse across repeated StartTunnelsForInstance calls",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_TunnelReuse",
        container,
      );

      if (result.exitCode !== 0) {
        console.error("--- Go test output ---");
        console.error(result.stdout);
        if (result.stderr) console.error(result.stderr);
        console.error("--- end ---");
      }

      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );

  it(
    "handles concurrent HTTP and TCP requests through tunnels",
    () => {
      const result = runGoTest(
        "TestExternalIntegration_ConcurrentTunnelProxy",
        container,
      );

      if (result.exitCode !== 0) {
        console.error("--- Go test output ---");
        console.error(result.stdout);
        if (result.stderr) console.error(result.stderr);
        console.error("--- end ---");
      }

      expect(result.exitCode).toBe(0);
      expect(result.stdout).toContain("PASS");
    },
    120_000,
  );
});
