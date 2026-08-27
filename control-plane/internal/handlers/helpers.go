package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// parseIntQuery reads an integer query parameter from r and clamps it to
// [min, max]. If the parameter is missing or unparseable, def is returned.
func parseIntQuery(r *http.Request, key string, def, min, max int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// logSafe renders an externally-supplied value for logging. A value carrying a
// newline would otherwise let its author forge an extra log entry, so CR/LF are
// escaped rather than emitted, and the result is capped so one large input
// cannot flood the log.
func logSafe(s string) string {
	const maxLen = 200
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\n", "\\n")
	if r := []rune(s); len(r) > maxLen {
		// Cut on a rune boundary so the log never shows a mangled character.
		return string(r[:maxLen]) + "…"
	}
	return s
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

// WriteOrchestratorUnavailable writes a uniform 503 response when no container
// backend is initialized. The body includes diagnostics derived from
// orchestrator.Status() so the client can show an actionable message.
func WriteOrchestratorUnavailable(w http.ResponseWriter) {
	s := orchestrator.Status()
	body := map[string]any{
		"error":   "orchestrator_unavailable",
		"detail":  "No container backend is currently available.",
		"backend": s.Backend,
		"status":  s,
	}
	if len(s.Attempts) > 0 {
		last := s.Attempts[len(s.Attempts)-1]
		body["reason"] = last.Reason
		body["message"] = last.Message
	}
	writeJSON(w, http.StatusServiceUnavailable, body)
}
