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
    xterm \
    ca-certificates \
    curl \
    wget \
    git \
    python3 \
    python3-pip \
    python3-venv \
    gnupg \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

# Upgrade pip
RUN python3 -m pip install --no-cache-dir --break-system-packages --upgrade --ignore-installed pip setuptools wheel

# Install pipx
RUN python3 -m pip install --no-cache-dir --break-system-packages pipx && \
    python3 -m pipx ensurepath

# Install Poetry
RUN curl -sSL https://install.python-poetry.org | python3 - && \
    ln -s /root/.local/bin/poetry /usr/local/bin/poetry

# Add Python tools to PATH for interactive sessions
RUN echo 'export PATH="/root/.local/bin:$PATH"' >> /root/.bashrc

# Install Google Chrome (AMD64 only)
RUN wget -q -O /tmp/google-chrome-stable_current_amd64.deb https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb && \
    apt-get update && \
    dpkg -i /tmp/google-chrome-stable_current_amd64.deb || true && \
    apt-get install -y -f && \
    rm /tmp/google-chrome-stable_current_amd64.deb && \
    rm -rf /var/lib/apt/lists/*

# Configure Chrome as default browser
RUN mkdir -p /root/.config && \
    echo "[Default Applications]" > /root/.config/mimeapps.list && \
    echo "text/html=google-chrome.desktop" >> /root/.config/mimeapps.list && \
    echo "x-scheme-handler/http=google-chrome.desktop" >> /root/.config/mimeapps.list && \
    echo "x-scheme-handler/https=google-chrome.desktop" >> /root/.config/mimeapps.list && \
    echo "x-scheme-handler/about=google-chrome.desktop" >> /root/.config/mimeapps.list && \
    echo "x-scheme-handler/unknown=google-chrome.desktop" >> /root/.config/mimeapps.list

# Create wrapper script for Chrome to always use --no-sandbox --kiosk flags
RUN mv /usr/bin/google-chrome-stable /usr/bin/google-chrome-stable.real && \
    echo '#!/bin/bash' > /usr/bin/google-chrome-stable && \
    echo 'exec /usr/bin/google-chrome-stable.real --no-sandbox --disable-dev-shm-usage --kiosk "$@"' >> /usr/bin/google-chrome-stable && \
    chmod +x /usr/bin/google-chrome-stable

# Copy Node.js, npm, and installed packages from builder
COPY --from=builder /usr/bin/node /usr/bin/node
COPY --from=builder /usr/bin/npm /usr/bin/npm
COPY --from=builder /usr/lib/node_modules /usr/lib/node_modules

# Symlink for global npm packages
RUN ln -s /usr/lib/node_modules/openclaw/dist/entry.js /usr/local/bin/openclaw

# Create working directory
RUN mkdir -p /root/.openclaw

# Install openclaw browser extension
# RUN openclaw browser extension install && \
#    cp -r ~/.openclaw/browser/chrome-extension /root/

# Install Homebrew for Linux
RUN useradd -m -s /bin/bash linuxbrew || true && \
    mkdir -p /home/linuxbrew/.linuxbrew && \
    chown -R linuxbrew:linuxbrew /home/linuxbrew

# Download and run Homebrew installer as linuxbrew user
RUN su - linuxbrew -c 'NONINTERACTIVE=1 /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"'

# Add Homebrew to PATH for all users
RUN echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' >> /root/.bashrc && \
    echo 'eval "$(/home/linuxbrew/.linuxbrew/bin/brew shellenv)"' >> /etc/profile.d/homebrew.sh

# Install noVNC
RUN git clone --branch v1.4.0 --depth 1 https://github.com/novnc/noVNC.git /opt/novnc && \
    git clone --branch v0.11.0 --depth 1 https://github.com/novnc/websockify /opt/novnc/utils/websockify

# Configure systemd
RUN systemctl set-default multi-user.target

# Copy systemd unit files (7 services, dual VNC)
COPY systemd/xvnc-chrome.service /etc/systemd/system/xvnc-chrome.service
COPY systemd/novnc-chrome.service /etc/systemd/system/novnc-chrome.service
COPY systemd/chrome.service /etc/systemd/system/chrome.service
COPY systemd/xvnc-term.service /etc/systemd/system/xvnc-term.service
COPY systemd/novnc-term.service /etc/systemd/system/novnc-term.service
COPY systemd/terminal.service /etc/systemd/system/terminal.service
COPY systemd/openclaw-gateway.service /etc/systemd/system/openclaw-gateway.service

# Enable all services
RUN systemctl enable xvnc-chrome.service && \
    systemctl enable novnc-chrome.service && \
    systemctl enable chrome.service && \
    systemctl enable xvnc-term.service && \
    systemctl enable novnc-term.service && \
    systemctl enable terminal.service && \
    systemctl enable openclaw-gateway.service

# Prevent systemd from clearing /tmp
RUN mkdir -p /etc/systemd/system/systemd-tmpfiles-setup.service.d && \
    echo "[Service]" > /etc/systemd/system/systemd-tmpfiles-setup.service.d/override.conf && \
    echo "ExecStart=" >> /etc/systemd/system/systemd-tmpfiles-setup.service.d/override.conf

WORKDIR /root

# Health check on Chrome noVNC port
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD curl -f http://localhost:6081/ || exit 1

EXPOSE 6081 6082 5901 5902

CMD ["/sbin/init"]
