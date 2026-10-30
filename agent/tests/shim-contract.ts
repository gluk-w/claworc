/**
 * Contract-level checks shared by every shim image suite (docs/shim.md).
 * Registers a `describe("shim contract", …)` block against one container;
 * the image suites add their agent-specific checks around it.
 *
 * Everything here is read-only, a usage/validation error path, or a mutation
 * that restores its own state (config-set verbatim write, a throwaway skill),
 * so it can run before the image's boot-state assertions without disturbing
 * them.
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, it, expect } from "vitest";
import { exec, execInput, parseJsonl, shim, SHIM_DIR } from "./helpers";
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
  "chat-stream",
  "chat-abort",
  "session-reset",
  "config-get",
  "config-set",
  "configure-llm",
  "restart",
  "skill-install",
  "skill-remove",
];

/** The canonical shared bash library every image ships byte-identical. */
const CANONICAL_SHIMLIB = readFileSync(
  join(dirname(fileURLToPath(import.meta.url)), "../template/shim/lib/shimlib.sh"),
  "utf-8",
);

const SKILL = "vitest-skill";

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

/** Run a shell snippet as root inside the container. */
export function sh(container: string, script: string) {
  return exec(container, ["bash", "-c", script]);
}

/**
 * Pipe a tar of `srcDir` (built inside the container) into skill-install.
 * `tarArgs` lets tests craft hostile archives (e.g. -P for absolute paths).
 */
export function installSkill(container: string, name: string, tarCmd: string) {
  return sh(container, `${tarCmd} | ${SHIM_DIR}/skill-install --name '${name}'`);
}

/**
 * Drive `chat-stream --session <session>` with a scripted stdin: `script` is a
 * bash command list run inside the container whose stdout feeds the verb
 * (e.g. `stream_send t1 hello; sleep 2; echo abort`). `stream_send <turn>
 * <text>` emits a `send` command with the text base64-encoded. When the
 * script finishes, stdin closes (EOF).
 */
export function chatStream(container: string, session: string, script: string, timeoutMs = 120_000) {
  const prelude = `stream_send() { printf 'send %s %s\\n' "$1" "$(printf '%s' "$2" | base64 -w0)"; }`;
  return execInput(
    container,
    ["bash", "-c", `${prelude}\n{ ${script}\n} | ${SHIM_DIR}/chat-stream --session '${session}'`],
    "",
    timeoutMs,
  );
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

    it("every verb entrypoint is a bash script", () => {
      const r = sh(
        container,
        `for f in ${SHIM_DIR}/*; do [ -f "$f" ] && [ -x "$f" ] && printf '%s\t%s\n' "$(basename "$f")" "$(head -n1 "$f")"; done`,
      );
      expect(r.exitCode).toBe(0);
      const entries = r.stdout.trim().split("\n").map((l) => l.split("\t"));
      expect(entries.map(([name]) => name)).toEqual(expect.arrayContaining(CONTRACT_VERBS));
      for (const [name, shebang] of entries) {
        expect(shebang, name).toBe("#!/usr/bin/env bash");
      }
    });

    it("ships the canonical lib/shimlib.sh", () => {
      expect(readFile(container, `${SHIM_DIR}/lib/shimlib.sh`)).toBe(CANONICAL_SHIMLIB);
      expect(mode(container, `${SHIM_DIR}/lib/shimlib.sh`)).toBe("644");
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

    it("config-set stores bytes verbatim, without validating them", () => {
      const meta = readMeta(container);
      for (const f of meta.config_files) {
        const before = shim(container, "config-get", ["--id", f.id]).stdout;
        const raw = `{"vitest": [unclosed\n\tnot: valid: yaml: either\n`;
        try {
          expect(shim(container, "config-set", ["--id", f.id], raw).exitCode, f.id).toBe(0);
          expect(shim(container, "config-get", ["--id", f.id]).stdout, f.id).toBe(raw);
          expect(owner(container, f.path), f.path).toBe("claworc:claworc");
        } finally {
          expect(shim(container, "config-set", ["--id", f.id], before).exitCode).toBe(0);
        }
      }
    });

    it("session verbs require --session (exit 2)", () => {
      expect(shim(container, "chat-send", [], "hi").exitCode).toBe(2);
      expect(shim(container, "chat-stream").exitCode).toBe(2);
      expect(shim(container, "chat-abort").exitCode).toBe(2);
      expect(shim(container, "session-reset").exitCode).toBe(2);
    });

    describe("chat-stream", () => {
      it("declares chat.stream", () => {
        expect(readMeta(container).capabilities).toContain("chat.stream");
      });

      it("prints ready first and exits 0 on stdin EOF", () => {
        const r = chatStream(container, "vitest-stream", "true");
        expect(r.exitCode, r.stderr).toBe(0);
        expect(parseJsonl(r.stdout)).toEqual([{ v: 1, event: "ready" }]);
      });

      it("ignores reset/abort with nothing in flight and unknown commands", () => {
        const r = chatStream(container, "vitest-stream", "echo abort; echo reset; echo bogus; sleep 2");
        expect(r.exitCode, r.stderr).toBe(0);
        const events = parseJsonl(r.stdout);
        expect(events[0]).toEqual({ v: 1, event: "ready" });
        // An unknown session resets cleanly: no reset_failed error.
        expect(events.filter((e) => e.event === "error")).toEqual([]);
      });

      it("abort ends the in-flight turn with stop_reason aborted", { timeout: 150_000 }, () => {
        const r = chatStream(container, "vitest-stream-abort", "stream_send t-abort hello; sleep 2; echo abort; sleep 10");
        expect(r.exitCode, r.stderr).toBe(0);
        const events = parseJsonl(r.stdout);
        expect(events[0].event).toBe("ready");
        const ends = events.filter((e) => e.event === "end");
        expect(ends).toHaveLength(1);
        expect(ends[0].turn).toBe("t-abort");
        expect(ends[0].stop_reason).toBe("aborted");
        expect(events.find((e) => e.event === "start")?.turn).toBe("t-abort");
      });
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

    describe("skills", () => {
      const skillsDir = () => readMeta(container).skills_dir as string;
      const src = "/tmp/vitest-skill-src";
      const build = (extra = "") =>
        sh(
          container,
          `rm -rf ${src} && mkdir -p ${src}/scripts && printf -- '---\\nname: ${SKILL}\\n---\\n' > ${src}/SKILL.md ` +
            `&& printf 'echo ok\\n' > ${src}/scripts/run.sh && chmod 755 ${src}/scripts/run.sh ${extra}`,
        );
      const cleanup = () => shim(container, "skill-remove", ["--name", SKILL]);

      it("declares the skills capability and a skills_dir", () => {
        const meta = readMeta(container);
        expect(meta.capabilities).toContain("skills");
        expect(typeof meta.skills_dir).toBe("string");
      });

      it("skill-install unpacks the archive into skills_dir/<name>, claworc-owned", () => {
        try {
          expect(build().exitCode).toBe(0);
          const r = installSkill(container, SKILL, `tar -C ${src} -cf - .`);
          expect(r.exitCode, r.stdout + r.stderr).toBe(0);
          const dir = `${skillsDir()}/${SKILL}`;
          expect(readFile(container, `${dir}/SKILL.md`)).toBe(readFile(container, `${src}/SKILL.md`));
          expect(readFile(container, `${dir}/scripts/run.sh`)).toBe("echo ok\n");
          for (const p of [skillsDir(), dir, `${dir}/SKILL.md`, `${dir}/scripts`, `${dir}/scripts/run.sh`]) {
            expect(owner(container, p), p).toBe("claworc:claworc");
          }
          expect(mode(container, `${dir}/scripts/run.sh`)).toBe("755");
        } finally {
          cleanup();
        }
      });

      it("skill-install replaces the whole skill on reinstall", () => {
        try {
          build();
          expect(installSkill(container, SKILL, `tar -C ${src} -cf - .`).exitCode).toBe(0);
          sh(container, `rm ${src}/scripts/run.sh`);
          expect(installSkill(container, SKILL, `tar -C ${src} -cf - .`).exitCode).toBe(0);
          const dir = `${skillsDir()}/${SKILL}`;
          expect(exec(container, ["test", "-e", `${dir}/scripts/run.sh`]).exitCode).not.toBe(0);
          expect(exec(container, ["test", "-f", `${dir}/SKILL.md`]).exitCode).toBe(0);
          // No staging leftovers next to the installed skill.
          expect(sh(container, `ls -A ${skillsDir()} | grep -c '^\\.${SKILL}'`).stdout.trim()).toBe("0");
        } finally {
          cleanup();
        }
      });

      it("skill-install rejects links, absolute paths and .. with exit 6, writing nothing", () => {
        const hostile: Record<string, string> = {
          symlink: `ln -sf /etc/passwd ${src}/link && tar -C ${src} -cf - .`,
          hardlink: `ln -f ${src}/SKILL.md ${src}/hard && tar -C ${src} -cf - .`,
          absolute: `tar -cPf - ${src}/SKILL.md`,
          // -P stops GNU tar from sanitizing the crafted name on create.
          dotdot: `tar -C ${src} -P --transform 's,^,sub/../../,' -cf - SKILL.md`,
          garbage: `echo not-a-tar`,
        };
        for (const [kind, tarCmd] of Object.entries(hostile)) {
          build();
          const r = installSkill(container, SKILL, tarCmd);
          expectValidationError(r);
          expect(exec(container, ["test", "-e", `${skillsDir()}/${SKILL}`]).exitCode, kind).not.toBe(0);
        }
        expect(exec(container, ["test", "-e", `${skillsDir()}/../evil`]).exitCode).not.toBe(0);
      });

      it("skill verbs require a safe --name (exit 2)", () => {
        for (const args of [[], ["--name"], ["--name", "../x"], ["--name", "a/b"], ["--name", ".."], ["--bogus", "x"]]) {
          expect(shim(container, "skill-install", args, "").exitCode, args.join(" ")).toBe(2);
          expect(shim(container, "skill-remove", args).exitCode, args.join(" ")).toBe(2);
        }
      });

      it("skill-remove deletes the skill and is idempotent", () => {
        build();
        expect(installSkill(container, SKILL, `tar -C ${src} -cf - .`).exitCode).toBe(0);
        expect(shim(container, "skill-remove", ["--name", SKILL]).exitCode).toBe(0);
        expect(exec(container, ["test", "-e", `${skillsDir()}/${SKILL}`]).exitCode).not.toBe(0);
        expect(shim(container, "skill-remove", ["--name", SKILL]).exitCode).toBe(0);
      });
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
