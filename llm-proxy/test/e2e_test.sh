#!/bin/bash
set -e

# End-to-end test for LLM proxy
# This test starts the proxy, performs token operations, and verifies the API

echo "=== LLM Proxy E2E Test ==="

# Setup
export LLM_PROXY_DATABASE_PATH="/tmp/e2e-test-proxy.db"
export LLM_PROXY_ADMIN_SECRET="test-secret"
export LLM_PROXY_LISTEN_ADDR=":18080"

# Clean up any existing test DB
rm -f "$LLM_PROXY_DATABASE_PATH"

# Build the proxy
echo "Building proxy..."
go build -o /tmp/llm-proxy .

# Start proxy in background
echo "Starting proxy..."
/tmp/llm-proxy &
PROXY_PID=$!

# Wait for proxy to be ready
sleep 2
if ! curl -sf http://localhost:18080/health > /dev/null; then
    echo "ERROR: Proxy failed to start"
    kill $PROXY_PID 2>/dev/null || true
    exit 1
fi
echo "Proxy is healthy"

# Cleanup function
cleanup() {
    echo "Cleaning up..."
    kill $PROXY_PID 2>/dev/null || true
    rm -f "$LLM_PROXY_DATABASE_PATH" /tmp/llm-proxy
}
trap cleanup EXIT

# Test 1: Register instance token
echo "Test 1: Register instance token"
curl -sf http://localhost:18080/admin/tokens \
    -H "Authorization: Bearer test-secret" \
    -H "Content-Type: application/json" \
    -d '{"instance_name":"bot-test","token":"test-token-abc123"}' \
    | grep -q "created" || { echo "FAIL: Token registration"; exit 1; }
echo "  PASS"

# Test 2: Sync API keys
echo "Test 2: Sync API keys"
curl -sf http://localhost:18080/admin/keys \
    -H "Authorization: Bearer test-secret" \
    -H "Content-Type: application/json" \
    -d '{"keys":[{"provider":"anthropic","scope":"global","key":"sk-ant-test"}]}' \
    | grep -q "synced" || { echo "FAIL: Key sync"; exit 1; }
echo "  PASS"

# Test 3: Query usage (should be empty)
echo "Test 3: Query usage"
curl -sf http://localhost:18080/admin/usage \
    -H "Authorization: Bearer test-secret" \
    | grep -q "\[\]" || grep -q "total" || { echo "FAIL: Usage query"; exit 1; }
echo "  PASS"

# Test 4: Set budget limit
echo "Test 4: Set budget limit"
curl -sf http://localhost:18080/admin/limits/bot-test \
    -H "Authorization: Bearer test-secret" \
    -H "Content-Type: application/json" \
    -X PUT \
    -d '{"budget":{"limit_micro":5000000,"period_type":"monthly","hard_limit":true}}' \
    | grep -q "updated" || { echo "FAIL: Set limits"; exit 1; }
echo "  PASS"

# Test 5: Get limits
echo "Test 5: Get limits"
LIMITS=$(curl -sf http://localhost:18080/admin/limits/bot-test \
    -H "Authorization: Bearer test-secret")
echo "$LIMITS" | grep -q "limit_micro" || { echo "FAIL: Get limits"; exit 1; }
echo "  PASS"

# Test 6: Disable token
echo "Test 6: Disable token"
curl -sf http://localhost:18080/admin/tokens/bot-test/disable \
    -H "Authorization: Bearer test-secret" \
    -X PUT \
    | grep -q "disabled" || { echo "FAIL: Disable token"; exit 1; }
echo "  PASS"

# Test 7: Auth with disabled token (should fail)
echo "Test 7: Auth with disabled token"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:18080/v1/anthropic/v1/messages \
    -H "x-api-key: test-token-abc123" \
    -H "Content-Type: application/json" \
    -d '{}')
if [ "$HTTP_CODE" != "401" ]; then
    echo "FAIL: Expected 401 for disabled token, got $HTTP_CODE"
    exit 1
fi
echo "  PASS"

# Test 8: Re-enable token
echo "Test 8: Re-enable token"
curl -sf http://localhost:18080/admin/tokens/bot-test/enable \
    -H "Authorization: Bearer test-secret" \
    -X PUT \
    | grep -q "enabled" || { echo "FAIL: Enable token"; exit 1; }
echo "  PASS"

# Test 9: Revoke token
echo "Test 9: Revoke token"
curl -s http://localhost:18080/admin/tokens/bot-test \
    -H "Authorization: Bearer test-secret" \
    -X DELETE \
    -o /dev/null -w "%{http_code}" \
    | grep -q "204" || { echo "FAIL: Revoke token"; exit 1; }
echo "  PASS"

# Test 10: Auth without admin secret (should fail)
echo "Test 10: Auth without admin secret"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:18080/admin/usage)
if [ "$HTTP_CODE" != "401" ]; then
    echo "FAIL: Expected 401 for missing admin secret, got $HTTP_CODE"
    exit 1
fi
echo "  PASS"

echo ""
echo "=== All tests passed ==="
