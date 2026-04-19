/**
 * Integration tests for env-var propagation inside the agent image.
 *
 * The orchestrator sets env vars via `docker run -e` / K8s `containers[].env`.
 * Those land in PID 1's environ. From there, four different launch paths need
 * to see them — `docker exec`, s6 services, bash login shells, and PAM-
 * authenticated sessions (SSH). Each path has different mechanics, so each
 * gets its own expectation below.
 *
 * The test env vars are injected in global-setup.ts:
 *   TEST_ENV_PLAIN=plain_value
 *   TEST_ENV_SPACED=has spaces in it
 *   TEST_ENV_SPECIAL=a!b#c$d
 */
import { describe, it, expect } from "vitest";
import { exec, execAsUser, getContainers } from "./helpers";

const containers = getContainers();
const entries = Object.entries(containers).map(
  ([browser, info]) => [browser, info.name] as [string, string],
);

if (entries.length === 0) {
  describe.skip("env-vars (no containers available)", () => {
    it.skip("skipped — no agent images found", () => {});
  });
}

// Vars the orchestrator also injects; we assert these still reach every
// launch path so they're not accidentally dropped by the init-setup filter.
const SYSTEM_VARS = ["OPENCLAW_GATEWAY_TOKEN"];

// User-defined vars (from `docker run -e` in global-setup.ts).
const USER_VARS: Record<string, string> = {
  TEST_ENV_PLAIN: "plain_value",
  TEST_ENV_SPACED: "has spaces in it",
  TEST_ENV_SPECIAL: "a!b#c$d",
};

describe.skipIf(entries.length === 0).each(entries)(
  "env-vars: %s",
  (_browser, container) => {
    // ────────────────────────────────────────────────────────────────────
    // Path 1: docker exec — inherits PID 1's environ directly. This is the
    // baseline: if the orchestrator passed the var, it must be here.
    // ────────────────────────────────────────────────────────────────────
    describe("docker exec (PID 1 env)", () => {
      it.each(Object.entries(USER_VARS))(
        "sees user-defined var %s",
        (name, expected) => {
          const result = exec(container, ["printenv", name]);
          expect(result.exitCode).toBe(0);
          expect(result.stdout.trim()).toBe(expected);
        },
      );

      it.each(SYSTEM_VARS)("sees system var %s", (name) => {
        const result = exec(container, ["printenv", name]);
        expect(result.exitCode).toBe(0);
        expect(result.stdout.trim().length).toBeGreaterThan(0);
      });
    });

    // ────────────────────────────────────────────────────────────────────
    // Path 2: /etc/environment — pam_env's source. Read by every
    // PAM-authenticated session (SSH, su, login, cron).
    // ────────────────────────────────────────────────────────────────────
    describe("/etc/environment (pam_env input)", () => {
      it.each(Object.entries(USER_VARS))(
        "contains %s with the expected value",
        (name, expected) => {
          const result = exec(container, ["cat", "/etc/environment"]);
          expect(result.exitCode).toBe(0);
          // init-setup.sh wraps each value in double quotes (so `#`, `$`,
          // and friends round-trip through pam_env) and escapes `\` + `"`.
          // We reverse that to compare plaintext.
          const line = result.stdout
            .split("\n")
            .find((l) => l.startsWith(`${name}=`));
          expect(line, `no line for ${name}`).toBeDefined();
          const match = line!.match(/^[^=]+="(.*)"$/);
          expect(match, `value for ${name} not double-quoted: ${line}`).not.toBeNull();
          const unescaped = match![1]!
            .replace(/\\"/g, '"')
            .replace(/\\\\/g, "\\");
          expect(unescaped).toBe(expected);
        },
      );

      it.each(SYSTEM_VARS)("contains %s", (name) => {
        const result = exec(container, ["cat", "/etc/environment"]);
        expect(result.stdout).toMatch(new RegExp(`^${name}="[^"]+"$`, "m"));
      });

      it.each(["PATH", "HOME", "HOSTNAME", "TERM", "SHLVL"])(
        "does NOT shadow shell-owned %s",
        (name) => {
          const result = exec(container, ["cat", "/etc/environment"]);
          // These are owned by the login shell / PAM / getpw. Writing them
          // here would override correct per-user values (e.g. HOME=/root
          // from PID 1 vs. claworc's /home/claworc).
          expect(result.stdout).not.toMatch(new RegExp(`^${name}=`, "m"));
        },
      );
    });

    // ────────────────────────────────────────────────────────────────────
    // Path 3: /etc/profile.d/claworc-env.sh — sourced by every bash login
    // shell as a belt-and-suspenders for PAM configs that skip pam_env.
    // ────────────────────────────────────────────────────────────────────
    describe("/etc/profile.d/claworc-env.sh (bash login shell)", () => {
      it("exists and is readable", () => {
        const result = exec(container, [
          "test",
          "-r",
          "/etc/profile.d/claworc-env.sh",
        ]);
        expect(result.exitCode).toBe(0);
      });

      it.each(Object.keys(USER_VARS))("contains export for %s", (name) => {
        const result = exec(container, [
          "grep",
          "-c",
          `^export ${name}=`,
          "/etc/profile.d/claworc-env.sh",
        ]);
        expect(result.exitCode).toBe(0);
        expect(Number(result.stdout.trim())).toBe(1);
      });
    });

    // ────────────────────────────────────────────────────────────────────
    // Path 4: bash login shell — simulates the SSH user path without
    // requiring an actual SSH key exchange. sshd on this image spawns
    // /bin/bash as a login shell ("bash -l") for the user, which sources
    // /etc/profile + /etc/profile.d/*.sh.
    // ────────────────────────────────────────────────────────────────────
    describe("bash login shell (SSH-equivalent)", () => {
      it.each(Object.entries(USER_VARS))(
        "exports %s as %j",
        (name, expected) => {
          // `env -i` scrubs the parent env so we prove the value comes from
          // /etc/profile.d/claworc-env.sh, not from docker-exec inheritance.
          const result = exec(container, [
            "env",
            "-i",
            "PATH=/usr/bin:/bin",
            "bash",
            "-l",
            "-c",
            `printf '%s' "$${name}"`,
          ]);
          expect(result.exitCode).toBe(0);
          expect(result.stdout).toBe(expected);
        },
      );

      it.each(SYSTEM_VARS)("exports system var %s", (name) => {
        const result = exec(container, [
          "env",
          "-i",
          "PATH=/usr/bin:/bin",
          "bash",
          "-l",
          "-c",
          `printf '%s' "$${name}"`,
        ]);
        expect(result.exitCode).toBe(0);
        expect(result.stdout.length).toBeGreaterThan(0);
      });
    });

    // ────────────────────────────────────────────────────────────────────
    // Path 5: `su - claworc` — goes through PAM, so it's the closest
    // reproduction of the real SSH path without needing key setup. Uses
    // pam_env to read /etc/environment.
    // ────────────────────────────────────────────────────────────────────
    describe("PAM-authenticated session via su - claworc", () => {
      it.each(Object.entries(USER_VARS))("exposes %s as %j", (name, expected) => {
        const result = execAsUser(container, `printf '%s' "$${name}"`);
        expect(result.exitCode).toBe(0);
        expect(result.stdout).toBe(expected);
      });

      it.each(SYSTEM_VARS)("exposes system var %s", (name) => {
        const result = execAsUser(container, `printf '%s' "$${name}"`);
        expect(result.exitCode).toBe(0);
        expect(result.stdout.length).toBeGreaterThan(0);
      });

      it("still has the correct HOME for claworc (not PID 1's HOME)", () => {
        // Regression guard: if we ever wrote HOME to /etc/environment,
        // pam_env would set HOME=/root (inherited from PID 1) and break
        // every claworc-user process. The exclude list prevents that.
        const result = execAsUser(container, "echo $HOME");
        expect(result.stdout.trim()).toBe("/home/claworc");
      });
    });

    // ────────────────────────────────────────────────────────────────────
    // Path 6: s6 services — run scripts start with `#!/command/with-contenv
    // bash`, which re-exports vars captured by s6-overlay's /init into
    // /run/s6/container_environment/. We verify by checking the openclaw
    // service's live environ via /proc.
    // ────────────────────────────────────────────────────────────────────
    describe("s6 services (with-contenv)", () => {
      it("openclaw process sees user-defined env vars", () => {
        // pgrep for the gateway process and read its environ. `tr` converts
        // nul-separated entries to newlines so grep can match per-line.
        const result = exec(container, [
          "bash",
          "-c",
          `pid=$(pgrep -f 'openclaw gateway' | head -n1); test -n "$pid" && tr '\\0' '\\n' < /proc/$pid/environ`,
        ]);
        expect(result.exitCode).toBe(0);
        expect(result.stdout).toContain("TEST_ENV_PLAIN=plain_value");
        expect(result.stdout).toContain("TEST_ENV_SPACED=has spaces in it");
        expect(result.stdout).toContain("TEST_ENV_SPECIAL=a!b#c$d");
      });
    });

    // ────────────────────────────────────────────────────────────────────
    // Path 7: actual SSH session — the control plane's SSH tunnel connects
    // to the container's sshd as root and runs commands in a login shell.
    // This is the failure mode the user originally reported: `echo $VAR2`
    // over SSH returned empty. We open a real ssh connection from inside
    // the container to 127.0.0.1:22 using the keypair provisioned in
    // global-setup.ts.
    // ────────────────────────────────────────────────────────────────────
    // The dashboard's terminal tab opens an interactive login shell over
    // SSH. That's the path the user originally reported broken. We force a
    // login shell on the remote side with `bash -l -c`, which sources
    // /etc/profile → /etc/profile.d/claworc-env.sh and overrides pam_env's
    // /etc/environment values. This is intentional: pam_env treats `#` in
    // /etc/environment as a comment even inside quotes, so values with `#`
    // only round-trip through the profile.d script — which is precisely
    // what a real login session uses.
    describe("real SSH session (root@127.0.0.1, login shell)", () => {
      function sshLoginShellEcho(varName: string): { stdout: string; exitCode: number } {
        // ssh joins trailing args with spaces into a single remote command
        // string. Build the remote `bash -l -c '…'` invocation as one token
        // so the printf arguments are not split across argv.
        const remote = `bash -l -c 'printf %s "$${varName}"'`;
        return exec(container, [
          "ssh",
          "-i", "/root/.ssh/test_key",
          "-o", "StrictHostKeyChecking=no",
          "-o", "UserKnownHostsFile=/dev/null",
          "-o", "LogLevel=ERROR",
          "-o", "ConnectTimeout=5",
          "root@127.0.0.1",
          remote,
        ]);
      }

      it.each(Object.entries(USER_VARS))(
        "sees %s over SSH as %j",
        (name, expected) => {
          const result = sshLoginShellEcho(name);
          expect(result.exitCode).toBe(0);
          expect(result.stdout).toBe(expected);
        },
      );

      it.each(SYSTEM_VARS)("sees system var %s over SSH", (name) => {
        const result = sshLoginShellEcho(name);
        expect(result.exitCode).toBe(0);
        expect(result.stdout.length).toBeGreaterThan(0);
      });
    });
  },
);
