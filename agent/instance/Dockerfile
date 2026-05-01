FROM debian:bookworm-slim AS base

ARG S6_OVERLAY_VERSION=3.2.0.2
ARG TARGETARCH
ARG USE_CHINA_MIRRORS=false

# Slim agent image: OpenClaw + sshd + cron only. The browser (Chromium /
# Chrome / Brave) and the VNC stack live in glukw/claworc-browser-* images
# launched on demand by the control plane; this image has none of those
# packages.
#
# Published as glukw/claworc-agent:latest. New instances created via the
# control-plane API default to this image; legacy instances continue to pull
# the old combined glukw/openclaw-vnc-* images already in the registry.

# Create claworc user (UID 1000)
RUN useradd -m -u 1000 -s /bin/bash claworc && \
    mkdir -p /defaults

# Optional China mirrors for faster downloads in CN regions.
RUN if [ "$USE_CHINA_MIRRORS" = "true" ]; then \
      sed -i 's|deb.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources; \
    fi

# System packages + Node.js 22 in a single apt transaction.
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends curl ca-certificates xz-utils gnupg && \
    curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      tzdata locales \
      openssh-server git python3 python3-pip python3-venv python3-numpy \
      build-essential procps cron nodejs \
      sudo && \
    echo "en_US.UTF-8 UTF-8" >> /etc/locale.gen && locale-gen && \
    if [ "$USE_CHINA_MIRRORS" = "true" ]; then npm config set registry https://registry.npmmirror.com; fi && \
    npm install -g openclaw@latest && \
    curl -sSL https://install.python-poetry.org | POETRY_HOME=/usr/local python3 - && \
    rm -rf /var/lib/apt/lists/*

RUN echo 'claworc ALL=(ALL) NOPASSWD: /usr/bin/apt-get, /usr/bin/apt' > /etc/sudoers.d/claworc-apt && \
    chmod 0440 /etc/sudoers.d/claworc-apt

RUN S6_ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "aarch64" || echo "x86_64") && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-noarch.tar.xz" \
      | tar -C / -Jxpf - && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-${S6_ARCH}.tar.xz" \
      | tar -C / -Jxpf -

# Homebrew on the persistent volume (tools survive across pod restarts).
RUN mkdir -p /home/linuxbrew/.linuxbrew && \
    chown -R claworc:claworc /home/linuxbrew/.linuxbrew

USER claworc
RUN NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" && \
    echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' >> /home/claworc/.bashrc
USER root

COPY browser.json /defaults/browser.json
COPY TOOLS.md /defaults/TOOLS.md
COPY rootfs/ /

RUN chmod +x /etc/s6-overlay/s6-rc.d/init-setup/up \
             /etc/s6-overlay/s6-rc.d/init-openclaw-seed/up \
             /etc/s6-overlay/scripts/init-setup.sh \
             /etc/s6-overlay/scripts/init-openclaw-seed.sh \
             /etc/s6-overlay/s6-rc.d/svc-openclaw/run \
             /etc/s6-overlay/s6-rc.d/svc-sshd/run \
             /etc/s6-overlay/s6-rc.d/svc-cron/run

# Bake .openclaw at build time so the runtime container does not have to run
# `openclaw doctor --fix` and `openclaw onboard` on every boot. We materialize
# the config + workspace as the claworc user (so ownership matches the runtime
# user), then move the result to /opt/openclaw-skeleton so it survives the
# PVC mount on /home/claworc. The init-openclaw-seed s6 oneshot copies it
# back into /home/claworc/.openclaw on first boot if the PVC is empty.
USER claworc
# Gateway-* config (gateway.mode, gateway.bind, controlUi.* etc.) is set at
# runtime by svc-openclaw/run, where it can react to env vars and stay
# best-effort; baking those values here would abort the build if openclaw's
# validator rejects a value (e.g. legacy `gateway.bind localhost`). Doctor
# alone is enough to produce a usable skeleton config.
RUN bash -c '\
    set -e; \
    mkdir -p /home/claworc/.openclaw; \
    openclaw doctor --fix || echo "Doctor finished with exit code $?"; \
    openclaw config set logging.file /var/log/claworc/openclaw.log; \
    openclaw config set browser "$(cat /defaults/browser.json)" --json; \
    openclaw onboard --non-interactive --accept-risk --mode local || echo "Onboard finished with exit code $?"; \
    WORKSPACE=$(openclaw config get agents.defaults.workspace 2>/dev/null | tr -d "\"" | tr -d "'\''"); \
    [ -n "$WORKSPACE" ] || WORKSPACE=/home/claworc/.openclaw/workspace; \
    if [ -d "$WORKSPACE" ]; then \
      install -m 0644 /defaults/TOOLS.md "$WORKSPACE/TOOLS.md"; \
    fi'
USER root
RUN mkdir -p /opt/openclaw-skeleton && \
    mv /home/claworc/.openclaw /opt/openclaw-skeleton/.openclaw && \
    chown -R claworc:claworc /opt/openclaw-skeleton

# Reduce SUID surface.
RUN chmod u-s /usr/bin/su /usr/bin/mount /usr/bin/umount /usr/bin/newgrp \
    /usr/bin/chsh /usr/bin/chfn /usr/bin/gpasswd /usr/bin/chage \
    /usr/bin/crontab /usr/lib/openssh/ssh-keysign

ENTRYPOINT ["/init"]
