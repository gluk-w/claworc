FROM debian:bookworm-slim AS base

ARG S6_OVERLAY_VERSION=3.2.0.2
ARG TARGETARCH
ARG USE_CHINA_MIRRORS=false

# Create claworc user (UID 1000)
RUN useradd -m -u 1000 -s /bin/bash claworc && \
    mkdir -p /defaults

# Optionally use China mirrors for faster downloads in CN regions
RUN if [ "$USE_CHINA_MIRRORS" = "true" ]; then \
      sed -i 's|deb.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources; \
    fi

# Install all system packages + Node.js in one apt transaction
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends curl ca-certificates xz-utils gnupg && \
    curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      tigervnc-standalone-server tigervnc-common \
      openbox xfonts-base fonts-liberation x11-utils dbus-x11 \
      fonts-noto-color-emoji fonts-noto-cjk fonts-dejavu-core fonts-freefont-ttf \
      tzdata locales \
      openssh-server git python3 python3-pip python3-venv python3-numpy \
      build-essential procps cron nodejs \
      sudo && \
    echo "en_US.UTF-8 UTF-8" >> /etc/locale.gen && locale-gen && \
    if [ "$USE_CHINA_MIRRORS" = "true" ]; then npm config set registry https://registry.npmmirror.com; fi && \
    npm install -g openclaw@latest && \
    curl -sSL https://install.python-poetry.org | POETRY_HOME=/usr/local python3 - && \
    rm -rf /var/lib/apt/lists/*

# Grant claworc user passwordless apt access (limited to package management only)
RUN echo 'claworc ALL=(ALL) NOPASSWD: /usr/bin/apt-get, /usr/bin/apt' > /etc/sudoers.d/claworc-apt && \
    chmod 0440 /etc/sudoers.d/claworc-apt

# Install s6-overlay
RUN S6_ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "aarch64" || echo "x86_64") && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-noarch.tar.xz" \
      | tar -C / -Jxpf - && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-${S6_ARCH}.tar.xz" \
      | tar -C / -Jxpf -

# Install noVNC + websockify from git
RUN git clone --branch v1.4.0 --depth 1 https://github.com/novnc/noVNC.git /opt/novnc && \
    git clone --branch v0.12.0 --depth 1 https://github.com/novnc/websockify /opt/novnc/utils/websockify

# Install Homebrew as claworc user
RUN mkdir -p /home/linuxbrew/.linuxbrew && \
    chown -R claworc:claworc /home/linuxbrew/.linuxbrew

USER claworc
RUN NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" && \
    echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' >> /home/claworc/.bashrc
USER root

COPY browser.json /defaults/browser.json
COPY stealth-extension/ /opt/stealth-extension/
COPY rootfs/ /

RUN chmod +x /etc/s6-overlay/s6-rc.d/init-setup/up \
    /etc/s6-overlay/s6-rc.d/svc-xvnc/run \
    /etc/s6-overlay/s6-rc.d/svc-novnc/run \
    /etc/s6-overlay/s6-rc.d/svc-desktop/run \
    /etc/s6-overlay/s6-rc.d/svc-openclaw/run \
    /etc/s6-overlay/s6-rc.d/svc-sshd/run \
    /etc/s6-overlay/s6-rc.d/svc-cron/run


# Remove unnecessary SUID bits to reduce local privilege escalation surface
RUN chmod u-s /usr/bin/su /usr/bin/mount /usr/bin/umount /usr/bin/newgrp \
    /usr/bin/chsh /usr/bin/chfn /usr/bin/gpasswd /usr/bin/chage \
    /usr/bin/crontab /usr/lib/openssh/ssh-keysign

ENTRYPOINT ["/init"]
