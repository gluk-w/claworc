#!/usr/bin/env bash
set -euo pipefail

COMPOSE_FILES="-f docker-compose.yml -f docker-compose.test.yml"
HEALTH_URL="http://localhost:8585/health"
HEALTH_TIMEOUT=60

cd "$(dirname "$0")/.."

# ── 1. Cleanup ────────────────────────────────────────────────
echo "==> Cleaning up previous environment..."
docker compose $COMPOSE_FILES down --volumes --remove-orphans 2>/dev/null || true

# Remove any leftover bot containers and volumes
docker ps -a --filter "name=bot-" --format '{{.ID}}' | xargs -r docker rm -f 2>/dev/null || true
docker volume ls --filter "name=bot-" --format '{{.Name}}' | xargs -r docker volume rm 2>/dev/null || true

# ── 2. Pull agent image ───────────────────────────────────────
echo "==> Pulling agent image..."
docker pull --platform linux/amd64 glukw/openclaw-vnc-chromium:latest

# ── 3. Build & Start ──────────────────────────────────────────
echo "==> Building and starting services..."
docker compose $COMPOSE_FILES up -d --build

# ── 4. Health wait ────────────────────────────────────────────
echo "==> Waiting for health endpoint (up to ${HEALTH_TIMEOUT}s)..."
elapsed=0
until curl -sf "$HEALTH_URL" > /dev/null 2>&1; do
  if [ "$elapsed" -ge "$HEALTH_TIMEOUT" ]; then
    echo "ERROR: Health check timed out after ${HEALTH_TIMEOUT}s"
    docker compose $COMPOSE_FILES logs
    exit 1
  fi
  sleep 2
  elapsed=$((elapsed + 2))
done
echo "==> Service is healthy."

# ── 5. Run tests ──────────────────────────────────────────────
echo "==> Running Playwright tests..."
cd tests
npx playwright test "$@"
TEST_EXIT=$?
cd ..

# ── 6. Teardown ───────────────────────────────────────────────
if [ "${SKIP_TEARDOWN:-}" = "1" ]; then
  echo "==> Skipping teardown (SKIP_TEARDOWN=1)"
else
  echo "==> Tearing down..."
  docker compose $COMPOSE_FILES down --volumes --remove-orphans 2>/dev/null || true
  docker ps -a --filter "name=bot-" --format '{{.ID}}' | xargs -r docker rm -f 2>/dev/null || true
  docker volume ls --filter "name=bot-" --format '{{.Name}}' | xargs -r docker volume rm 2>/dev/null || true
fi

exit $TEST_EXIT
