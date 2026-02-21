FROM lscr.io/linuxserver/chromium:latest

RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
    && apt-get install -y --no-install-recommends \
        nodejs git python3-pip python3-venv build-essential openssh-server \
    && npm install -g openclaw@latest \
    && curl -sSL https://install.python-poetry.org | POETRY_HOME=/usr/local python3 - \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /home/linuxbrew/.linuxbrew \
    && chown -R abc:abc /home/linuxbrew/.linuxbrew

COPY autostart /defaults/autostart
COPY default.conf /defaults/default.conf
COPY browser.json /defaults/browser.json
COPY rootfs/ /

RUN chmod +x /etc/s6-overlay/s6-rc.d/svc-openclaw/run \
    && chmod +x /etc/s6-overlay/s6-rc.d/svc-sshd/run \
    && chmod +x /custom-cont-init.d/40-setup-logs \
    && chmod +x /custom-cont-init.d/50-force-autostart

ENV START_DOCKER=false
