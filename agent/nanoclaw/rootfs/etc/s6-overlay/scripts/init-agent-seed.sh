#!/bin/bash
# Seeds the NanoClaw agent workspace on the persistent volume on first boot
# (s6-rc oneshot, after init-setup, before svc-agent). Idempotent: only
# writes files that don't exist yet, so agent-accumulated state (memory/,
# conversations/, user edits to container.json) is never clobbered.
#
# The workspace lives at /home/claworc/workspace (the instance PVC); the
# image's /workspace/agent symlink points here so upstream NanoClaw's
# hardcoded /workspace/agent paths (runner cwd, container.json, memory
# scaffold) work unchanged.

set -e

WORKSPACE=/home/claworc/workspace

mkdir -p "$WORKSPACE"

# Per-agent-group config read by the agent-runner at startup
# (container/agent-runner/src/config.ts in the NanoClaw repo).
if [ ! -f "$WORKSPACE/container.json" ] && [ -f /defaults/container.json ]; then
    install -m 0644 /defaults/container.json "$WORKSPACE/container.json"
fi

# Upstream shared agent doctrine (workspace layout, memory conventions,
# communication rules). Upstream composes this per group; we seed the shared
# base once and let the agent/user evolve it.
if [ ! -f "$WORKSPACE/CLAUDE.md" ] && [ -f /opt/nanoclaw/container/CLAUDE.md ]; then
    install -m 0644 /opt/nanoclaw/container/CLAUDE.md "$WORKSPACE/CLAUDE.md"
fi

chown -R claworc:claworc "$WORKSPACE"
echo "init-agent-seed: NanoClaw workspace ready at $WORKSPACE"
