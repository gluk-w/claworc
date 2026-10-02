#!/bin/bash
# Seeds /home/claworc/.hermes from the image-baked skeleton on first boot,
# then applies the initial LLM routing passed by the control plane.
#
# The Dockerfile bakes a minimal ~/.hermes tree (config.yaml with the
# claworc-managed model block + an empty .env) into /opt/hermes-skeleton.
# This oneshot copies it onto the (possibly-empty) PVC mounted at
# /home/claworc. Idempotent: a no-op when /home/claworc/.hermes already
# exists (PVC carried state from a previous boot).
#
# The heavy lifting is shared with the shim verbs via ensure-seed.sh so the
# selftest / verbs also work in containers that never ran the s6 boot
# sequence (e.g. `docker run --entrypoint sh`).

set -e

/opt/claworc/shim/lib/ensure-seed.sh

# ---------------------------------------------------------------------------
# First-boot LLM routing: the control plane passes the configure-llm routing
# document in CLAWORC_INITIAL_LLM_CONFIG (docs/shim.md). Apply it through the
# shim's own verb so boot and reconfiguration share one code path.
# ---------------------------------------------------------------------------
if [ -n "${CLAWORC_INITIAL_LLM_CONFIG:-}" ]; then
    if ! printf '%s' "$CLAWORC_INITIAL_LLM_CONFIG" | /opt/claworc/shim/configure-llm; then
        echo "configure-llm failed; continuing boot without initial LLM routing" >&2
    fi
fi
