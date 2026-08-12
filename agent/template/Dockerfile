# Claworc custom-agent template image.
#
# Minimal skeleton every Claworc-managed agent image needs: debian-slim,
# s6-overlay as PID 1, a hardened sshd (SSH is the contract's only hard
# runtime dependency), and a complete pure-shell implementation of the
# Claworc Agent Shim Contract v1 at /opt/claworc/shim (see docs/shim.md).
#
# Copy this directory, install your agent below, point shim/agent.env's
# CHAT_CMD at it, and adjust shim/meta. Validate with:
#   docker build -t my-agent agent/template/
#   docker run --rm my-agent /opt/claworc/shim/shim-selftest

FROM debian:bookworm-slim

ARG S6_OVERLAY_VERSION=3.2.0.2
ARG TARGETARCH

# Create claworc user (UID 1000) — all agent state stays owned by it.
RUN useradd -m -u 1000 -s /bin/bash claworc

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates curl xz-utils \
      openssh-server \
      python3 jq procps && \
    rm -rf /var/lib/apt/lists/*

RUN S6_ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "aarch64" || echo "x86_64") && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-noarch.tar.xz" \
      | tar -C / -Jxpf - && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-${S6_ARCH}.tar.xz" \
      | tar -C / -Jxpf -

# ---------------------------------------------------------------------------
# Install YOUR agent here, e.g.:
#   RUN npm install -g my-agent           # or pip install / curl a binary ...
# Then set CHAT_CMD in shim/agent.env and update shim/meta accordingly.
# Agents with a daemon should add an s6 longrun service named `svc-agent`
# (see agent/openclaw/rootfs/etc/s6-overlay/s6-rc.d/svc-agent) and switch
# shim/restart to `s6-svc -r /run/service/svc-agent`.
# ---------------------------------------------------------------------------

COPY rootfs/ /
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
               /opt/claworc/shim/shim-selftest && \
    chmod 0644 /opt/claworc/shim/agent.txt \
               /opt/claworc/shim/agent.svg \
               /opt/claworc/shim/agent.env && \
    chmod +x /etc/s6-overlay/s6-rc.d/init-setup/up \
             /etc/s6-overlay/scripts/init-setup.sh \
             /etc/s6-overlay/s6-rc.d/svc-sshd/run

# Reduce SUID surface. Stripping su's SUID bit is safe: the shim invokes it
# as root (SSH exec), and root does not need SUID to switch users.
RUN chmod u-s /usr/bin/su /usr/bin/mount /usr/bin/umount /usr/bin/newgrp \
    /usr/bin/chsh /usr/bin/chfn /usr/bin/gpasswd /usr/bin/chage \
    /usr/lib/openssh/ssh-keysign 2>/dev/null || true

ENTRYPOINT ["/init"]
