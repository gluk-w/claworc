#!/bin/bash
# Seeds /home/linuxbrew/.linuxbrew from the image-baked skeleton on first boot.
#
# Homebrew is installed at build time and stashed under
# /opt/homebrew-skeleton/.linuxbrew. This oneshot copies it onto the
# (possibly-empty) PVC mounted at /home/linuxbrew/.linuxbrew so `brew` is
# immediately available without a network install on every container start.
#
# Idempotent: if /home/linuxbrew/.linuxbrew/bin/brew already exists (PVC
# carried state from a previous boot) this script is a no-op.

set -e

SKELETON=/opt/homebrew-skeleton/.linuxbrew
TARGET=/home/linuxbrew/.linuxbrew

if [ -x "$TARGET/bin/brew" ]; then
    echo "init-homebrew-seed: $TARGET/bin/brew already present, skipping"
    exit 0
fi

if [ ! -d "$SKELETON" ]; then
    echo "init-homebrew-seed: skeleton missing at $SKELETON, skipping"
    exit 1
fi

echo "init-homebrew-seed: seeding $TARGET from $SKELETON"
mkdir -p "$TARGET"
cp -a "$SKELETON"/. "$TARGET"/
chown -R claworc:claworc "$TARGET"
