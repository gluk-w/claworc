# Claworc Hermes agent image (claworc/hermes).
#
# Hermes Agent by Nous Research (https://github.com/NousResearch/hermes-agent)
# behind the Claworc Agent Shim Contract v1 (docs/shim.md): debian-slim,
# s6-overlay as PID 1, the hardened sshd, and a shim at /opt/claworc/shim
# that drives Hermes' non-interactive CLI (`hermes chat -q ... -Q`) with
# native resume-by-session-id persistence.
#
# Lean by design: no VNC/browser (those live in the claworc/<browser>-browser
# images) and no Hermes messaging-gateway daemon — each chat turn spawns the
# CLI, so there is no svc-agent service and the shim's restart verb is a
# contract-legal no-op.

FROM debian:bookworm-slim

ARG S6_OVERLAY_VERSION=3.2.0.2
ARG TARGETARCH
# Pinned Hermes release tag (or any git ref) from NousResearch/hermes-agent.
ARG HERMES_VERSION=v2026.8.3

# Create claworc user (UID 1000) — all agent state stays owned by it.
RUN useradd -m -u 1000 -s /bin/bash claworc

# System packages: sshd (the contract's only hard runtime dependency),
# python3 + venv for Hermes (bookworm ships 3.11, inside Hermes'
# requires-python >=3.11,<3.14), git (Hermes install + its git/worktree
# tooling), ripgrep (Hermes' file-search tools), jq/procps for the shim
# and selftest.
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates curl xz-utils \
      openssh-server \
      python3 python3-venv \
      git ripgrep jq procps && \
    rm -rf /var/lib/apt/lists/*

RUN S6_ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "aarch64" || echo "x86_64") && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-noarch.tar.xz" \
      | tar -C / -Jxpf - && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-${S6_ARCH}.tar.xz" \
      | tar -C / -Jxpf -

# uv — the package manager Hermes' own installer uses. Multi-arch static
# binaries copied from the pinned upstream image.
COPY --from=ghcr.io/astral-sh/uv:0.11.6 /uv /uvx /usr/local/bin/

# ---------------------------------------------------------------------------
# Hermes install, pinned to HERMES_VERSION.
#
# Mirrors the official managed layout (full checkout + editable install +
# venv OUTSIDE the source tree — see the hermes-agent README's contributor
# notes): repo-relative data (locales/, skills/, prompts) resolves from the
# checkout, and the venv can't be clobbered by agent file operations.
# Base dependencies only (exact-pinned upstream); the heavy `[all]` extra
# (voice, messaging platforms) is deliberately skipped — Hermes lazy-installs
# optional backends via uv at runtime when a user enables them.
# ---------------------------------------------------------------------------
RUN git clone --depth 1 --branch "${HERMES_VERSION}" \
      https://github.com/NousResearch/hermes-agent /opt/hermes/hermes-agent && \
    uv venv --python /usr/bin/python3 /opt/hermes/venv && \
    VIRTUAL_ENV=/opt/hermes/venv uv pip install -e /opt/hermes/hermes-agent && \
    ln -s /opt/hermes/venv/bin/hermes /usr/local/bin/hermes && \
    printf '%s\n' "${HERMES_VERSION}" > /opt/hermes/VERSION && \
    /opt/hermes/venv/bin/python -m compileall -q /opt/hermes/hermes-agent || true

# Sanity check: the pinned install must answer --version (the shim's health
# verb runs the same probe at runtime).
RUN /opt/hermes/venv/bin/hermes --version

# Baked ~/.hermes skeleton: minimal config.yaml (with the claworc-managed
# model block configure-llm rewrites) + empty .env. Hermes needs no
# interactive onboarding when model.provider/base_url/api_key are present in
# config.yaml (hermes_cli/main.py:_has_any_provider_configured). Seeded onto
# the PVC by the init-agent-seed oneshot (or lazily by the shim verbs).
COPY skeleton/ /opt/hermes-skeleton/.hermes/
RUN chown -R claworc:claworc /opt/hermes-skeleton && \
    chmod 0644 /opt/hermes-skeleton/.hermes/config.yaml /opt/hermes-skeleton/.hermes/.env

COPY rootfs/ /

# Claworc agent shim (docs/shim.md): the universal exec-based interface the
# control plane invokes over SSH. Verbs must be 0755; agent.txt/agent.svg are
# static identity files read over SFTP.
COPY shim/ /opt/claworc/shim/
RUN chmod 0755 /opt/claworc/shim/meta \
               /opt/claworc/shim/health \
               /opt/claworc/shim/chat-send \
               /opt/claworc/shim/chat-abort \
               /opt/claworc/shim/session-reset \
               /opt/claworc/shim/config-get \
               /opt/claworc/shim/config-set \
               /opt/claworc/shim/configure-llm \
               /opt/claworc/shim/restart \
               /opt/claworc/shim/shim-selftest \
               /opt/claworc/shim/lib/ensure-seed.sh && \
    chmod 0644 /opt/claworc/shim/agent.txt /opt/claworc/shim/agent.svg

RUN chmod +x /etc/s6-overlay/s6-rc.d/init-setup/up \
             /etc/s6-overlay/s6-rc.d/init-agent-seed/up \
             /etc/s6-overlay/scripts/init-setup.sh \
             /etc/s6-overlay/scripts/init-agent-seed.sh \
             /etc/s6-overlay/s6-rc.d/svc-sshd/run

# Pre-create the agent log dir (init-setup.sh recreates it at boot anyway).
RUN mkdir -p /var/log/claworc && chown claworc:claworc /var/log/claworc

# Reduce SUID surface. Stripping su's SUID bit is safe: the shim invokes it
# as root (SSH exec), and root does not need SUID to switch users.
RUN chmod u-s /usr/bin/su /usr/bin/mount /usr/bin/umount /usr/bin/newgrp \
    /usr/bin/chsh /usr/bin/chfn /usr/bin/gpasswd /usr/bin/chage \
    /usr/lib/openssh/ssh-keysign 2>/dev/null || true

ENTRYPOINT ["/init"]
