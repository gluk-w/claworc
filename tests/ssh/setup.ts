/**
 * Shared Docker container lifecycle management for SSH integration tests.
 *
 * Provides functions to start/stop agent containers and wait for SSH readiness.
 * These helpers are designed to be imported by any test file in tests/ssh/.
 *
 * Usage:
 *   import { startAgentContainer, stopAgentContainer, waitForSSH } from "./setup";
 *
 * For sharing a single container across multiple test files in a single Vitest run,
 * migrate to Vitest's globalSetup mechanism and use provide/inject or a temp file
 * to pass container info between the setup script and test files.
 */

import { execFileSync } from "node:child_process";
import { createConnection } from "node:net";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

/** Default agent Docker image. Override with AGENT_TEST_IMAGE env var. */
const DEFAULT_IMAGE = "claworc-agent:local";

const __setup_dirname = dirname(fileURLToPath(import.meta.url));
const AGENT_DIR = resolve(__setup_dirname, "../../agent");

/** Container metadata returned by startAgentContainer. */
export interface ContainerInfo {
  containerId: string;
  sshHost: string;
  sshPort: number;
  name: string;
}

/** Result of executing a command inside a container. */
export interface ExecResult {
  stdout: string;
  stderr: string;
  exitCode: number;
}

/** Returns the Docker image name for agent tests. */
export function agentImage(): string {
  return process.env.AGENT_TEST_IMAGE ?? DEFAULT_IMAGE;
}

/**
 * Ensures the agent Docker image exists, building it from agent/Dockerfile if needed.
 * Skips the build when AGENT_TEST_IMAGE is set (assumes external image is pre-built).
 *
 * @returns The image name that is guaranteed to exist
 */
export function ensureAgentImage(): string {
  const image = agentImage();

  // Check if image already exists
  try {
    execFileSync("docker", ["inspect", "--type=image", image], {
      stdio: "ignore",
    });
    return image;
  } catch {
    // Image doesn't exist — try to build it
  }

  // Only auto-build the default image (not custom AGENT_TEST_IMAGE)
  if (process.env.AGENT_TEST_IMAGE) {
    throw new Error(
      `Agent image "${image}" not found. Ensure the AGENT_TEST_IMAGE exists.`,
    );
  }

  console.log(`Building agent image "${image}" from ${AGENT_DIR}/Dockerfile...`);
  try {
    execFileSync(
      "docker",
      ["build", "--platform", "linux/amd64", "-t", image, AGENT_DIR],
      {
        encoding: "utf-8",
        stdio: "inherit",
        timeout: 600_000, // 10 minute build timeout
      },
    );
  } catch (err: any) {
    throw new Error(
      `Failed to build agent image "${image}" from ${AGENT_DIR}/Dockerfile.\n` +
        `Build error: ${err.message ?? err}`,
    );
  }

  return image;
}

/**
 * Runs a command inside a Docker container.
 * Returns stdout, stderr, and exit code.
 */
export function execInContainer(
  info: ContainerInfo,
  cmd: string[],
): ExecResult {
  try {
    const stdout = execFileSync(
      "docker",
      ["exec", info.containerId, ...cmd],
      {
        encoding: "utf-8",
        timeout: 30_000,
      },
    );
    return { stdout, stderr: "", exitCode: 0 };
  } catch (err: any) {
    return {
      stdout: err.stdout ?? "",
      stderr: err.stderr ?? "",
      exitCode: err.status ?? 1,
    };
  }
}

/**
 * Starts an agent Docker container with SSH (port 22) published to a random host port.
 * The container name includes the process PID to avoid collisions.
 *
 * @param name - Human-readable test name (used as part of the container name)
 * @param image - Docker image override (defaults to AGENT_TEST_IMAGE or claworc-agent:local)
 * @returns Container metadata with SSH connection info
 */
export function startAgentContainer(
  name: string,
  image?: string,
): ContainerInfo {
  const img = image ?? agentImage();
  const containerName = `ssh-test-${name}-${process.pid}`;

  // Remove stale container from a previous run
  try {
    execFileSync("docker", ["rm", "-f", containerName], { stdio: "ignore" });
  } catch {
    // ignore
  }

  const containerId = execFileSync(
    "docker",
    [
      "run",
      "-d",
      "--privileged",
      "--platform",
      "linux/amd64",
      "-p",
      "127.0.0.1::22",
      "--name",
      containerName,
      img,
    ],
    { encoding: "utf-8" },
  ).trim();

  // Get the mapped SSH port
  const portOutput = execFileSync(
    "docker",
    ["port", containerName, "22/tcp"],
    { encoding: "utf-8" },
  ).trim();

  // Parse "127.0.0.1:12345" or "0.0.0.0:12345" → 12345
  const port = parseInt(portOutput.split(":").pop()!, 10);

  return {
    containerId,
    sshHost: "127.0.0.1",
    sshPort: port,
    name: containerName,
  };
}

/**
 * Stops and removes a Docker container.
 * Ignores errors (container may already be gone).
 */
export function stopAgentContainer(info: ContainerInfo): void {
  try {
    execFileSync("docker", ["rm", "-f", info.containerId], {
      stdio: "ignore",
    });
  } catch {
    // ignore
  }
}

/**
 * Waits for the SSH daemon inside the container to accept connections and
 * return a valid SSH protocol banner (e.g., "SSH-2.0-OpenSSH_9.6").
 * A TCP-only check is insufficient because Docker Desktop port forwarding
 * accepts connections before sshd finishes generating host keys.
 *
 * @param info - Container connection info
 * @param timeoutMs - Maximum time to wait (default: 120 seconds)
 */
export function waitForSSH(
  info: ContainerInfo,
  timeoutMs = 120_000,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const deadline = Date.now() + timeoutMs;

    function attempt() {
      if (Date.now() > deadline) {
        reject(
          new Error(
            `SSH not ready at ${info.sshHost}:${info.sshPort} after ${timeoutMs}ms`,
          ),
        );
        return;
      }

      const socket = createConnection(
        { host: info.sshHost, port: info.sshPort },
        () => {
          socket.once("data", (data: Buffer) => {
            socket.destroy();
            if (data.toString().startsWith("SSH-")) {
              resolve();
            } else {
              setTimeout(attempt, 2_000);
            }
          });
          socket.setTimeout(3_000);
          socket.on("timeout", () => {
            socket.destroy();
            setTimeout(attempt, 2_000);
          });
        },
      );

      socket.on("error", () => {
        socket.destroy();
        setTimeout(attempt, 2_000);
      });
    }

    attempt();
  });
}

/**
 * Starts mock services inside the container for testing tunnel data flow:
 *   - HTTP server on port 3000 (simulates VNC/Selkies UI)
 *   - TCP echo server on port 8080 (simulates Gateway service)
 *
 * Safe to call multiple times; existing services continue running if ports
 * are already occupied.
 */
export function startMockServices(info: ContainerInfo): void {
  // HTTP server on port 3000 (simulates VNC/Selkies web interface)
  execInContainer(info, [
    "sh",
    "-c",
    "nohup python3 -m http.server 3000 --bind 0.0.0.0 >/dev/null 2>&1 &",
  ]);

  // TCP echo server on port 8080 (simulates Gateway service)
  const echoScript = [
    "import socket",
    "s = socket.socket()",
    "s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)",
    's.bind(("0.0.0.0", 8080))',
    "s.listen(5)",
    "while True:",
    "    c, a = s.accept()",
    "    try:",
    "        d = c.recv(4096)",
    "        if d:",
    "            c.sendall(d)",
    "    finally:",
    "        c.close()",
  ].join("\n");

  const b64 = Buffer.from(echoScript).toString("base64");
  execInContainer(info, [
    "sh",
    "-c",
    `echo '${b64}' | base64 -d > /tmp/echo_server.py`,
  ]);
  execInContainer(info, [
    "sh",
    "-c",
    "nohup python3 /tmp/echo_server.py >/dev/null 2>&1 &",
  ]);
}

/** Returns a Promise that resolves after the given number of milliseconds. */
export function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}
