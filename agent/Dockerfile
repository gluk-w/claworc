FROM debian:bookworm-slim AS base

ARG S6_OVERLAY_VERSION=3.2.0.2
ARG TARGETARCH

# Create claworc user (UID 1000)
RUN useradd -m -u 1000 -s /bin/bash claworc && \
    mkdir -p /defaults

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
      build-essential procps nodejs && \
    echo "en_US.UTF-8 UTF-8" >> /etc/locale.gen && locale-gen && \
    npm install -g openclaw@latest && \
    curl -sSL https://install.python-poetry.org | POETRY_HOME=/usr/local python3 - && \
    rm -rf /var/lib/apt/lists/*


# Install s6-overlay
RUN S6_ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "aarch64" || echo "x86_64") && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-noarch.tar.xz" \
      | tar -C / -Jxpf - && \
    curl -fsSL "https://github.com/just-containers/s6-overlay/releases/download/v${S6_OVERLAY_VERSION}/s6-overlay-${S6_ARCH}.tar.xz" \
      | tar -C / -Jxpf -

# Install noVNC + websockify from git
RUN git clone --branch v1.4.0 --depth 1 https://github.com/novnc/noVNC.git /opt/novnc && \
    git clone --branch v0.12.0 --depth 1 https://github.com/novnc/websockify /opt/novnc/utils/websockify

# Homebrew directory
RUN mkdir -p /home/linuxbrew/.linuxbrew && \
    chown -R claworc:claworc /home/linuxbrew/.linuxbrew

COPY browser.json /defaults/browser.json
COPY stealth-extension/ /opt/stealth-extension/
COPY rootfs/ /

RUN chmod +x /etc/s6-overlay/s6-rc.d/init-setup/up \
    /etc/s6-overlay/s6-rc.d/svc-xvnc/run \
    /etc/s6-overlay/s6-rc.d/svc-novnc/run \
    /etc/s6-overlay/s6-rc.d/svc-desktop/run \
    /etc/s6-overlay/s6-rc.d/svc-openclaw/run \
    /etc/s6-overlay/s6-rc.d/svc-sshd/run

ENTRYPOINT ["/init"]
