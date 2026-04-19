/**
 * Vitest globalSetup — launches one Docker container per available agent image
 * and tears them all down after every test file has run.
 *
 * The container map is passed to test workers via AGENT_TEST_CONTAINERS env var.
 */
import { execFileSync } from "node:child_process";
import type { ContainerMap } from "./helpers";

const IMAGES: Record<string, string> = {
  chromium: process.env.AGENT_TEST_IMAGE ?? "openclaw-vnc-chromium:test",
  chrome: process.env.AGENT_CHROME_TEST_IMAGE ?? "openclaw-vnc-chrome:test",
  brave: process.env.AGENT_BRAVE_TEST_IMAGE ?? "openclaw-vnc-brave:test",
};

// CI builds multi-arch images and standardises tests on linux/amd64 to match
// how production clusters are provisioned. On dev machines (e.g. Apple
// silicon) you can set AGENT_TEST_PLATFORM=linux/arm64 or leave it empty to
// let Docker auto-select.
const PLATFORM = process.env.AGENT_TEST_PLATFORM ?? "linux/amd64";

const BROWSER_PROCESS_PATTERNS: Record<string, string> = {
  chromium: "chromium",
  chrome: "google-chrome",
  brave: "brave-browser",
};

function imageExists(image: string): boolean {
  try {
    execFileSync("docker", ["image", "inspect", image], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

function exec(container: string, cmd: string[]): { stdout: string; exitCode: number } {
  try {
    const stdout = execFileSync("docker", ["exec", container, ...cmd], {
      encoding: "utf-8",
      timeout: 60_000,
    });
    return { stdout, exitCode: 0 };
  } catch (err: any) {
    return { stdout: err.stdout ?? "", exitCode: err.status ?? 1 };
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

async function waitForProcess(
  container: string,
  pattern: string,
  timeoutMs: number,
): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const result = exec(container, ["pgrep", "-f", pattern]);
    if (result.exitCode === 0 && result.stdout.trim()) return true;
    await sleep(3_000);
  }
  return false;
}

async function waitForCDP(container: string, timeoutMs: number): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const result = exec(container, [
      "curl",
      "-sf",
      "http://127.0.0.1:9222/json/version",
    ]);
    if (result.exitCode === 0 && result.stdout.includes("webSocketDebuggerUrl")) {
      return true;
    }
    await sleep(3_000);
  }
  return false;
}

function dumpDiagnostics(container: string): void {
  console.error(`=== Container ${container} logs ===`);
  try {
    const result = execFileSync("docker", ["logs", "--tail", "80", container], {
      encoding: "utf-8",
      timeout: 30_000,
      stdio: ["pipe", "pipe", "pipe"],
    });
    console.error(result || "(stdout empty)");
  } catch (err: any) {
    const combined = [err.stdout, err.stderr].filter(Boolean).join("\n");
    console.error(combined || "(no output)");
  }
  console.error(`=== Container ${container} state ===`);
  try {
    const state = execFileSync(
      "docker",
      ["inspect", "-f", "{{.State.Status}} exit={{.State.ExitCode}}", container],
      { encoding: "utf-8", timeout: 30_000 },
    );
    console.error(state.trim());
  } catch {
    console.error("(unable to inspect container)");
  }
  console.error("=== Process list ===");
  console.error(exec(container, ["ps", "aux"]).stdout || "(empty)");
}

/**
 * Generate an ed25519 keypair inside the container and install the public key
 * into /root/.ssh/authorized_keys. The private key lives at
 * /root/.ssh/test_key inside the container — env-vars.test.ts SSHes from
 * `docker exec <ct> ssh -i /root/.ssh/test_key root@127.0.0.1`, so there is
 * no need to extract the key to the host.
 */
function provisionRootSSHKey(container: string): void {
  execFileSync("docker", [
    "exec", container, "bash", "-c",
    [
      "set -e",
      "mkdir -p /root/.ssh",
      "chmod 700 /root/.ssh",
      "ssh-keygen -t ed25519 -f /root/.ssh/test_key -N '' -q",
      "cat /root/.ssh/test_key.pub >> /root/.ssh/authorized_keys",
      "chmod 600 /root/.ssh/authorized_keys",
    ].join(" && "),
  ], { encoding: "utf-8" });
}

async function waitForSSHD(container: string, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const result = exec(container, [
      "bash", "-c", "(>/dev/tcp/127.0.0.1/22) 2>/dev/null && echo ok",
    ]);
    if (result.stdout.trim() === "ok") return;
    await sleep(1_000);
  }
  throw new Error(`[global-setup] sshd did not start on ${container} within ${timeoutMs}ms`);
}

// ── Global lifecycle ────────────────────────────────────────────────────

const launched: ContainerMap = {};

export async function setup(): Promise<void> {
  // Detect which images are available locally
  const available = Object.entries(IMAGES).filter(([, img]) => imageExists(img));
  if (available.length === 0) {
    console.warn("[global-setup] No agent images found locally — skipping container launch");
    return;
  }

  // Launch containers
  for (const [browser, image] of available) {
    const name = `agent-test-${browser}-${process.pid}`;
    try {
      execFileSync("docker", ["rm", "-f", name], { stdio: "ignore" });
    } catch {
      // ignore
    }
    const runArgs = [
      "run", "-d", "--privileged",
      ...(PLATFORM ? ["--platform", PLATFORM] : []),
      "-e", "OPENCLAW_GATEWAY_TOKEN=zzzbbb",
      // Test env vars consumed by env-vars.test.ts. Covers plain value,
      // embedded space, and shell-special characters so we can verify
      // the `printf %q` quoting in /etc/profile.d/claworc-env.sh.
      "-e", "TEST_ENV_PLAIN=plain_value",
      "-e", "TEST_ENV_SPACED=has spaces in it",
      "-e", "TEST_ENV_SPECIAL=a!b#c$d",
      "--name", name, image,
    ];
    execFileSync("docker", runArgs, { encoding: "utf-8" });
    launched[browser] = { name, image };
    console.log(`[global-setup] Started ${browser} container: ${name}`);
  }

  // Wait for readiness in parallel
  const readinessPromises = Object.entries(launched).map(async ([browser, { name }]) => {
    const pattern = BROWSER_PROCESS_PATTERNS[browser] ?? browser;

    // All images: wait for browser process.
    // Generous timeouts because multiple containers under QEMU compete for CPU.
    const browserOk = await waitForProcess(name, pattern, 300_000);
    if (!browserOk) {
      dumpDiagnostics(name);
      throw new Error(`[global-setup] ${browser} process did not start within 300s`);
    }

    // All images: wait for CDP port 9222
    const cdpOk = await waitForCDP(name, 180_000);
    if (!cdpOk) {
      dumpDiagnostics(name);
      throw new Error(`[global-setup] CDP port 9222 not ready for ${browser} within 180s`);
    }

    // Note: openclaw gateway readiness is NOT checked here because
    // `openclaw doctor --fix` + `openclaw config set` commands are extremely
    // slow under QEMU emulation (~10+ minutes with concurrent containers).
    // The openclaw.test.ts file handles its own gateway wait in beforeAll.

    // Provision an in-container SSH keypair so env-vars.test.ts can open a
    // real SSH session back to 127.0.0.1. This reproduces the path used by
    // the control plane's SSH tunnel (connects as root) without needing host
    // port mapping. Idempotent: a re-run of setup() would overwrite the key.
    provisionRootSSHKey(name);
    await waitForSSHD(name, 60_000);

    console.log(`[global-setup] ${browser} container ready`);
  });

  await Promise.all(readinessPromises);

  // Publish container map to test workers
  process.env.AGENT_TEST_CONTAINERS = JSON.stringify(launched);
}

export async function teardown(): Promise<void> {
  for (const [browser, { name }] of Object.entries(launched)) {
    try {
      execFileSync("docker", ["rm", "-f", name], { stdio: "ignore" });
      console.log(`[global-setup] Removed ${browser} container: ${name}`);
    } catch {
      // ignore
    }
  }
}
