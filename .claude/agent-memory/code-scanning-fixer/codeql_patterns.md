---
name: CodeQL Patterns in Claworc
description: Recurring CodeQL rules, root causes, verified fixes, and false-positive patterns in this repo
type: reference
---

## go/log-injection (Medium) — CWE-117

**Where it fires:** `internal/orchestrator/docker.go` — `ensureImage()` logs the `img` parameter (user-provided container image name) directly.

**Root cause:** User-supplied string logged without sanitization; newlines could inject fake log lines.

**Fix:** Wrap with `utils.SanitizeForLog(img)` in the log call only. The raw `img` value is still passed to Docker API calls (correct behavior).

**Existing helper:** `internal/utils/sanitize.go` → `utils.SanitizeForLog()`. Also aliased as `safeLog()` in `internal/llmgateway/gateway.go`.

---

## go/reflected-xss (High) — CWE-79, CWE-116

**Where it fires:** `internal/llmgateway/gateway.go:42` (`flushingWriter.Write`)

**CodeQL source:** `internal/sshterminal/session_manager.go:335` (`ms.terminal.Stdout.Read`)

**False positive aspect:** The sshterminal `Attach()` method takes an `io.Writer` and writes SSH terminal output to a WebSocket writer (`wsOutputWriter`), not to the gateway's `flushingWriter`. The two code paths are independent. CodeQL infers a common `io.Writer` interface path that doesn't exist at runtime.

**Real risk addressed anyway:** The gateway copied all upstream response headers including `Content-Type`. A compromised/malicious LLM provider could return `Content-Type: text/html`, causing browsers to render the response as HTML (XSS via proxy). Fix: enforce an allowlist — only `application/json` or `text/event-stream` are forwarded as Content-Type. Also added filtering of `Set-Cookie` and `X-Content-Type-Options` from upstream headers (set explicitly with safe values).

**Fix location:** `handleProxy()` in `gateway.go`, around the header-copying loop.

---

## Build notes

- `go build ./...` from repo root or `control-plane/` fails due to stray `control-plane/pkg/mod/` directory in the repo tree (a local Go module cache).
- Workaround: use explicit package paths: `go build github.com/gluk-w/claworc/control-plane/internal/...` or pass `GOPATH=/tmp/go_build_temp` to redirect the cache.
- `go vet ./...` hits same issue. Use full module paths instead.
- Pre-existing vet issues (not introduced by security fixes): `internal/sshproxy/health_test.go:210` (mutex copy) and `internal/handlers/ssh_test.go:128` (IPv6 format).
- Tests for modified packages: `go test github.com/gluk-w/claworc/control-plane/internal/llmgateway/... github.com/gluk-w/claworc/control-plane/internal/orchestrator/... github.com/gluk-w/claworc/control-plane/internal/utils/...`
