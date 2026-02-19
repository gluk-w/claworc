# LLM Proxy

Reverse proxy for LLM API requests with usage tracking, budget enforcement, and rate limiting.

## Quick Start

```bash
# Build
go build -o llm-proxy .

# Run
export LLM_PROXY_ADMIN_SECRET="your-secret-here"
./llm-proxy
```

The proxy listens on port 8080 by default.

## Configuration

Environment variables with `LLM_PROXY_` prefix:

| Variable | Default | Description |
|----------|---------|-------------|
| `LLM_PROXY_DATABASE_PATH` | `/app/data/llm-proxy.db` | SQLite database path |
| `LLM_PROXY_ADMIN_SECRET` | (required) | Shared secret for management API |
| `LLM_PROXY_LISTEN_ADDR` | `:8080` | HTTP listen address |
| `LLM_PROXY_OLLAMA_URL` | (optional) | Override Ollama upstream URL |
| `LLM_PROXY_LLAMACPP_URL` | (optional) | Override llama.cpp upstream URL |

## Docker Build

```bash
docker build -t llm-proxy:latest .
docker run -p 8080:8080 \
  -e LLM_PROXY_ADMIN_SECRET=secret \
  -v proxy-data:/app/data \
  llm-proxy:latest
```

## Routes

### Agent-Facing (LLM Traffic)

```
/v1/{provider}/*
```

All LLM API requests from agents go through here. The `{provider}` slug determines which upstream provider to proxy to.

**Auth**: Instance proxy token (via `Authorization: Bearer`, `x-api-key`, or `x-goog-api-key`)

Examples:
```bash
# Anthropic
curl http://llm-proxy:8080/v1/anthropic/v1/messages \
  -H "x-api-key: <proxy-token>" \
  -H "anthropic-version: 2023-06-01" \
  -d '{"model":"claude-3-5-sonnet-20241022","messages":[...],"stream":true}'

# OpenAI
curl http://llm-proxy:8080/v1/openai/v1/chat/completions \
  -H "Authorization: Bearer <proxy-token>" \
  -d '{"model":"gpt-4o","messages":[...],"stream":true}'
```

### Management API

All endpoints require `Authorization: Bearer <admin-secret>`.

#### Register Instance Token

```http
POST /admin/tokens
{"instance_name": "bot-example", "token": "abc123..."}
```

Response: `201 Created` or `200 OK` (if updating existing)

#### Revoke Token

```http
DELETE /admin/tokens/{name}
```

Response: `204 No Content`

#### Disable/Enable Token

```http
PUT /admin/tokens/{name}/disable
PUT /admin/tokens/{name}/enable
```

Response: `200 OK {"status":"disabled"}` or `{"status":"enabled"}`

#### Sync API Keys

```http
PUT /admin/keys
{
  "keys": [
    {"provider": "anthropic", "scope": "global", "key": "sk-ant-..."},
    {"provider": "openai", "scope": "bot-example", "key": "sk-..."}
  ]
}
```

Response: `200 OK {"status":"synced"}`

#### Query Usage

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

#### Get/Set Limits

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

## Architecture

```
internal/
├── api/
│   ├── handlers.go     # Management API endpoints
│   └── middleware.go   # Admin auth
├── config/
│   └── config.go       # Environment variable loading
├── database/
│   ├── models.go       # GORM models
│   └── database.go     # SQLite init, migrations, pricing seed
├── providers/
│   ├── registry.go     # Provider definitions (15 providers)
│   ├── anthropic.go    # Anthropic SSE parser
│   ├── openai.go       # OpenAI SSE parser
│   ├── google.go       # Gemini JSON parser
│   └── cohere.go       # Cohere SSE parser
└── proxy/
    ├── auth.go         # Token validation (cached 30s)
    ├── budget.go       # Budget enforcement (cached 10s)
    ├── ratelimit.go    # Sliding window rate limiter
    ├── streaming.go    # SSE parser orchestration
    ├── handler.go      # Reverse proxy logic
    └── context.go      # Request context helpers
```

## Token Extraction

The proxy parses SSE/NDJSON streams to extract token usage:

### Anthropic
- `message_start` event → `usage.input_tokens`
- `message_delta` event → `usage.output_tokens`

### OpenAI-compatible (OpenAI, Groq, DeepSeek, etc.)
- Final chunk → `usage.prompt_tokens`, `usage.completion_tokens`

### Gemini
- Each chunk → `usageMetadata.promptTokenCount`, `usageMetadata.candidatesTokenCount`

### Cohere
- Final event → `response.meta.tokens.input_tokens`, `response.meta.tokens.output_tokens`

Non-streaming responses are parsed from the full JSON body.

## Development

### Running Tests

```bash
go test ./...
```

### Adding a New Provider

1. Add to `internal/providers/registry.go`:
   ```go
   "newprovider": {
       Name:        "newprovider",
       UpstreamURL: "https://api.newprovider.com",
       AuthStyle:   AuthBearer,
       ParserType:  "openai", // or "anthropic", "gemini", "cohere"
   },
   ```

2. If the provider has a unique streaming format, add a parser to `internal/providers/newprovider.go`

3. Add pricing to `internal/database/database.go` seed function

4. Add env var mappings to `control-plane/internal/llmproxy/client.go`

## Performance

- **Token cache**: 30s TTL, prevents DB lookup on every request
- **Budget cache**: 10s TTL, sum is cached per instance
- **Rate limit windows**: In-memory, cleaned up after 60 seconds
- **Streaming**: Zero-copy SSE passthrough with line-by-line parsing

Benchmarks (single replica, no caching):
- Token validation: ~50µs (cached) / ~2ms (uncached)
- Budget check: ~100µs (cached) / ~5ms (uncached)
- Proxy overhead: ~200µs for streaming requests
