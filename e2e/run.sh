#!/usr/bin/env bash
# On-demand E2E test driver for Claworc.
#
# Installs claworc fresh into namespace `claworc-e2e` (with auth disabled),
# port-forwards the service to localhost, runs the Playwright suite, and
# tears the install down again.
#
# Usage:
#   ./e2e/run.sh <path-to-kubeconfig> [-- <playwright args>]
#
# Env toggles:
#   E2E_BUILD=1     Rebuild + push control-plane and agent images before install
#   E2E_KEEP=1      Skip teardown (leaves cluster + port-forward dead but release intact)
#   E2E_HELM_ARGS   Extra args appended to `helm upgrade --install`
#   LOCAL_PORT      Local port for the port-forward (default 18001)

set -euo pipefail

usage() {
  cat >&2 <<EOF
Usage: $0 <path-to-kubeconfig> [-- <playwright args>]

Examples:
  $0 ../kubeconfig
  $0 ~/.kube/config -- --grep "smoke"
  E2E_KEEP=1 $0 ../kubeconfig
  E2E_BUILD=1 $0 ../kubeconfig
EOF
  exit 2
}

if [[ $# -lt 1 ]]; then
  usage
fi

KUBECONFIG_PATH="$1"
shift
if [[ ! -f "$KUBECONFIG_PATH" ]]; then
  echo "error: kubeconfig file not found: $KUBECONFIG_PATH" >&2
  usage
fi

# Allow a `--` separator before forwarded playwright args.
if [[ "${1:-}" == "--" ]]; then
  shift
fi
PLAYWRIGHT_ARGS=("$@")

export KUBECONFIG
KUBECONFIG="$(cd "$(dirname "$KUBECONFIG_PATH")" && pwd)/$(basename "$KUBECONFIG_PATH")"

HELM_RELEASE="${HELM_RELEASE:-claworc-e2e}"
HELM_NAMESPACE="${HELM_NAMESPACE:-claworc-e2e}"
LOCAL_PORT="${LOCAL_PORT:-18001}"
SERVICE_PORT=8001

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
HELM_CHART="$REPO_ROOT/helm"

echo "==> Pre-flight checks"
for bin in kubectl helm npx; do
  if ! command -v "$bin" >/dev/null; then
    echo "error: required command '$bin' not on PATH" >&2
    exit 1
  fi
done
kubectl get ns >/dev/null

PORT_FORWARD_PID=""

cleanup() {
  local rc=$?
  set +e
  if [[ -n "$PORT_FORWARD_PID" ]] && kill -0 "$PORT_FORWARD_PID" 2>/dev/null; then
    echo "==> Stopping port-forward (pid $PORT_FORWARD_PID)"
    kill "$PORT_FORWARD_PID" 2>/dev/null || true
    wait "$PORT_FORWARD_PID" 2>/dev/null || true
  fi
  if [[ -n "${E2E_KEEP:-}" ]]; then
    echo "==> E2E_KEEP=1 set; leaving release '$HELM_RELEASE' in namespace '$HELM_NAMESPACE'"
    echo "    Re-port-forward with: kubectl --kubeconfig $KUBECONFIG -n $HELM_NAMESPACE port-forward svc/$HELM_RELEASE $LOCAL_PORT:$SERVICE_PORT"
  else
    echo "==> Tearing down release '$HELM_RELEASE'"
    if helm status "$HELM_RELEASE" -n "$HELM_NAMESPACE" --kubeconfig "$KUBECONFIG" >/dev/null 2>&1; then
      helm uninstall "$HELM_RELEASE" -n "$HELM_NAMESPACE" --kubeconfig "$KUBECONFIG" || true
    fi
    kubectl delete pvc --all -n "$HELM_NAMESPACE" --kubeconfig "$KUBECONFIG" --ignore-not-found --wait=false --timeout=30s || true
  fi
  exit "$rc"
}
trap cleanup EXIT INT TERM

echo "==> Wiping any prior install in namespace '$HELM_NAMESPACE'"
if helm status "$HELM_RELEASE" -n "$HELM_NAMESPACE" --kubeconfig "$KUBECONFIG" >/dev/null 2>&1; then
  helm uninstall "$HELM_RELEASE" -n "$HELM_NAMESPACE" --kubeconfig "$KUBECONFIG" || true
fi
kubectl delete pvc --all -n "$HELM_NAMESPACE" --kubeconfig "$KUBECONFIG" --ignore-not-found --wait=false --timeout=30s || true

if [[ -n "${E2E_BUILD:-}" ]]; then
  echo "==> E2E_BUILD=1 set; rebuilding control-plane and agent images"
  (cd "$REPO_ROOT" && make control-plane)
  (cd "$REPO_ROOT" && make agent)
fi

echo "==> Helm install: $HELM_RELEASE -> $HELM_NAMESPACE (auth disabled)"
# shellcheck disable=SC2086
helm upgrade --install "$HELM_RELEASE" "$HELM_CHART" \
  -n "$HELM_NAMESPACE" --create-namespace \
  --kubeconfig "$KUBECONFIG" \
  --set "extraEnv.CLAWORC_AUTH_DISABLED=true" \
  --set "config.k8sNamespace=$HELM_NAMESPACE" \
  --set "service.type=ClusterIP" \
  --set "service.nodePort=null" \
  --wait --timeout 5m \
  ${E2E_HELM_ARGS:-}

echo "==> Waiting for deployment rollout"
kubectl rollout status deployment/"$HELM_RELEASE" -n "$HELM_NAMESPACE" --kubeconfig "$KUBECONFIG" --timeout=300s

echo "==> Starting port-forward localhost:$LOCAL_PORT -> svc/$HELM_RELEASE:$SERVICE_PORT"
kubectl --kubeconfig "$KUBECONFIG" -n "$HELM_NAMESPACE" \
  port-forward "svc/$HELM_RELEASE" "$LOCAL_PORT:$SERVICE_PORT" >/dev/null 2>&1 &
PORT_FORWARD_PID=$!

echo "==> Waiting for /health to return 200"
for i in $(seq 1 60); do
  if curl -fsS "http://localhost:$LOCAL_PORT/health" >/dev/null 2>&1; then
    echo "    /health is up"
    break
  fi
  if ! kill -0 "$PORT_FORWARD_PID" 2>/dev/null; then
    echo "error: port-forward died before /health became ready" >&2
    exit 1
  fi
  sleep 2
  if [[ "$i" == "60" ]]; then
    echo "error: /health did not become ready within 120s" >&2
    exit 1
  fi
done

echo "==> Bootstrapping admin user (CLAWORC_AUTH_DISABLED still requires a user row)"
SETUP_REQUIRED=$(curl -fsS "http://localhost:$LOCAL_PORT/api/v1/auth/setup-required" || echo '{}')
if [[ "$SETUP_REQUIRED" == *'"setup_required":true'* ]]; then
  curl -fsS -X POST "http://localhost:$LOCAL_PORT/api/v1/auth/setup" \
    -H "Content-Type: application/json" \
    -d '{"username":"e2e-admin","password":"e2e-admin-password"}' >/dev/null
  echo "    admin user created"
else
  echo "    admin user already exists, skipping"
fi

echo "==> Dismissing first-run analytics-consent dialog (set opt_out)"
curl -fsS -X PUT "http://localhost:$LOCAL_PORT/api/v1/settings" \
  -H "Content-Type: application/json" \
  -d '{"analytics_consent":"opt_out"}' >/dev/null
echo "    analytics consent set to opt_out"

echo "==> Running Playwright suite"
cd "$SCRIPT_DIR"
BASE_URL="http://localhost:$LOCAL_PORT" npx playwright test "${PLAYWRIGHT_ARGS[@]}"

echo "==> Done"
