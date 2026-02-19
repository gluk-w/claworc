# LLM Proxy Tests

## Test Coverage

### Unit Tests

**Providers** (`internal/providers/*_test.go`)
- ✅ Anthropic SSE parser (message_start, message_delta, message_stop)
- ✅ OpenAI SSE parser (compatible with 9 providers)
- ✅ Gemini JSON parser
- ✅ Cohere SSE parser
- ✅ Provider registry (Get, All, SetAuthHeader)
- ✅ Custom upstream URLs (Ollama, llama.cpp)

**Proxy Auth** (`internal/proxy/auth_test.go`)
- ✅ Token extraction from headers (Bearer, x-api-key, x-goog-api-key)
- ✅ Token lookup (enabled, disabled, unknown)
- ✅ Token caching (30s TTL)
- ✅ Auth middleware (valid, invalid, missing tokens)
- ✅ Cache invalidation

**Streaming** (`internal/proxy/streaming_test.go`)
- ✅ SSE stream parsing (Anthropic, OpenAI, Gemini formats)
- ✅ Stream passthrough (data is forwarded to client)
- ✅ Token extraction from streams
- ✅ Non-streaming response parsing

**Database** (`internal/database/database_test.go`)
- ✅ Database initialization and migrations
- ✅ Model pricing seeding
- ✅ InstanceToken CRUD operations
- ✅ ProviderKey unique constraints

### Integration Tests

**Main Integration Suite** (`integration_test.go`)
- ✅ Health check endpoint
- ✅ Token registration via management API
- ✅ Token disable/enable lifecycle
- ✅ Token revocation
- ✅ API key synchronization
- ✅ Budget limit enforcement (hard limit blocks requests)
- ✅ Budget/rate limit persistence
- ✅ Admin auth (missing, wrong, valid secrets)
- ✅ Usage query endpoints
- ⏭️ Full proxy flow (skipped - requires real upstream)
- ⏭️ Rate limiting with real requests (skipped - requires real upstream)

**Control Plane Client Tests** (`control-plane/internal/llmproxy/client_test.go`)
- ✅ RegisterInstance
- ✅ RevokeInstance
- ✅ DisableInstance / EnableInstance
- ✅ SyncAPIKeys with multiple providers and scopes
- ✅ SyncInstanceKeys (filters BRAVE_API_KEY)
- ✅ Environment variable to provider mapping
- ✅ Provider to BASE_URL/API_KEY env var mapping
- ✅ GetUsage with query parameters
- ✅ GetInstanceUsage
- ✅ GetLimits / SetLimits

### End-to-End Test

**Shell Script** (`test/e2e_test.sh`)

Starts the actual proxy binary and tests the full lifecycle:
1. Proxy starts and reports healthy
2. Register instance token
3. Sync API keys (global and instance-scoped)
4. Query usage (empty at start)
5. Set budget limit
6. Get limits back from API
7. Disable token
8. Verify auth fails with disabled token
9. Re-enable token
10. Revoke token
11. Verify admin auth required

## Running Tests

### Unit + Integration Tests

```bash
cd llm-proxy
go test ./...
```

Expected output:
```
ok  	github.com/gluk-w/claworc/llm-proxy	0.2s
ok  	github.com/gluk-w/claworc/llm-proxy/internal/database	0.01s
ok  	github.com/gluk-w/claworc/llm-proxy/internal/providers	0.01s
ok  	github.com/gluk-w/claworc/llm-proxy/internal/proxy	0.01s
```

### Control Plane Client Tests

```bash
cd control-plane
go test ./internal/llmproxy/...
```

Expected output:
```
ok  	github.com/gluk-w/claworc/control-plane/internal/llmproxy	0.01s
```

### End-to-End Test

```bash
cd llm-proxy
./test/e2e_test.sh
```

Expected output:
```
=== LLM Proxy E2E Test ===
Building proxy...
Starting proxy...
Proxy is healthy
Test 1: Register instance token
  PASS
Test 2: Sync API keys
  PASS
...
=== All tests passed ===
```

## Test Data

Tests use temporary SQLite databases created in `/tmp/llm-proxy-test-*` or `/tmp/proxy-test-*`. Cleanup is automatic via defer statements.

Mock data used:
- Instance names: `bot-test`, `bot-example`, `bot-disabled`
- Tokens: `test-token-123`, `valid-token`, `disabled-token`
- API keys: `sk-ant-test`, `sk-openai-test`, `sk-ant-global`
- Models: `claude-3-5-sonnet-20241022`, `gpt-4o`, `gemini-2.0-flash`

## Coverage Goals

Current coverage (estimated):
- **Providers**: ~95% (all parsers, registry, auth headers)
- **Auth**: ~90% (token lookup, caching, middleware)
- **Streaming**: ~85% (SSE parsing, passthrough)
- **Database**: ~80% (CRUD, migrations, constraints)
- **API handlers**: ~70% (token management, limits, usage queries)
- **Budget/Rate limit**: ~60% (middleware logic, caching)

Gaps:
- Full proxy handler with real upstream mocking
- Token usage counting in rate limiter (not just request counting)
- Alert threshold triggering (not yet implemented)
- Concurrent request handling
- Error recovery and retry logic

## CI Integration

Add to `.github/workflows/test.yml`:

```yaml
jobs:
  test-llm-proxy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
      - name: Run unit tests
        run: |
          cd llm-proxy
          go test ./... -coverprofile=coverage.out
      - name: Run E2E test
        run: |
          cd llm-proxy
          chmod +x test/e2e_test.sh
          ./test/e2e_test.sh
```
