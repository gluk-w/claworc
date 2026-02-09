FROM ubuntu:24.04 AS builder

# Install Node.js 22 via NodeSource
RUN apt-get update && apt-get install -y curl ca-certificates git && \
    curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y nodejs && \
    npm install -g openclaw@latest && \
    rm -rf /var/lib/apt/lists/*

FROM ubuntu:24.04

# Install runtime dependencies (no desktop environment)
RUN apt-get update && apt-get install -y \
    tigervnc-standalone-server \
    tigervnc-common \
    systemd \
    systemd-sysv \
    dbus \
    dbus-x11 \
    x11-utils \
    xfonts-base \
    fonts-liberation \
    ca-certificates \
    curl \
    wget \
    git \
    python3 \
    python3-pip \
    python3-venv \
    gnupg \
    build-essential \
    vim \
    && rm -rf /var/lib/apt/lists/*

# Create claworc user
RUN useradd -m -s /bin/bash claworc

# Upgrade pip
RUN python3 -m pip install --no-cache-dir --break-system-packages --upgrade --ignore-installed pip setuptools wheel

# Install pipx
RUN python3 -m pip install --no-cache-dir --break-system-packages pipx
USER claworc
RUN python3 -m pipx ensurepath

# Install Poetry
RUN curl -sSL https://install.python-poetry.org | python3 -
USER root
RUN ln -s /home/claworc/.local/bin/poetry /usr/local/bin/poetry

# Add Python tools to PATH for interactive sessions
RUN echo 'export PATH="/home/claworc/.local/bin:$PATH"' >> /home/claworc/.bashrc

# Install Google Chrome (AMD64 only)
RUN wget -q -O /tmp/google-chrome-stable_current_amd64.deb https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb && \
    apt-get update && \
    dpkg -i /tmp/google-chrome-stable_current_amd64.deb || true && \
    apt-get install -y -f && \
    rm /tmp/google-chrome-stable_current_amd64.deb && \
    rm -rf /var/lib/apt/lists/*

# Configure Chrome as default browser
RUN mkdir -p /home/claworc/.config && \
    echo "[Default Applications]" > /home/claworc/.config/mimeapps.list && \
    echo "text/html=google-chrome.desktop" >> /home/claworc/.config/mimeapps.list && \
    echo "x-scheme-handler/http=google-chrome.desktop" >> /home/claworc/.config/mimeapps.list && \
    echo "x-scheme-handler/https=google-chrome.desktop" >> /home/claworc/.config/mimeapps.list && \
    echo "x-scheme-handler/about=google-chrome.desktop" >> /home/claworc/.config/mimeapps.list && \
    echo "x-scheme-handler/unknown=google-chrome.desktop" >> /home/claworc/.config/mimeapps.list && \
    chown -R claworc:claworc /home/claworc/.config

# Create wrapper script for Chrome to always use --no-sandbox flags
RUN mv /usr/bin/google-chrome-stable /usr/bin/google-chrome-stable.real && \
    echo '#!/bin/bash' > /usr/bin/google-chrome-stable && \
    echo 'exec /usr/bin/google-chrome-stable.real --no-sandbox --disable-dev-shm-usage --no-first-run --no-default-browser-check --password-store=basic "$@"' >> /usr/bin/google-chrome-stable && \
    chmod +x /usr/bin/google-chrome-stable

# Suppress Chrome --no-sandbox warning bar via enterprise policy
RUN mkdir -p /etc/opt/chrome/policies/managed && \
    echo '{"CommandLineFlagSecurityWarningsEnabled": false}' > /etc/opt/chrome/policies/managed/suppress_warnings.json

# Copy Node.js, npm, and installed packages from builder
COPY --from=builder /usr/bin/node /usr/bin/node
COPY --from=builder /usr/bin/npm /usr/bin/npm
COPY --from=builder /usr/lib/node_modules /usr/lib/node_modules

# Symlink for global npm packages
RUN ln -s /usr/lib/node_modules/openclaw/dist/entry.js /usr/local/bin/openclaw

# Create working directory
RUN mkdir -p /home/claworc/.openclaw && chown claworc:claworc /home/claworc/.openclaw

# Install Homebrew for Linux
RUN useradd -m -s /bin/bash linuxbrew || true && \
    mkdir -p /home/linuxbrew/.linuxbrew && \
    chown -R linuxbrew:linuxbrew /home/linuxbrew

# Download and run Homebrew installer as linuxbrew user
RUN su - linuxbrew -c 'NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"' && \
    chmod o+x /home/linuxbrew && \
    chmod -R o+rx /home/linuxbrew/.linuxbrew

# Add Homebrew to PATH for all users
RUN echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' >> /home/claworc/.bashrc && \
    echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' >> /etc/profile.d/homebrew.sh

# Install noVNC
RUN git clone --branch v1.4.0 --depth 1 https://github.com/novnc/noVNC.git /opt/novnc && \
    git clone --branch v0.11.0 --depth 1 https://github.com/novnc/websockify /opt/novnc/utils/websockify

# Configure systemd
RUN systemctl set-default multi-user.target

# Copy helper scripts
COPY scripts/start-xvnc.sh /opt/scripts/start-xvnc.sh
COPY scripts/init-env.sh /opt/scripts/init-env.sh
RUN chmod +x /opt/scripts/start-xvnc.sh /opt/scripts/init-env.sh

# Copy systemd unit files
COPY systemd/init-env.service /etc/systemd/system/init-env.service
COPY systemd/xvnc-chrome.service /etc/systemd/system/xvnc-chrome.service
COPY systemd/novnc-chrome.service /etc/systemd/system/novnc-chrome.service
COPY systemd/chrome.service /etc/systemd/system/chrome.service
COPY systemd/openclaw-gateway.service /etc/systemd/system/openclaw-gateway.service

# Enable all services
RUN systemctl enable init-env.service && \
    systemctl enable xvnc-chrome.service && \
    systemctl enable novnc-chrome.service && \
    systemctl enable chrome.service && \
    systemctl enable openclaw-gateway.service

# Prevent systemd from clearing /tmp
RUN mkdir -p /etc/systemd/system/systemd-tmpfiles-setup.service.d && \
    echo "[Service]" > /etc/systemd/system/systemd-tmpfiles-setup.service.d/override.conf && \
    echo "ExecStart=" >> /etc/systemd/system/systemd-tmpfiles-setup.service.d/override.conf

WORKDIR /home/claworc

# Health check on Chrome noVNC port
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD curl -f http://localhost:6081/ || exit 1


# Setup OpenClaw — config is initialized at runtime by init-env.sh (PVC shadows image layer)
COPY --chown=claworc:claworc browser.json /opt/browser.json
USER claworc
RUN openclaw setup

USER root

CMD ["/sbin/init"]
