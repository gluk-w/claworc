# LLM Proxy

The LLM proxy is an optional reverse proxy service that sits between agent instances and LLM providers. It provides secure API key management, usage tracking, budget enforcement, and rate limiting.

## Overview

When enabled, the proxy intercepts all LLM API requests from agent instances. Real API keys are stored only in the proxy's database — instances receive instance-specific proxy tokens instead. This architecture enables:

- **Instant revocation** — disable access for an instance without rotating the actual API key
- **Usage visibility** — track token consumption and costs across all instances and providers
- **Budget control** — prevent runaway costs with spending limits
- **Centralized key management** — update API keys in one place without restarting instances

## How It Works

```
┌─────────────────┐                      ┌─────────────────┐                   ┌──────────────────┐
│  Agent Instance │                      │   LLM Proxy     │                   │  LLM Provider    │
│                 │                      │                 │                   │  (Anthropic, etc)│
└────────┬────────┘                      └────────┬────────┘                   └────────┬─────────┘
         │                                        │                                     │
         │  ANTHROPIC_BASE_URL=                   │                                     │
         │   http://llm-proxy:8080/v1/anthropic   │                                     │
         │  ANTHROPIC_API_KEY=<proxy-token>       │                                     │
         │                                        │                                     │
         ├───────── POST /v1/messages ───────────>│                                     │
         │                                        │                                     │
         │                                        ├─ validates proxy token              │
         │                                        ├─ checks budget & rate limits        │
         │                                        ├─ swaps in real API key ────────────>│
         │                                        │                                     │
         │                                        │<───── SSE stream with tokens ───────┤
         │                                        │                                     │
         │                                        ├─ extracts token counts              │
         │                                        ├─ records usage + cost               │
         │<───── stream forwarded to agent ──────┤                                     │
         │                                        │                                     │
```

## Supported Providers

The proxy supports **15 LLM providers** with 4 streaming parser families:

| Provider | Parser Family | Base URL |
|----------|---------------|----------|
| **Anthropic** | Anthropic SSE | `https://api.anthropic.com` |
| **OpenAI** | OpenAI SSE | `https://api.openai.com` |
| **Google Gemini** | Gemini JSON | `https://generativelanguage.googleapis.com` |
| **Mistral** | OpenAI SSE | `https://api.mistral.ai` |
| **Groq** | OpenAI SSE | `https://api.groq.com/openai` |
| **DeepSeek** | OpenAI SSE | `https://api.deepseek.com` |
| **xAI** | OpenAI SSE | `https://api.x.ai` |
| **Cohere** | Cohere SSE | `https://api.cohere.com` |
| **Together** | OpenAI SSE | `https://api.together.xyz` |
| **Fireworks** | OpenAI SSE | `https://api.fireworks.ai/inference` |
| **Cerebras** | OpenAI SSE | `https://api.cerebras.ai` |
| **Perplexity** | OpenAI SSE | `https://api.perplexity.ai` |
| **OpenRouter** | OpenAI SSE | `https://openrouter.ai/api` |
| **Ollama** | OpenAI SSE | `http://localhost:11434` (configurable) |
| **llama.cpp** | OpenAI SSE | `http://localhost:8080` (configurable) |

### Local Provider Configuration

For Ollama and llama.cpp, configure custom upstream URLs:

```bash
# Docker
LLM_PROXY_OLLAMA_URL=http://host.docker.internal:11434
LLM_PROXY_LLAMACPP_URL=http://192.168.1.100:8080

# Kubernetes
LLM_PROXY_OLLAMA_URL=http://ollama-service.default.svc.cluster.local:11434
```

## Deployment

### Docker Compose

The proxy runs as a separate container on the same network as the control plane and agent containers:

```yaml
services:
  control-plane:
    image: glukw/claworc:latest
    environment:
      - CLAWORC_PROXY_ENABLED=true
      - CLAWORC_PROXY_URL=http://llm-proxy:8080
      - CLAWORC_PROXY_SECRET=your-secret-here
    depends_on:
      - llm-proxy
    networks:
      - claworc

  llm-proxy:
    image: glukw/claworc-llm-proxy:latest
    environment:
      - LLM_PROXY_ADMIN_SECRET=your-secret-here
    volumes:
      - llm-proxy-data:/app/data
    networks:
      - claworc
```

### Kubernetes (Helm)

Enable the proxy in your Helm values:

```yaml
proxy:
  enabled: true
  adminSecret: "your-secret-here"
  image:
    repository: glukw/claworc-llm-proxy
    tag: latest
  resources:
    requests:
      cpu: 50m
      memory: 64Mi
    limits:
      cpu: 500m
      memory: 256Mi
  persistence:
    enabled: true
    size: 1Gi
```

The Helm chart creates:
- Deployment with the proxy container
- ClusterIP Service for internal communication
- PersistentVolumeClaim for the SQLite database
- Secret for the admin token
- NetworkPolicy allowing agent→proxy and control-plane→proxy traffic

## Configuration

### Environment Variables

**LLM Proxy:**

| Variable | Default | Description |
|----------|---------|-------------|
| `LLM_PROXY_DATABASE_PATH` | `/app/data/llm-proxy.db` | SQLite database path |
| `LLM_PROXY_ADMIN_SECRET` | (required) | Shared secret for management API auth |
| `LLM_PROXY_LISTEN_ADDR` | `:8080` | HTTP listen address |
| `LLM_PROXY_OLLAMA_URL` | (optional) | Override Ollama upstream URL |
| `LLM_PROXY_LLAMACPP_URL` | (optional) | Override llama.cpp upstream URL |

**Control Plane:**

| Variable | Default | Description |
|----------|---------|-------------|
| `CLAWORC_PROXY_ENABLED` | `false` | Enable LLM proxy integration |
| `CLAWORC_PROXY_URL` | `http://llm-proxy:8080` | Proxy base URL |
| `CLAWORC_PROXY_SECRET` | (required) | Must match proxy's `ADMIN_SECRET` |

## Usage Tracking

### Viewing Usage

The dashboard includes a **Usage** page (admin-only) showing aggregate usage across all instances:

- Group by instance, provider, model, or day
- Filter by time period (7d, 30d, 90d, all time)
- View request counts, token usage, and estimated costs

Each instance detail page also has a **Usage** tab showing per-instance breakdown by provider and model.

### Cost Estimation

The proxy includes a pricing table seeded with current rates for major models:

- **Anthropic**: Claude Opus 4, Sonnet 4, Haiku 4, Claude 3.5
- **OpenAI**: GPT-4o, o1, o3-mini, etc.
- **Google**: Gemini 2.0 Flash, Gemini 2.0 Pro, Gemini 1.5
- **Mistral**, **Groq**, **DeepSeek**, **xAI**, **Cohere**

Costs are estimated in microdollars (1/1,000,000 of a dollar) and displayed as formatted USD amounts.

## Budget Limits

Set spending limits per instance to prevent runaway costs:

### Configuration

From the instance detail page, **Usage** tab:

1. **Limit** — spending cap in USD (e.g., `10.00`)
2. **Period** — `daily` or `monthly`
3. **Hard limit** — if enabled, requests are blocked (HTTP 429) when the budget is exceeded

### How It Works

- Budget spend is tracked as the sum of `estimated_cost_micro` from usage records
- The spend sum is cached for 10 seconds, refreshed on next request
- When hard limit is enabled and `spent >= limit`, the proxy returns:
  ```json
  HTTP 429
  {"error":"budget_exceeded","message":"Budget limit has been reached"}
  ```

### Resetting Budget

Budgets reset automatically based on the period type:
- **Daily**: at midnight UTC
- **Monthly**: on the first day of the month

## Rate Limiting

Set request and token rate limits per instance:

### Configuration

From the instance detail page, **Usage** tab:

1. **Requests/min** — maximum requests per minute (0 = unlimited)
2. **Tokens/min** — maximum tokens per minute (0 = unlimited)

Rate limits can be set globally (`provider: "*"`) or per-provider.

### How It Works

- Sliding window algorithm tracks requests in the last 60 seconds
- When a limit is exceeded, the proxy returns:
  ```json
  HTTP 429
  Retry-After: 60
  {"error":"rate_limit_exceeded","message":"Rate limit exceeded"}
  ```

## Management API

The proxy exposes a management API on `/admin/*` for the control plane. All endpoints require `Authorization: Bearer <admin-secret>`.

### Token Management

```http
POST /admin/tokens
{"instance_name": "bot-example", "token": "<64-char-hex>"}

DELETE /admin/tokens/{name}

PUT /admin/tokens/{name}/disable
PUT /admin/tokens/{name}/enable
```

### Key Synchronization

```http
PUT /admin/keys
{
  "keys": [
    {"provider": "anthropic", "scope": "global", "key": "sk-ant-..."},
    {"provider": "openai", "scope": "bot-example", "key": "sk-..."}
  ]
}
```

The `scope` field is either `"global"` (default key for all instances) or an instance name (override for that instance).

### Usage Queries

```http
GET /admin/usage?since=2026-01-01&until=2026-02-01&group_by=provider
GET /admin/usage/instances/{name}?since=2026-01-01
```

Response:
```json
[
  {
    "group": "anthropic",
    "requests": 145,
    "input_tokens": 12500,
    "output_tokens": 8300,
    "estimated_cost_usd": "$0.125000"
  }
]
```

### Limits Management

```http
GET /admin/limits/{name}

PUT /admin/limits/{name}
{
  "budget": {
    "limit_micro": 10000000,
    "period_type": "monthly",
    "alert_threshold": 0.8,
    "hard_limit": true
  },
  "rate_limits": [
    {"provider": "*", "requests_per_minute": 60, "tokens_per_minute": 50000}
  ]
}
```

## Database Schema

The proxy stores data in a SQLite database with WAL mode enabled:

### Tables

**instance_tokens**
- `instance_name` (unique) — K8s-safe instance name (e.g., `bot-example`)
- `token` (unique) — 64-character hex token
- `enabled` — whether the token is currently active

**provider_keys**
- `provider_name` — provider slug (e.g., `anthropic`)
- `scope` — `"global"` or instance name
- `key_value` — the real API key (plaintext, encrypted at control-plane)
- Unique constraint: `(provider_name, scope)`

**usage_records**
- `instance_name` — which instance made the request
- `provider` — which provider was called
- `model` — which model was used
- `input_tokens`, `output_tokens` — token counts
- `estimated_cost_micro` — cost in microdollars
- `status_code` — HTTP response code
- `duration_ms` — request duration
- `created_at` — timestamp (indexed)

**budget_limits**
- `instance_name` (unique)
- `limit_micro` — spending cap in microdollars
- `period_type` — `"daily"` or `"monthly"`
- `alert_threshold` — e.g., 0.8 for 80% warning (not enforced yet)
- `hard_limit` — whether to block requests when exceeded

**rate_limits**
- `instance_name`, `provider` (unique together)
- `requests_per_minute`, `tokens_per_minute`

**model_pricing**
- `provider`, `model_pattern` — e.g., `"anthropic"`, `"claude-opus-4"`
- `input_price_micro`, `output_price_micro` — prices per 1M tokens

## Security Considerations

1. **Token Security**: Proxy tokens are 64-character hex strings (256 bits of entropy). Store them encrypted at rest in the control plane database.

2. **Network Isolation**: The proxy should be on a private network accessible only to the control plane and agent instances. Never expose the proxy's port 8080 to the public internet.

3. **Admin Secret**: Use a strong, random admin secret. Rotate it periodically. If compromised, an attacker can:
   - View all usage data
   - Modify budget/rate limits
   - Disable instance tokens

4. **API Keys**: The proxy stores real API keys in plaintext (the control plane encrypts them before sending). Ensure the proxy's SQLite database volume has appropriate permissions.

## Monitoring

### Health Check

```http
GET /health
{"status":"healthy"}
```

### Logs

The proxy logs:
- Token validation failures
- Budget/rate limit rejections
- Usage recording errors
- Upstream request failures

Example:
```
2026-02-19T12:34:56Z Upstream request to anthropic failed: context deadline exceeded
2026-02-19T12:35:10Z Failed to record usage: database is locked
```

## Troubleshooting

### Instance getting 401 Unauthorized

- Check that the instance's proxy token is registered: `docker exec llm-proxy sqlite3 /app/data/llm-proxy.db "SELECT * FROM instance_tokens WHERE instance_name='bot-example'"`
- Verify the token hasn't been disabled: `enabled` should be `1`
- Check control plane logs for token registration errors

### Instance getting 429 Budget Exceeded

- Check current spend: `GET /admin/usage/instances/{name}`
- Verify budget limit: `GET /admin/limits/{name}`
- If the limit is too low, update it via the dashboard or API

### Missing usage data

- Verify the proxy is receiving requests (check proxy logs)
- Ensure the provider's streaming parser is working (check for token extraction errors in logs)
- Query the database directly: `SELECT * FROM usage_records WHERE instance_name='bot-example' ORDER BY created_at DESC LIMIT 10`

### Cost estimates are $0.00

- Check that the model name matches a pattern in `model_pricing` table
- Add custom pricing: `INSERT INTO model_pricing (provider, model_pattern, input_price_micro, output_price_micro) VALUES ('openai', 'custom-model', 1000000, 2000000);`

## Disabling the Proxy

To disable the proxy after it's been enabled:

1. **Docker**: Set `CLAWORC_PROXY_ENABLED=false` and restart the control plane
2. **Kubernetes**: Set `proxy.enabled: false` in values.yaml and upgrade the Helm release

Existing instances will need to be restarted to reconfigure them with direct API keys (not proxy tokens).

## Testing

The LLM proxy includes comprehensive tests covering all functionality.

### Running Tests

From the repository root:

```bash
./test-all.sh
```

From the `llm-proxy/` directory:

```bash
# All tests
go test ./...

# Specific package
go test ./internal/providers -v
go test ./internal/proxy -v

# With coverage
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Test Coverage

**Provider Parsers** (`internal/providers/*_test.go`)
- Anthropic SSE: message_start, message_delta events
- OpenAI SSE: usage chunks (9 compatible providers)
- Gemini JSON: usageMetadata parsing
- Cohere SSE: meta.tokens extraction
- Registry: provider lookup, auth headers, custom upstreams

**Authentication** (`internal/proxy/auth_test.go`)
- Token extraction from headers (Bearer, x-api-key, x-goog-api-key)
- Token validation (enabled, disabled, unknown)
- Token caching (30s TTL)
- Middleware integration

**Streaming** (`internal/proxy/streaming_test.go`)
- SSE stream passthrough
- Token extraction from streams
- Multi-event aggregation
- Non-streaming response parsing

**Database** (`internal/database/database_test.go`)
- Migrations and table creation
- Model pricing seeding
- CRUD operations
- Unique constraints

**Integration** (`integration_test.go`)
- Token registration lifecycle
- Admin auth enforcement
- Budget enforcement (hard limits)
- Rate limiting
- Usage recording
- Limits persistence

**Control Plane Client** (`control-plane/internal/llmproxy/client_test.go`)
- All management API calls
- Environment variable mappings
- Error handling

### End-to-End Test

Shell script that starts the proxy and tests the full lifecycle:

```bash
cd llm-proxy
./test/e2e_test.sh
```

Tests:
1. Proxy health check
2. Token registration
3. API key synchronization
4. Usage queries
5. Budget limits
6. Token disable/enable
7. Token revocation
8. Admin auth

See `llm-proxy/test/README.md` for detailed test documentation.

### Manual Testing

To test with real LLM providers:

```bash
# Start proxy
cd llm-proxy
export LLM_PROXY_ADMIN_SECRET="test-secret"
go run .

# Register token
curl -X POST http://localhost:8080/admin/tokens \
  -H "Authorization: Bearer test-secret" \
  -d '{"instance_name":"bot-test","token":"test-token"}'

# Sync real API key
curl -X PUT http://localhost:8080/admin/keys \
  -H "Authorization: Bearer test-secret" \
  -d '{"keys":[{"provider":"anthropic","scope":"global","key":"YOUR_KEY"}]}'

# Make proxied request
curl -X POST http://localhost:8080/v1/anthropic/v1/messages \
  -H "x-api-key: test-token" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-3-5-sonnet-20241022","max_tokens":50,"messages":[{"role":"user","content":"Hi"}]}'

# Check usage
curl http://localhost:8080/admin/usage/instances/bot-test \
  -H "Authorization: Bearer test-secret"
```
