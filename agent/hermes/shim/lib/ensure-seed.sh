#!/bin/sh
# ensure-seed.sh — idempotently materialize /home/claworc/.hermes from the
# image-baked skeleton at /opt/hermes-skeleton/.hermes.
#
# Called from the init-agent-seed s6 oneshot at boot AND lazily from the shim
# verbs (health, chat-send, config-get/-set, configure-llm), so the shim also
# works in containers started without the s6 boot sequence (conformance
# selftest, `docker run --entrypoint sh`, CI).
set -eu

SKELETON=/opt/hermes-skeleton/.hermes
TARGET=/home/claworc/.hermes

if [ -d "$TARGET" ]; then
    exit 0
fi

if [ ! -d "$SKELETON" ]; then
    echo "ensure-seed: skeleton missing at $SKELETON" >&2
    exit 1
fi

echo "ensure-seed: seeding $TARGET from $SKELETON" >&2
mkdir -p "$TARGET"
cp -a "$SKELETON"/. "$TARGET"/
chown -R claworc:claworc "$TARGET" 2>/dev/null || true
