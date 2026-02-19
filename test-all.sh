#!/bin/bash
set -e

echo "======================================"
echo "Claworc Test Suite"
echo "======================================"
echo ""

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Track results
FAILED=0

run_test() {
    local name="$1"
    local dir="$2"
    local cmd="$3"

    echo -n "Testing $name... "
    if (cd "$dir" && eval "$cmd") > /tmp/test-output.log 2>&1; then
        echo -e "${GREEN}PASS${NC}"
    else
        echo -e "${RED}FAIL${NC}"
        cat /tmp/test-output.log
        FAILED=1
    fi
}

REPO_ROOT="$(cd "$(dirname "$0")" && pwd)"

# Test 1: LLM Proxy Unit Tests
run_test "LLM Proxy - Providers" "$REPO_ROOT/llm-proxy" "go test ./internal/providers/... -short"
run_test "LLM Proxy - Database" "$REPO_ROOT/llm-proxy" "go test ./internal/database/... -short"
run_test "LLM Proxy - Proxy Core" "$REPO_ROOT/llm-proxy" "go test ./internal/proxy/... -short"
run_test "LLM Proxy - Integration" "$REPO_ROOT/llm-proxy" "go test . -short"

# Test 2: Control Plane Client Tests
run_test "Control Plane - Proxy Client" "$REPO_ROOT/control-plane" "go test ./internal/llmproxy/... -short"

# Test 3: Build Verification
run_test "LLM Proxy - Build" "$REPO_ROOT/llm-proxy" "go build -o /dev/null ."
run_test "Control Plane - Build Internal" "$REPO_ROOT/control-plane" "go build -o /dev/null ./internal/..."

# Test 4: Vet
run_test "LLM Proxy - Vet" "$REPO_ROOT/llm-proxy" "go vet ./..."
run_test "Control Plane - Vet" "$REPO_ROOT/control-plane" "go vet ./internal/..."

echo ""
echo "======================================"
if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed${NC}"
    exit 1
fi
