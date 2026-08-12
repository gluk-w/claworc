# Claworc NanoClaw agent image (claworc/nanoclaw).
#
# NanoClaw (https://github.com/nanocoai/nanoclaw) is a lightweight
# OpenClaw alternative built on the Claude Agent SDK. Upstream, a Node host
# process routes channel messages and spawns one Docker container per agent
# session; host and session runner communicate exclusively through a
# per-session SQLite pair (inbound.db / outbound.db).
#
# The Claworc instance container IS the session sandbox — there is no
# Docker-in-Docker here. This image therefore skips the upstream host
# entirely (channels, OneCLI credential gateway, container spawning are all
# host concerns Claworc replaces) and runs the *agent-runner* directly as a
# child process, supervised by /opt/claworc/shim/lib/host.mjs (svc-agent).
# All message IO stays on NanoClaw's documented host<->runner session-DB
# contract; a small build-time patch (patches/0001-session-dir-env.patch)
# makes the runner's fixed /workspace session mount point overridable via
# NANOCLAW_SESSION_DIR so multiple sessions can run as plain processes.
#
# Layout mirrors upstream's agent container where it matters:
#   /opt/nanoclaw                       pinned NanoClaw checkout (patched)
#   /app/src -> /opt/nanoclaw/container/agent-runner/src   (runner source)
#   /app/node_modules                   bun install of the runner deps
#   /pnpm/claude                        pinned @anthropic-ai/claude-code
#                                       (the SDK hardcodes this path)
#   /workspace/agent -> /home/claworc/workspace            (agent group dir)
#
# No VNC/browser: browsers live in the on-demand claworc/<browser>-browser
# pods, as with the other slim agent images.

FROM debian:bookworm-slim

ARG S6_OVERLAY_VERSION=3.2.0.2
ARG TARGETARCH
# NanoClaw release tag (github.com/nanocoai/nanoclaw). Bump deliberately and
# regenerate patches/ against the new tag.
ARG NANOCLAW_VERSION=v2.1.54
# Runtime pins matching upstream's container/Dockerfile + cli-tools.json for
# this NanoClaw version.
ARG BUN_VERSION=1.3.12
ARG CLAUDE_CODE_VERSION=2.1.197
ARG PNPM_VERSION=10.33.0

# Create claworc user (UID 1000) — all agent state stays owned by it.
RUN useradd -m -u 1000 -s /bin/bash claworc && mkdir -p /defaults

# System packages + Node.js 22 (the Claude Code CLI and its ecosystem need
# node; the agent-runner itself runs under Bun). python3 + jq serve the shim
# selftest and general agent tooling.
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      curl ca-certificates xz-utils gnupg && \
    curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      tzdata locales \
      openssh-server git patch unzip zip \
      python3 jq procps nodejs \
      sudo && \
    echo "en_US.UTF-8 UTF-8" >> /etc/locale.gen && locale-gen && \
    rm -rf /var/lib/apt/lists/*

RUN echo 'claworc ALL=(ALL) NOPASSWD: /usr/bin/apt-get, /usr/bin/apt' > /etc/sudoers.d/claworc-apt && \
    chmod 0440 /etc/sudoers.d/claworc-apt

# s6-overlay (PID 1)
RUN S6_ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "aarch64" || echo "x86_64") && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-noarch.tar.xz" \
      | tar -C / -Jxpf - && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-${S6_ARCH}.tar.xz" \
      | tar -C / -Jxpf -

# Bun — the agent-runner's runtime AND the shim's scripting runtime
# (bun:sqlite is the same SQLite driver the runner uses, so the shim bridge
# adds zero dependencies of its own).
RUN curl -fsSL https://bun.sh/install | bash -s "bun-v${BUN_VERSION}" && \
    install -m 0755 /root/.bun/bin/bun /usr/local/bin/bun && \
    rm -rf /root/.bun

# pnpm + the Claude Code CLI at upstream's pinned version. The Claude Agent
# SDK inside NanoClaw hardcodes pathToClaudeCodeExecutable: '/pnpm/claude'
# (container/agent-runner/src/providers/claude.ts), so we reproduce
# upstream's PNPM_HOME=/pnpm global-bin layout exactly. The
# only-built-dependencies opt-in lets claude-code's postinstall download its
# native binary (pnpm skips build scripts by default).
ENV PNPM_HOME="/pnpm"
ENV PATH="$PNPM_HOME:$PATH"
RUN corepack enable && corepack prepare pnpm@${PNPM_VERSION} --activate && \
    echo "only-built-dependencies[]=@anthropic-ai/claude-code" > /root/.npmrc && \
    pnpm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" && \
    test -x /pnpm/claude

# NanoClaw source at the pinned tag, with the Claworc no-container patch.
COPY patches/ /tmp/claworc-patches/
RUN mkdir -p /opt/nanoclaw && \
    curl -fsSL "https://github.com/nanocoai/nanoclaw/archive/refs/tags/${NANOCLAW_VERSION}.tar.gz" \
      | tar -xz -C /opt/nanoclaw --strip-components=1 && \
    for p in /tmp/claworc-patches/*.patch; do patch -p1 -d /opt/nanoclaw < "$p"; done && \
    rm -rf /tmp/claworc-patches

# Agent-runner dependencies (Claude Agent SDK, MCP SDK, zod, cron-parser)
# from upstream's own lockfile, plus the /app layout the runner source
# expects (/app/src is also baked into its memory-hook command strings).
RUN cd /opt/nanoclaw/container/agent-runner && \
    bun install --frozen-lockfile && \
    mkdir -p /app && \
    ln -s /opt/nanoclaw/container/agent-runner/src /app/src && \
    ln -s /opt/nanoclaw/container/agent-runner/node_modules /app/node_modules && \
    ln -s /opt/nanoclaw/container/skills /app/skills && \
    ln -s /opt/nanoclaw/container/CLAUDE.md /app/CLAUDE.md && \
    chown -R claworc:claworc /opt/nanoclaw

# Upstream group-dir path -> persistent claworc workspace. The runner keeps
# its hardcoded cwd /workspace/agent; the symlink lands it on the PVC.
RUN mkdir -p /workspace && \
    ln -s /home/claworc/workspace /workspace/agent && \
    chown -R claworc:claworc /workspace

COPY defaults/container.json /defaults/container.json

COPY rootfs/ /

# Claworc agent shim (docs/shim.md): the universal exec-based interface the
# control plane invokes over SSH.
COPY shim/ /opt/claworc/shim/
RUN printf '%s\n' "${NANOCLAW_VERSION#v}" > /opt/claworc/shim/nanoclaw.version && \
    chmod 0755 /opt/claworc/shim/meta \
               /opt/claworc/shim/health \
               /opt/claworc/shim/chat-send \
               /opt/claworc/shim/chat-abort \
               /opt/claworc/shim/session-reset \
               /opt/claworc/shim/config-get \
               /opt/claworc/shim/config-set \
               /opt/claworc/shim/configure-llm \
               /opt/claworc/shim/restart \
               /opt/claworc/shim/shim-selftest && \
    chmod 0644 /opt/claworc/shim/agent.txt /opt/claworc/shim/agent.svg \
               /opt/claworc/shim/nanoclaw.version /opt/claworc/shim/lib/*.mjs

RUN chmod +x /etc/s6-overlay/s6-rc.d/init-setup/up \
             /etc/s6-overlay/s6-rc.d/init-agent-seed/up \
             /etc/s6-overlay/scripts/init-setup.sh \
             /etc/s6-overlay/scripts/init-agent-seed.sh \
             /etc/s6-overlay/s6-rc.d/svc-agent/run \
             /etc/s6-overlay/s6-rc.d/svc-sshd/run

RUN mkdir -p /var/log/claworc && chown claworc:claworc /var/log/claworc

# Reduce SUID surface. Stripping su's SUID bit is safe: the shim invokes it
# as root (SSH exec), and root does not need SUID to switch users.
RUN chmod u-s /usr/bin/su /usr/bin/mount /usr/bin/umount /usr/bin/newgrp \
    /usr/bin/chsh /usr/bin/chfn /usr/bin/gpasswd /usr/bin/chage \
    /usr/lib/openssh/ssh-keysign 2>/dev/null || true

ENTRYPOINT ["/init"]
