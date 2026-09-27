/**
 * Contract-level checks shared by every shim image suite (docs/shim.md).
 * Registers a `describe("shim contract", …)` block against one container;
 * the image suites add their agent-specific checks around it.
 *
 * Everything here is read-only or a usage/validation error path, so it can
 * run before the image's boot-state assertions without disturbing them.
 */
import { describe, it, expect } from "vitest";
import { exec, shim, SHIM_DIR } from "./helpers";
import { CONFIG_LANGUAGES, DISPLAY_NAMES, type ShimRole } from "./fixtures";

export interface ShimContractOptions {
  role: ShimRole;
  /** Expected `meta` document, given the agent version baked into the image. */
  expectedMeta: (version: string) => Record<string, unknown>;
  /** Shell snippet (run as root) printing the baked agent version the way `meta` reads it. */
  versionCommand: string;
  /** Extra files under SHIM_DIR that must be executable (besides the verbs). */
  executableExtras?: string[];
  /** Files under SHIM_DIR that must be plain 0644 (helper libraries, data). */
  readOnlyFiles?: string[];
}

export const CONTRACT_VERBS = [
  "meta",
  "health",
  "chat-send",
  "chat-abort",
  "session-reset",
  "config-get",
  "config-set",
  "configure-llm",
  "restart",
];

export function owner(container: string, path: string): string {
  return exec(container, ["stat", "-c", "%U:%G", path]).stdout.trim();
}

export function mode(container: string, path: string): string {
  return exec(container, ["stat", "-c", "%a", path]).stdout.trim();
}

export function readFile(container: string, path: string): string {
  const r = exec(container, ["cat", path]);
  expect(r.exitCode, `cat ${path}: ${r.stderr}`).toBe(0);
  return r.stdout;
}

export function readMeta(container: string): any {
  const r = shim(container, "meta");
  expect(r.exitCode, r.stderr).toBe(0);
  return JSON.parse(r.stdout);
}

/** Assert `stdout` is exactly one JSON object with a string `error` (exit-6 contract). */
export function expectValidationError(r: { stdout: string; exitCode: number }): void {
  expect(r.exitCode).toBe(6);
  const parsed = JSON.parse(r.stdout.trim());
  expect(typeof parsed.error).toBe("string");
  expect(parsed.error.length).toBeGreaterThan(0);
}

export function shimContractTests(container: string, opts: ShimContractOptions): void {
  describe("shim contract", () => {
    it("agent.txt is the user-facing display name", () => {
      const r = exec(container, ["cat", `${SHIM_DIR}/agent.txt`]);
      expect(r.exitCode).toBe(0);
      // Single non-empty line, matching DisplayName in the control-plane
      // agent-type registry (control-plane/internal/agentshim/registry.go).
      expect(r.stdout.trim().split("\n")).toEqual([DISPLAY_NAMES[opts.role]]);
    });

    it("agent.svg is a non-empty SVG", () => {
      const r = exec(container, ["cat", `${SHIM_DIR}/agent.svg`]);
      expect(r.exitCode).toBe(0);
      expect(r.stdout.trimStart()).toMatch(/^(<\?xml|<svg)/);
    });

    it("all contract verbs are executable (0755)", () => {
      for (const verb of [...CONTRACT_VERBS, "shim-selftest", ...(opts.executableExtras ?? [])]) {
        expect(mode(container, `${SHIM_DIR}/${verb}`), verb).toBe("755");
      }
    });

    it("identity and helper files are plain 0644", () => {
      for (const f of ["agent.txt", "agent.svg", ...(opts.readOnlyFiles ?? [])]) {
        expect(mode(container, `${SHIM_DIR}/${f}`), f).toBe("644");
      }
    });

    it("meta matches the expected contract document", () => {
      const version = exec(container, ["sh", "-c", opts.versionCommand]).stdout.trim();
      expect(version).not.toBe("");
      expect(readMeta(container)).toEqual(opts.expectedMeta(version));
    });

    it("meta declares only contract-legal config languages", () => {
      const meta = readMeta(container);
      for (const f of meta.config_files) {
        expect(CONFIG_LANGUAGES, `${f.id}: ${f.language}`).toContain(f.language);
      }
    });

    it("meta paths exist and are owned by claworc", () => {
      const meta = readMeta(container);
      const paths: string[] = [
        ...meta.config_files.map((f: any) => f.path),
        meta.workspace_dir,
        ...meta.log_files.map((f: any) => f.path),
      ];
      for (const p of paths) {
        expect(exec(container, ["test", "-e", p]).exitCode, `${p} missing`).toBe(0);
        expect(owner(container, p), p).toBe("claworc:claworc");
      }
    });

    it("health reports ok with JSON on stdout", () => {
      const r = shim(container, "health");
      expect(r.exitCode, r.stderr).toBe(0);
      expect(JSON.parse(r.stdout)).toEqual({ status: "ok" });
    });

    it("config-get without --id returns the first declared file", () => {
      const meta = readMeta(container);
      const first = meta.config_files[0];
      const viaDefault = shim(container, "config-get");
      const viaId = shim(container, "config-get", ["--id", first.id]);
      expect(viaDefault.exitCode).toBe(0);
      expect(viaId.exitCode).toBe(0);
      expect(viaDefault.stdout).toBe(readFile(container, first.path));
      expect(viaId.stdout).toBe(viaDefault.stdout);
    });

    it("config-get / config-set reject an unknown --id with exit 2", () => {
      const meta = readMeta(container);
      const before = readFile(container, meta.config_files[0].path);
      expect(shim(container, "config-get", ["--id", "bogus"]).exitCode).toBe(2);
      expect(shim(container, "config-set", ["--id", "bogus"], "garbage").exitCode).toBe(2);
      expect(readFile(container, meta.config_files[0].path)).toBe(before);
    });

    it("session verbs require --session (exit 2)", () => {
      expect(shim(container, "chat-send", [], "hi").exitCode).toBe(2);
      expect(shim(container, "chat-abort").exitCode).toBe(2);
      expect(shim(container, "session-reset").exitCode).toBe(2);
    });

    it("chat-abort and session-reset are idempotent for an unknown session", () => {
      expect(shim(container, "chat-abort", ["--session", "vitest-nope"]).exitCode).toBe(0);
      expect(shim(container, "session-reset", ["--session", "vitest-nope"]).exitCode).toBe(0);
      expect(shim(container, "session-reset", ["--session", "vitest-nope"]).exitCode).toBe(0);
    });

    it("configure-llm rejects bad input without touching config", () => {
      const meta = readMeta(container);
      const before = readFile(container, meta.config_files[0].path);
      expect(shim(container, "configure-llm", ["--bogus"], "{}").exitCode).toBe(2);
      expectValidationError(shim(container, "configure-llm", [], "not json"));
      expectValidationError(shim(container, "configure-llm", [], "[]"));
      expect(readFile(container, meta.config_files[0].path)).toBe(before);
    });
  });
}

/**
 * Run the image's own conformance script. It mutates config (session key
 * `shim-selftest`, virtual key `claworc-vk-selftest`), so suites call this
 * last. The chat check alone may take up to 180 s inside the script.
 */
export function runShimSelftest(container: string): void {
  const r = shim(container, "shim-selftest", [], "", 400_000);
  if (r.exitCode !== 0) console.error(r.stdout, r.stderr);
  expect(r.exitCode).toBe(0);
  expect(r.stdout).toMatch(/shim-selftest: \d+ passed, 0 failed/);
}
