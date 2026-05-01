#!/bin/bash
# Seeds /home/claworc/.openclaw from the image-baked skeleton on first boot.
#
# The agent image runs `openclaw doctor --fix` and `openclaw onboard` at build
# time and stashes the resulting tree under /opt/openclaw-skeleton/.openclaw.
# This oneshot copies it onto the (possibly-empty) PVC mounted at
# /home/claworc, so svc-openclaw can `exec openclaw gateway run` immediately
# instead of running doctor/onboard on every container start.
#
# Idempotent: if /home/claworc/.openclaw already exists (e.g. PVC carried
# state from a previous boot) this script is a no-op.

set -e

SKELETON=/opt/openclaw-skeleton/.openclaw
TARGET=/home/claworc/.openclaw

if [ -d "$TARGET" ]; then
    echo "init-openclaw-seed: $TARGET already present, skipping"
    exit 0
fi

if [ ! -d "$SKELETON" ]; then
    echo "init-openclaw-seed: skeleton missing at $SKELETON, skipping"
    exit 1
fi

echo "init-openclaw-seed: seeding $TARGET from $SKELETON"
mkdir -p "$TARGET"
cp -a "$SKELETON"/. "$TARGET"/
chown -R claworc:claworc "$TARGET"
