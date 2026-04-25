---
name: Verified Build and Test Commands
description: Working build/test commands for Claworc components, with known workarounds
type: reference
---

## Go backend (control-plane/)

**Problem:** `go build ./...` and `go vet ./...` from `control-plane/` fail because `control-plane/pkg/mod/` (a stray local Go module cache) is inside the module tree.

**Working commands (verified 2026-04-24):**

Build the main binary:
```
cd control-plane && go build .
```

Vet the main package:
```
cd control-plane && go vet .
```

Test all internal packages (works despite the `./...` issue):
```
cd control-plane && go test ./internal/...
```

Old workaround (also works if needed):
```
cd control-plane && GOPATH=/tmp/go_build_temp go test github.com/gluk-w/claworc/control-plane/internal/... -timeout 60s
```

## Frontend (control-plane/frontend/)

`npx vite build` — skip `tsc -b` as it fails on pre-existing errors (per MEMORY.md).
