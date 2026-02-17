# Base image with Ubuntu XFCE and Chromium (supports amd64/arm64)
FROM lscr.io/linuxserver/webtop:ubuntu-xfce

# Install dependencies
RUN apt-get update && apt-get install -y \
    curl \
    ca-certificates \
    git \
    gnupg \
    python3 \
    python3-pip \
    python3-venv \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js 22 via NodeSource
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y nodejs && \
    npm install -g openclaw@latest

# Install pipx and upgrade pip
RUN python3 -m pip install --no-cache-dir --break-system-packages --upgrade pip setuptools wheel pipx

# Install Poetry
RUN curl -sSL https://install.python-poetry.org | POETRY_HOME=/usr/local python3 -

# Configure environment for claworc user
# Webtop creates user 'abc' with PUID/PGID — rename it to 'claworc'
ENV PUID=1000
ENV PGID=1000
ENV HOME=/config

# Rename abc user/group to claworc so su - claworc works
RUN usermod -l claworc abc && \
    groupmod -n claworc abc && \
    sed -i 's|/config:/bin/bash|/home/claworc:/bin/bash|' /etc/passwd

# Create claworc home as symlink to /config
RUN ln -s /config /home/claworc

# Add local bin to PATH in bashrc
RUN echo 'export PATH="/config/.local/bin:$PATH"' >> /config/.bashrc

# Copy browser config
COPY --chown=claworc:claworc browser.json /config/browser.json

# Copy custom service script for OpenClaw (s6-overlay)
COPY scripts/openclaw-run /custom-services.d/openclaw
RUN chmod +x /custom-services.d/openclaw

# Copy Chromium autostart for debugging
COPY scripts/chromium-autostart.desktop /config/.config/autostart/chromium.desktop
RUN chown claworc:claworc /config/.config/autostart/chromium.desktop

# Create OpenClaw directory
RUN mkdir -p /config/.openclaw && chown claworc:claworc /config/.openclaw

EXPOSE 3000

