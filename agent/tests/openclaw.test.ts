import { describe, it, expect, beforeAll } from "vitest";
import { readFileSync } from "node:fs";
import { exec, execAsUser, sleep, getContainers, dumpDiagnostics, parseJsonl, shim, SHIM_DIR } from "./helpers";
import { chatStream, CONTRACT_VERBS, expectValidationError, installSkill, mode, owner, readFile, sh } from "./shim-contract";

const containers = getContainers();
// openclaw lives in the claworc-agent image only. Browser-only images
// (claworc-browser-*) don't ship the gateway, so this suite runs against
// the dedicated `agent` container launched by global-setup.ts when the
// instance image is available locally.
const container = containers.agent?.name;

function structureOf(obj: any): any {
  if (Array.isArray(obj)) return obj.length > 0 ? [structureOf(obj[0])] : [];
  if (obj !== null && typeof obj === "object") {
    return Object.fromEntries(
      Object.keys(obj).sort().map((k) => [k, structureOf(obj[k])]),
    );
  }
  return typeof obj;
}

describe.skipIf(!container)("agent image", { timeout: 300_000 }, () => {
  // Wait for openclaw gateway to be ready.
  // The svc-agent run script executes `openclaw doctor --fix` followed by
  // several `openclaw config set` commands before starting the gateway — each
  // spawns Node.js under QEMU emulation, which is very slow with concurrent
  // containers. By the time browser.test.ts finishes, the gateway is usually ready.
  // Wait for openclaw gateway to be ready.
  // Under QEMU with multiple concurrent containers, `openclaw doctor --fix` +
  // several `openclaw config set` commands can take 15+ minutes. The gateway
  // only starts after all of those complete.
  beforeAll(async () => {
    const deadline = Date.now() + 900_000;
    while (Date.now() < deadline) {
      const result = exec(container!, ["pgrep", "-f", "openclaw gateway"]);
      if (result.exitCode === 0 && result.stdout.trim()) break;
      await sleep(5_000);
    }

    // Final check
    const check = exec(container!, ["pgrep", "-f", "openclaw gateway"]);
    if (check.exitCode !== 0) {
      dumpDiagnostics(container!);
      throw new Error("openclaw gateway did not start within 900s");
    }

    // Wait for gateway WebSocket to be ready (port 18789 listening).
    // Port 18789 = 0x4965 in hex.
    const portDeadline = Date.now() + 60_000;
    while (Date.now() < portDeadline) {
      const result = exec(container!, ["grep", "-q", ":4965", "/proc/net/tcp6"]);
      if (result.exitCode === 0) break;
      await sleep(2_000);
    }
  }, 960_000);

  it("openclaw home directory exists and is owned by claworc", () => {
    const result = exec(container!, ["stat", "-c", "%U:%G", "/home/claworc/.openclaw"]);
    expect(result.exitCode).toBe(0);
    expect(result.stdout.trim()).toBe("claworc:claworc");
  });

  // chrome-data must be created by the desktop service (only when Chrome runs),
  // not by init-setup.sh. Otherwise on-demand-layout agents — where Chrome
  // lives in a separate browser pod — would still get a stale chrome-data/
  // visible in the file manager. init-setup.sh may legitimately *remove*
  // the dir on agent images (no svc-desktop), so this test only forbids
  // mkdir-style creation.
  it("init-setup.sh does not create chrome-data", () => {
    const result = exec(container!, [
      "grep",
      "-E",
      "mkdir.*chrome-data",
      "/etc/s6-overlay/scripts/init-setup.sh",
    ]);
    expect(result.exitCode).not.toBe(0);
  });

  // Conversely, on the agent image the init script must remove any leftover
  // chrome-data dir from a prior legacy boot so it isn't reachable from the
  // agent SSH/terminal/file-manager.
  it("init-setup.sh removes chrome-data when svc-desktop is absent", () => {
    const result = exec(container!, [
      "grep",
      "-E",
      "rm -rf /home/claworc/chrome-data",
      "/etc/s6-overlay/scripts/init-setup.sh",
    ]);
    expect(result.exitCode).toBe(0);
  });

  it("openclaw.json structure matches snapshot", () => {
    const result = exec(container!, [
      "cat",
      "/home/claworc/.openclaw/openclaw.json",
    ]);
    expect(result.exitCode).toBe(0);

    const config = JSON.parse(result.stdout);
    // Strip upstream-owned plugin/skill/hook catalogs entirely. They're not
    // schema we depend on, they change weekly as openclaw adds/removes
    // built-ins (e.g. 2026.7.x added `hooks.internal.entries.session-memory`),
    // and they're shaped completely differently between
    // `openclaw@latest` (uses `skills.entries.*`) and `openclaw@stable`
    // (uses `plugins.entries.*`) — so a single snapshot can't track both.
    // The stable parts of the config (gateway, browser, agents, meta, ...)
    // are what our integration actually cares about.
    delete config.skills;
    delete config.plugins;
    delete config.hooks;
    // `meta.migrations` is upstream's own one-shot migration bookkeeping. It
    // gains a new boolean on essentially every openclaw release (e.g.
    // `utilityModelSeparation` exists in 2026.9.5 but not in 2026.9.4), so it
    // diverges between the `latest` and `stable` images that both assert
    // against this one snapshot. Not schema we depend on either way.
    delete config.meta?.migrations;
    expect(structureOf(config)).toMatchSnapshot();
  });

  // The gateway run script starts with `#!/command/with-contenv bash`, so
  // env vars passed by the orchestrator must be visible in its live environ
  // (env-vars.test.ts covers the with-contenv mechanism itself; this pins
  // the actual service). `[o]penclaw` defeats pgrep's self-match against
  // the `bash -c` command line; `-o` picks the oldest match (the gateway).
  it("openclaw gateway sees orchestrator env vars", () => {
    const result = exec(container!, [
      "bash",
      "-c",
      `pid=$(pgrep -o -f '[o]penclaw gateway') && test -n "$pid" && tr '\\0' '\\n' < /proc/$pid/environ`,
    ]);
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain("TEST_ENV_PLAIN=plain_value");
    expect(result.stdout).toContain("TEST_ENV_SPACED=has spaces in it");
    expect(result.stdout).toContain("TEST_ENV_SPECIAL=a!b#c$d");
    expect(result.stdout).toContain("OPENCLAW_GATEWAY_TOKEN=zzzbbb");
  });

  it("openclaw logs exits without crash", () => {
    const result = execAsUser(container!, "openclaw logs --plain --limit 5");
    expect(result.exitCode).toBeDefined();
  });

  it("can set gateway auth token via config", () => {
    const result = execAsUser(
      container!,
      "openclaw config set gateway.auth.token test-token-abc123",
    );
    expect(result.exitCode).toBe(0);

    const configResult = exec(container!, [
      "cat",
      "/home/claworc/.openclaw/openclaw.json",
    ]);
    const config = JSON.parse(configResult.stdout);
    expect(config.gateway.auth.token).toBe("test-token-abc123");
  });

  it("can set agents.defaults.model via --json", () => {
    const modelJson = JSON.stringify({
      primary: "anthropic/claude-sonnet-5",
      fallbacks: ["anthropic/claude-haiku-4-5-20251001"],
    });

    const result = execAsUser(
      container!,
      `openclaw config set agents.defaults.model '${modelJson}' --json`,
    );
    expect(result.exitCode).toBe(0);

    const configResult = exec(container!, [
      "cat",
      "/home/claworc/.openclaw/openclaw.json",
    ]);
    const config = JSON.parse(configResult.stdout);
    expect(config.agents.defaults.model).toEqual({
      primary: "anthropic/claude-sonnet-5",
      fallbacks: ["anthropic/claude-haiku-4-5-20251001"],
    });
  });

  // The claworc agent shim (docs/shim.md). The generic contract battery in
  // shim-contract.ts is not reused wholesale: its config-set check writes
  // unparseable bytes, which the running gateway would hot-reload.
  describe("shim", () => {
    const CONFIG = "/home/claworc/.openclaw/openclaw.json";
    const readConfig = () => JSON.parse(readFile(container!, CONFIG));

    it("every verb entrypoint is a bash script backed by the canonical shimlib", () => {
      for (const verb of [...CONTRACT_VERBS, "control-ui-auth"]) {
        expect(readFile(container!, `${SHIM_DIR}/${verb}`).split("\n")[0], verb).toBe("#!/usr/bin/env bash");
        expect(mode(container!, `${SHIM_DIR}/${verb}`), verb).toBe("755");
      }
      const canonical = readFileSync(new URL("../template/shim/lib/shimlib.sh", import.meta.url), "utf-8");
      expect(readFile(container!, `${SHIM_DIR}/lib/shimlib.sh`)).toBe(canonical);
    });

    it("config-set round-trips openclaw.json byte-for-byte, claworc-owned 0600", () => {
      const before = shim(container!, "config-get").stdout;
      const next = JSON.stringify({ ...JSON.parse(before) }, null, 2) + "\n";
      try {
        expect(shim(container!, "config-set", [], next).exitCode).toBe(0);
        expect(shim(container!, "config-get").stdout).toBe(next);
        expect(owner(container!, CONFIG)).toBe("claworc:claworc");
        expect(mode(container!, CONFIG)).toBe("600");
      } finally {
        expect(shim(container!, "config-set", [], before).exitCode).toBe(0);
      }
      expect(shim(container!, "config-set", ["--id", "bogus"], before).exitCode).toBe(2);
    });

    it("meta declares chat.stream and the gateway-served control UI", () => {
      const meta = JSON.parse(shim(container!, "meta").stdout);
      expect(meta.capabilities).toEqual(expect.arrayContaining(["chat.stream", "control-ui"]));
      expect(meta.control_ui.port).toBe(18789);
      const id = exec(container!, ["cat", "/run/s6/container_environment/CLAWORC_INSTANCE_ID"]).stdout.trim();
      expect(meta.control_ui.base_path).toBe(id ? `/openclaw/${id}/` : "/openclaw/");
    });

    it("control-ui-auth prints the gateway token and loopback Origin", () => {
      const r = shim(container!, "control-ui-auth");
      expect(r.exitCode, r.stderr).toBe(0);
      const doc = JSON.parse(r.stdout);
      expect(doc.headers).toEqual({ Origin: "http://localhost:18789" });
      const token = readConfig().gateway?.auth?.token;
      if (token) expect(doc.query).toEqual({ token });
      else expect(doc.query).toBeUndefined();
      expect(shim(container!, "control-ui-auth", ["--bogus"]).exitCode).toBe(2);
    });

    describe("chat-stream", () => {
      it("prints ready after the gateway handshake and exits 0 on EOF", () => {
        const r = chatStream(container!, "vitest-stream", "true");
        expect(r.exitCode, r.stderr).toBe(0);
        expect(parseJsonl(r.stdout)).toEqual([{ v: 1, event: "ready" }]);
      });

      it("serializes two turns over one connection, each ended exactly once", { timeout: 200_000 }, () => {
        const r = chatStream(
          container!,
          "vitest-stream-2",
          "stream_send t-1 'say one'; stream_send t-2 'say two'; sleep 90",
          180_000,
        );
        expect(r.exitCode, r.stderr).toBe(0);
        const events = parseJsonl(r.stdout);
        expect(events[0]).toEqual({ v: 1, event: "ready" });
        const own = events.filter((e) => e.turn === "t-1" || e.turn === "t-2");
        const order = own.filter((e) => e.event === "start" || e.event === "end").map((e) => `${e.event}:${e.turn}`);
        // t-2 starts only after t-1 ended (or both were cut by EOF: t-1 aborted, t-2 never sent).
        expect(order.slice(0, 2)).toEqual(["start:t-1", "end:t-1"]);
        if (order.length > 2) expect(order).toEqual(["start:t-1", "end:t-1", "start:t-2", "end:t-2"]);
        // Nothing from other sessions leaks in: every turn is ours or unsolicited (u-*).
        for (const e of events.slice(1)) expect(String(e.turn)).toMatch(/^(t-1|t-2|u-.+)$/);
      });

      it("abort ends the in-flight turn with stop_reason aborted", { timeout: 300_000 }, () => {
        // Route the LLM to a local server that accepts and never answers, so
        // the turn is still in flight when abort arrives (with no LLM
        // configured it would fail before the abort).
        exec(container!, ["bash", "-c", `node -e 'require("net").createServer(() => {}).listen(40009, "127.0.0.1")' >/dev/null 2>&1 &`]);
        const routing = {
          proxy_url: "http://127.0.0.1:40009",
          style: "openai",
          default_model: "anthropic/claude-sonnet-4-5",
          fallback_models: [],
          providers: [{ key: "anthropic", api_key: "claworc-vk-x", api_type: "anthropic-messages", models: [{ id: "anthropic/claude-sonnet-4-5" }] }],
        };
        const cfg = shim(container!, "configure-llm", [], JSON.stringify(routing), 240_000);
        expect(cfg.exitCode, cfg.stdout + cfg.stderr).toBe(0);
        // configure-llm restarts the gateway; wait until it accepts connections again.
        exec(container!, ["bash", "-c", "for i in $(seq 1 60); do (exec 3<>/dev/tcp/127.0.0.1/18789) 2>/dev/null && exit 0; sleep 1; done; exit 1"]);

        const r = chatStream(container!, "vitest-stream-abort", "stream_send t-a hello; sleep 3; echo abort; sleep 4");
        expect(r.exitCode, r.stderr).toBe(0);
        const ends = parseJsonl(r.stdout).filter((e) => e.event === "end");
        expect(ends).toEqual([expect.objectContaining({ turn: "t-a", stop_reason: "aborted" })]);
      });

      it("persists one claworc-owned device identity across connections", () => {
        const file = "/home/claworc/.claworc/shim/gateway-device.json";
        const first = exec(container!, ["cat", file]);
        expect(first.exitCode, first.stderr).toBe(0);
        const device = JSON.parse(first.stdout);
        expect(device.deviceId).toMatch(/^[0-9a-f]{64}$/);
        expect(exec(container!, ["stat", "-c", "%U %a", file]).stdout.trim()).toBe("claworc 600");
        // A fresh connection reuses the paired identity rather than minting a new one.
        expect(chatStream(container!, "vitest-stream-device", "true").exitCode).toBe(0);
        expect(JSON.parse(exec(container!, ["cat", file]).stdout).deviceId).toBe(device.deviceId);
      });
    });

    describe("configure-llm", () => {
      const doc = {
        proxy_url: "http://127.0.0.1:40001",
        style: "openai",
        default_model: "anthropic/claude-sonnet-4-5",
        fallback_models: ["openai/gpt-5"],
        providers: [
          {
            key: "anthropic",
            api_key: "claworc-vk-anthropic",
            api_type: "anthropic-messages",
            models: [{ id: "anthropic/claude-sonnet-4-5" }],
          },
          {
            key: "openai",
            api_key: "claworc-vk-openai",
            api_type: "openai-codex-responses",
            models: [{ id: "openai/gpt-5" }],
          },
        ],
      };

      it("writes providers, the default model and the allowlist", { timeout: 300_000 }, () => {
        const r = shim(container!, "configure-llm", [], JSON.stringify(doc), 240_000);
        expect(r.exitCode, r.stdout + r.stderr).toBe(0);
        const cfg = readConfig();
        expect(cfg.models.providers.anthropic).toMatchObject({
          baseUrl: doc.proxy_url,
          api: "anthropic-messages",
          apiKey: "claworc-vk-anthropic",
          models: [{ id: "claude-sonnet-4-5", name: "claude-sonnet-4-5" }],
        });
        // Codex providers are declared as openai-responses to OpenClaw.
        expect(cfg.models.providers.openai.api).toBe("openai-responses");
        expect(cfg.agents.defaults.model).toEqual({
          primary: "anthropic/claude-sonnet-4-5",
          fallbacks: ["openai/gpt-5"],
        });
        expect(Object.keys(cfg.agents.defaults.models).sort()).toEqual([
          "anthropic/claude-sonnet-4-5",
          "openai/gpt-5",
        ]);
        expect(owner(container!, CONFIG)).toBe("claworc:claworc");
      });

      it("is idempotent: a repeat run leaves openclaw.json byte-identical", { timeout: 300_000 }, () => {
        expect(shim(container!, "configure-llm", [], JSON.stringify(doc), 240_000).exitCode).toBe(0);
        const first = readFile(container!, CONFIG);
        expect(shim(container!, "configure-llm", [], JSON.stringify(doc), 240_000).exitCode).toBe(0);
        expect(readFile(container!, CONFIG)).toBe(first);
      });

      it("rejects bad input with exit 2/6 and leaves config untouched", () => {
        const before = readFile(container!, CONFIG);
        expect(shim(container!, "configure-llm", ["--bogus"], "{}").exitCode).toBe(2);
        expectValidationError(shim(container!, "configure-llm", [], "not json"));
        expectValidationError(shim(container!, "configure-llm", [], JSON.stringify({ ...doc, style: "anthropic" })));
        expect(readFile(container!, CONFIG)).toBe(before);
      });
    });

    it("skill-install / skill-remove manage ~/.openclaw/skills/<name>", () => {
      const src = "/tmp/vitest-openclaw-skill";
      const dir = "/home/claworc/.openclaw/skills/vitest-skill";
      try {
        expect(sh(container!, `rm -rf ${src} && mkdir -p ${src} && echo hi > ${src}/SKILL.md`).exitCode).toBe(0);
        const r = installSkill(container!, "vitest-skill", `tar -C ${src} -cf - .`);
        expect(r.exitCode, r.stdout + r.stderr).toBe(0);
        expect(readFile(container!, `${dir}/SKILL.md`)).toBe("hi\n");
        expect(owner(container!, dir)).toBe("claworc:claworc");
        expectValidationError(
          installSkill(container!, "vitest-skill", `ln -sf /etc/passwd ${src}/l && tar -C ${src} -cf - .`),
        );
        expect(readFile(container!, `${dir}/SKILL.md`)).toBe("hi\n");
      } finally {
        expect(shim(container!, "skill-remove", ["--name", "vitest-skill"]).exitCode).toBe(0);
      }
      expect(exec(container!, ["test", "-e", dir]).exitCode).not.toBe(0);
      expect(shim(container!, "skill-remove", ["--name", "vitest-skill"]).exitCode).toBe(0);
    });
  });

  it("openclaw status shows gateway is running", () => {
    const result = execAsUser(container!, "openclaw status");
    expect(result.exitCode).toBe(0);
    expect(result.stdout).toContain("ws://127.0.0.1:18789");
  });

  it("openclaw gateway stop exits without crash", () => {
    const result = execAsUser(container!, "openclaw gateway stop");
    expect(result.exitCode).toBeDefined();
  });

  // Regression for https://github.com/gluk-w/claworc/issues/127. sharp is a
  // native addon (libvips) used by openclaw's image pipeline (Telegram,
  // screenshots). Upstream openclaw lazy-imports sharp but no longer
  // declares it in package.json, so the Dockerfile installs it explicitly
  // (see `npm install --no-save sharp` in agent/openclaw/Dockerfile, plus
  // the libvips42 apt package).
  describe("sharp image dependency (issue #127)", () => {
    const cdOpenclaw = 'cd "$(npm root -g)/openclaw"';

    it("openclaw can load sharp for image processing", () => {
      // Resolve sharp the same way openclaw does at runtime — from its own
      // node_modules — so this fails loudly if libvips is missing or the
      // native binding didn't get built.
      const result = exec(container!, [
        "bash",
        "-c",
        `${cdOpenclaw} && node -e "console.log(require('sharp').versions.sharp)"`,
      ]);
      expect(result.exitCode).toBe(0);
      expect(result.stdout.trim()).toMatch(/^\d+\.\d+\.\d+/);
    });

    it("openclaw runtime still references sharp", () => {
      // If upstream stops importing sharp, the Dockerfile's explicit
      // `npm install --no-save sharp` (and the libvips42 apt package) become
      // dead weight — surface that so we can drop them. We grep the bundled
      // dist for any `sharp` reference (string literal in dynamic import,
      // static import, etc).
      const result = exec(container!, [
        "bash",
        "-c",
        `${cdOpenclaw} && grep -rEq "['\\"]sharp['\\"]" dist`,
      ]);
      expect(result.exitCode).toBe(0);
    });
  });
});
