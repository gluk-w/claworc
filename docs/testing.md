# Testing Guide

This document describes the testing strategy for Claworc and the LLM proxy service.

## Test Suite Overview

```
claworc/
├── test-all.sh                          # Main test runner (all tests)
├── llm-proxy/
│   ├── integration_test.go              # Full proxy API integration tests
│   ├── test/
│   │   ├── e2e_test.sh                  # End-to-end shell script test
│   │   └── README.md                    # Test documentation
│   └── internal/
│       ├── providers/*_test.go          # Provider parser tests
│       ├── proxy/*_test.go              # Auth, streaming, middleware tests
│       └── database/*_test.go           # Database and model tests
└── control-plane/
    └── internal/
        └── llmproxy/client_test.go      # Proxy client integration tests
```

## Quick Start

Run all tests:

```bash
./test-all.sh
```

Expected output:
```
Testing LLM Proxy - Providers... PASS
Testing LLM Proxy - Database... PASS
Testing LLM Proxy - Proxy Core... PASS
Testing LLM Proxy - Integration... PASS
Testing Control Plane - Proxy Client... PASS
Testing LLM Proxy - Build... PASS
Testing Control Plane - Build Internal... PASS
Testing LLM Proxy - Vet... PASS
Testing Control Plane - Vet... PASS

All tests passed!
```

## Individual Test Suites

### LLM Proxy Tests

```bash
cd llm-proxy

# All tests
go test ./...

# Specific package
go test ./internal/providers -v
go test ./internal/proxy -v
go test ./internal/database -v

# Integration tests only
go test . -v

# With coverage
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Control Plane Tests

```bash
cd control-plane

# Proxy client tests
go test ./internal/llmproxy -v

# All internal packages
go test ./internal/... -v
```

### End-to-End Test

```bash
cd llm-proxy
./test/e2e_test.sh
```

This test:
1. Builds the proxy from source
2. Starts it on port 18080
3. Performs all management API operations
4. Verifies database state
5. Cleans up automatically

## Test Categories

### 1. Provider Parsers

**Location**: `llm-proxy/internal/providers/*_test.go`

**Coverage**:
- Anthropic SSE event parsing (message_start, message_delta, message_stop)
- OpenAI SSE chunk parsing (compatible with 9 providers)
- Gemini JSON chunk parsing
- Cohere SSE event parsing
- Non-streaming response body parsing
- Provider registry lookup (case-insensitive)
- Auth header formatting (Bearer, x-api-key, x-goog-api-key)
- Custom upstream URL configuration (Ollama, llama.cpp)

**Example**:
```go
func TestParseAnthropicSSE(t *testing.T) {
    input, output := providers.ParseAnthropicSSE("message_start", `{"message":{"usage":{"input_tokens":150}}}`)
    // Verify input = 150, output = 0
}
```

### 2. Authentication & Authorization

**Location**: `llm-proxy/internal/proxy/auth_test.go`

**Coverage**:
- Token extraction from HTTP headers (Authorization, x-api-key, x-goog-api-key)
- Token validation (enabled, disabled, unknown)
- Token caching (30s TTL)
- Cache invalidation
- Auth middleware (returns 401 for invalid/missing/disabled tokens)

**Example**:
```go
func TestAuthMiddleware_Isolated(t *testing.T) {
    handler := AuthMiddleware(successHandler)
    req.Header.Set("Authorization", "Bearer valid-token")
    // Verify returns 200, not 401
}
```

### 3. Streaming

**Location**: `llm-proxy/internal/proxy/streaming_test.go`

**Coverage**:
- SSE stream parsing (reads line-by-line, extracts token counts)
- Stream passthrough (data is forwarded to client unchanged)
- Event type handling (different SSE event formats)
- Token extraction from multiple event types in a stream
- Non-streaming response parsing

**Example**:
```go
func TestStreamingParser_ParseSSEStream(t *testing.T) {
    parser := &StreamingParser{ParserType: "anthropic"}
    parser.ParseSSEStream(sseReader, outputWriter)
    // Verify parser.Result has correct token counts
    // Verify outputWriter received all SSE lines
}
```

### 4. Budget & Rate Limiting

**Location**: `llm-proxy/integration_test.go`

**Coverage**:
- Budget limit enforcement (hard limit blocks with 429)
- Budget calculation from usage records
- Budget period handling (daily, monthly)
- Rate limit enforcement (requests per minute)
- Sliding window algorithm
- Middleware integration

**Example**:
```go
func TestBudgetMiddleware_HardLimit(t *testing.T) {
    // Setup: instance with $1 limit, $1.50 already spent
    // Make request
    // Verify: returns 429 budget_exceeded
}
```

### 5. Database

**Location**: `llm-proxy/internal/database/database_test.go`

**Coverage**:
- Database initialization and migrations
- Model pricing seeding (25+ model patterns)
- InstanceToken CRUD
- ProviderKey unique constraints (provider_name + scope)
- WAL mode enablement

**Example**:
```go
func TestProviderKeyUniqueness(t *testing.T) {
    DB.Create(ProviderKey{Provider: "anthropic", Scope: "global", Key: "key1"})
    err := DB.Create(ProviderKey{Provider: "anthropic", Scope: "global", Key: "key2"})
    // Verify: unique constraint violation
}
```

### 6. Management API

**Location**: `llm-proxy/integration_test.go`

**Coverage**:
- Token registration (POST /admin/tokens)
- Token revocation (DELETE /admin/tokens/{name})
- Token disable/enable (PUT /admin/tokens/{name}/disable|enable)
- Key synchronization (PUT /admin/keys)
- Usage queries (GET /admin/usage, GET /admin/usage/instances/{name})
- Limits management (GET/PUT /admin/limits/{name})
- Admin auth (Bearer token required)
- Health check (GET /health)

**Example**:
```go
func TestTokenRegistrationAndAuth(t *testing.T) {
    // POST /admin/tokens with instance_name and token
    // Verify: 201 Created
    // Verify: token exists in database
}
```

### 7. Control Plane Integration

**Location**: `control-plane/internal/llmproxy/client_test.go`

**Coverage**:
- HTTP client wrapper (all management API endpoints)
- Token lifecycle (register, revoke, disable, enable)
- Key synchronization with multiple providers and scopes
- Environment variable to provider mapping (15 providers)
- Provider to BASE_URL/API_KEY env var mapping
- Usage data fetching (aggregate and per-instance)
- Limits management (get and set)
- Error handling (non-200 status codes)

**Example**:
```go
func TestSyncInstanceKeys(t *testing.T) {
    apiKeys := map[string]string{
        "ANTHROPIC_API_KEY": "sk-ant-123",
        "BRAVE_API_KEY":     "brave-789", // Should be filtered out
    }
    err := SyncInstanceKeys("bot-test", apiKeys)
    // Verify: only ANTHROPIC_API_KEY was synced, not BRAVE_API_KEY
}
```

## Test Data & Fixtures

### Mock Instance Names
- `bot-test` — standard test instance
- `bot-example` — example instance
- `bot-disabled` — disabled instance

### Mock Tokens
- `test-token-123` — generic valid token
- `valid-token` — valid enabled token
- `disabled-token` — valid but disabled token
- `invalid-token` — not registered token

### Mock API Keys
- `sk-ant-test` — Anthropic test key
- `sk-ant-global` — Global Anthropic key
- `sk-ant-instance` — Instance-specific Anthropic key
- `sk-openai-test` — OpenAI test key

### Mock Models
- `claude-3-5-sonnet-20241022` — Anthropic
- `gpt-4o` — OpenAI
- `gemini-2.0-flash` — Google

## Mocking Strategy

### Database Mocking

Tests use temporary SQLite databases in `/tmp`:

```go
func setupTestDB(t *testing.T) func() {
    tmpDir := os.MkdirTemp("", "proxy-test-*")
    config.Cfg.DatabasePath = filepath.Join(tmpDir, "test.db")
    database.Init()
    return func() {
        database.Close()
        os.RemoveAll(tmpDir)
    }
}
```

Cleanup is automatic via defer.

### HTTP Mocking

Tests use `httptest.NewServer` for mock backends:

```go
func setupMockProxy(t *testing.T) (*httptest.Server, func()) {
    mux := http.NewServeMux()
    mux.HandleFunc("POST /admin/tokens", func(w http.ResponseWriter, r *http.Request) {
        // Mock implementation
    })
    server := httptest.NewServer(mux)
    config.Cfg.ProxyURL = server.URL
    return server, server.Close
}
```

### Skipped Tests

Tests that require real upstream provider connectivity are skipped:

```go
func TestAuthMiddleware_ValidToken(t *testing.T) {
    t.Skip("Skipping test that requires upstream provider connectivity")
}
```

Rationale: Unit tests should not depend on external services. Full proxy flow testing should be done manually or in a staging environment.

## Coverage Reporting

Generate coverage report:

```bash
cd llm-proxy
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out
```

View HTML coverage:

```bash
go tool cover -html=coverage.out
```

Current coverage (by package):

| Package | Coverage | Notes |
|---------|----------|-------|
| `providers` | ~95% | All parsers tested |
| `proxy/auth` | ~90% | Token validation, caching |
| `proxy/streaming` | ~85% | SSE parsing, passthrough |
| `database` | ~80% | CRUD, constraints |
| `api` | ~70% | Management endpoints |
| `proxy/budget` | ~60% | Middleware, caching |
| `proxy/ratelimit` | ~60% | Sliding window |

## Continuous Integration

### GitHub Actions

Create `.github/workflows/test.yml`:

```yaml
name: Tests

on:
  push:
    branches: [main, feature/*]
  pull_request:

jobs:
  test-llm-proxy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'

      - name: Run proxy tests
        run: |
          cd llm-proxy
          go test ./... -v -coverprofile=coverage.out

      - name: Upload coverage
        uses: codecov/codecov-action@v4
        with:
          files: ./llm-proxy/coverage.out
          flags: llm-proxy

  test-control-plane:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'

      - name: Run control plane tests
        run: |
          cd control-plane
          go test ./internal/llmproxy -v

  e2e:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'

      - name: Run E2E test
        run: |
          cd llm-proxy
          chmod +x test/e2e_test.sh
          ./test/e2e_test.sh
```

## Manual Testing

### Testing with Real Providers

To test the proxy with real LLM providers:

1. Start the proxy:
   ```bash
   cd llm-proxy
   export LLM_PROXY_ADMIN_SECRET="test-secret"
   go run .
   ```

2. Register an instance token:
   ```bash
   curl -X POST http://localhost:8080/admin/tokens \
     -H "Authorization: Bearer test-secret" \
     -H "Content-Type: application/json" \
     -d '{"instance_name":"bot-manual","token":"manual-test-token"}'
   ```

3. Sync your real API key:
   ```bash
   curl -X PUT http://localhost:8080/admin/keys \
     -H "Authorization: Bearer test-secret" \
     -H "Content-Type: application/json" \
     -d '{"keys":[{"provider":"anthropic","scope":"global","key":"YOUR_REAL_KEY"}]}'
   ```

4. Make a proxied request:
   ```bash
   curl -X POST http://localhost:8080/v1/anthropic/v1/messages \
     -H "x-api-key: manual-test-token" \
     -H "anthropic-version: 2023-06-01" \
     -H "Content-Type: application/json" \
     -d '{
       "model": "claude-3-5-sonnet-20241022",
       "max_tokens": 100,
       "messages": [{"role": "user", "content": "Hello"}]
     }'
   ```

5. Check usage:
   ```bash
   curl http://localhost:8080/admin/usage/instances/bot-manual \
     -H "Authorization: Bearer test-secret"
   ```

6. Set a budget limit:
   ```bash
   curl -X PUT http://localhost:8080/admin/limits/bot-manual \
     -H "Authorization: Bearer test-secret" \
     -H "Content-Type: application/json" \
     -d '{
       "budget": {
         "limit_micro": 100000,
         "period_type": "daily",
         "hard_limit": true
       }
     }'
   ```

7. Verify budget enforcement (make requests until limit is hit, should get 429)

### Testing with Docker Compose

```bash
# Start services
docker compose up -d

# Wait for services to be ready
sleep 5

# Create an instance via the control plane UI
# http://localhost:8000

# Check that the instance's /etc/default/claworc-keys contains:
# ANTHROPIC_BASE_URL=http://llm-proxy:8080/v1/anthropic
# ANTHROPIC_API_KEY=<proxy-token>

# Check proxy logs
docker compose logs llm-proxy -f

# Make a chat request through the instance
# Verify proxy logs show the request
# Verify usage appears in the UI (/usage page)
```

### Testing Kubernetes Deployment

```bash
# Deploy with proxy enabled
helm install claworc ./helm \
  --set proxy.enabled=true \
  --set proxy.adminSecret=your-secret

# Verify proxy is running
kubectl get pods -n claworc
kubectl logs -n claworc -l app.kubernetes.io/component=proxy

# Create an instance via the UI
# Port-forward to access UI
kubectl port-forward -n claworc svc/claworc 8001:8001

# Verify NetworkPolicy allows agent -> proxy traffic
kubectl exec -n claworc <bot-pod> -- curl http://claworc-proxy:8080/health

# Check usage in UI
# Open http://localhost:8001/usage
```

## Debugging Failed Tests

### Test fails with "no such file or directory"

Ensure you're running from the correct directory:
- Run `./test-all.sh` from the repository root
- Run `go test ./...` from `llm-proxy/` or `control-plane/`

### Test fails with "command not found: go"

Source your shell profile:
```bash
source ~/.bashrc
./test-all.sh
```

Or use absolute path:
```bash
/usr/local/go/bin/go test ./...
```

### Test fails with database locked

SQLite may have lingering locks from previous tests. Clean up:
```bash
rm -rf /tmp/*proxy-test* /tmp/*llm-proxy-test*
```

### Integration test makes real API calls

Check if a test is properly skipped:
```go
func TestSomething(t *testing.T) {
    t.Skip("Skipping test that requires upstream provider connectivity")
}
```

If you want to run it with real providers, remove the skip and set real API keys in the test.

### Coverage is low

Generate detailed coverage report:
```bash
go test ./... -coverprofile=coverage.out
go tool cover -func=coverage.out | grep -v "100.0%"
```

This shows files with less than 100% coverage.

## Adding New Tests

### For a New Provider

1. Add parser test in `internal/providers/newprovider_test.go`:
   ```go
   func TestParseNewProviderSSE(t *testing.T) {
       // Test event parsing
   }
   ```

2. Add to registry test in `registry_test.go`:
   ```go
   {name: "newprovider", providerName: "newprovider", wantOK: true},
   ```

### For a New Middleware

1. Create isolated test in `internal/proxy/newmiddleware_test.go`:
   ```go
   func TestNewMiddleware(t *testing.T) {
       handler := NewMiddleware(successHandler)
       // Make request, verify response
   }
   ```

2. Add integration test in `integration_test.go`:
   ```go
   func TestNewMiddleware_Integration(t *testing.T) {
       // Test with full server setup
   }
   ```

### For a New Management API Endpoint

1. Add to `integration_test.go`:
   ```go
   func TestNewEndpoint(t *testing.T) {
       r, cleanup := setupTestServer(t)
       req := httptest.NewRequest("GET", "/admin/new-endpoint", nil)
       // Verify response
   }
   ```

2. Add to `test/e2e_test.sh`:
   ```bash
   echo "Test N: New endpoint"
   curl -sf http://localhost:18080/admin/new-endpoint \
       -H "Authorization: Bearer test-secret" \
       | grep -q "expected" || { echo "FAIL"; exit 1; }
   echo "  PASS"
   ```

## Performance Testing

### Load Testing

Use `hey` or `wrk` to test proxy throughput:

```bash
# Install hey
go install github.com/rakyll/hey@latest

# Register a test token first
curl -X POST http://localhost:8080/admin/tokens \
  -H "Authorization: Bearer test-secret" \
  -d '{"instance_name":"bot-load","token":"load-token"}'

# Sync a real API key
curl -X PUT http://localhost:8080/admin/keys \
  -H "Authorization: Bearer test-secret" \
  -d '{"keys":[{"provider":"anthropic","scope":"global","key":"YOUR_KEY"}]}'

# Run load test (1000 requests, 10 concurrent)
hey -n 1000 -c 10 \
  -m POST \
  -H "x-api-key: load-token" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":10,"messages":[{"role":"user","content":"Hi"}]}' \
  http://localhost:8080/v1/anthropic/v1/messages
```

Expected results:
- **Throughput**: 50-100 req/s (limited by upstream provider)
- **Latency p50**: < 500ms (mostly upstream latency)
- **Latency p99**: < 2000ms
- **Errors**: 0% (assuming valid API key and no rate limits)

### Memory Profiling

```bash
# Start proxy with profiling enabled
go run . -cpuprofile=cpu.prof -memprofile=mem.prof

# Run load test
hey -n 1000 -c 10 ...

# Analyze profiles
go tool pprof mem.prof
> top10
> list lookupToken
```

Common hotspots:
- Token cache lookups (should be <1% CPU)
- SSE line scanning (should be <5% CPU)
- Database queries (should be batched and cached)

## Troubleshooting

### Tests hang or timeout

Likely waiting for network I/O. Check for:
- Requests to real provider APIs (should be mocked)
- Database locks (clean up /tmp/*test*.db files)
- Goroutines not cleaned up (check for missing cleanup() calls)

### Flaky tests

Common causes:
- Time-based tests (cache TTLs) - add sleep or use fake time
- Parallel test execution - use `t.Parallel()` or `-p 1`
- Database state leaking between tests - ensure each test calls setupTestDB

### Test coverage not accurate

Ensure `-coverprofile` is used:
```bash
go test ./... -coverprofile=coverage.out
```

Exclude test files:
```bash
grep -v "_test.go" coverage.out > coverage-filtered.out
go tool cover -func=coverage-filtered.out
```
