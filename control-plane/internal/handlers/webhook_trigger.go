package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"github.com/go-chi/chi/v5"
)

// sessionNamePattern restricts session_name to Latin letters, digits,
// dashes, underscores, and dots so it is safe to use as the OpenClaw
// sessionKey, log column value, and attachment path component without
// escaping.
var sessionNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// runWebhookBridge is the call-out into the OpenClaw gateway bridge. It is
// a package-level var so unit tests can stub the SSH+gateway round-trip.
var runWebhookBridge = RunWebhookBridge

// contextWithChi returns a context that carries the supplied chi route
// context — used by PrivateWebhookTrigger to thread a manually-parsed URL
// parameter through chi.URLParam in shared code.
func contextWithChi(r *http.Request, rctx *chi.Context) context.Context {
	return context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
}

// matchWebhookKey looks for a WebhookApiKey row whose decrypted token
// equals presented, scoped to instanceID. Returns (matched key, ok).
//
// Comparison uses subtle.ConstantTimeCompare. Per-instance scope keeps
// the candidate set small in practice (a handful of keys per agent at
// most), so a linear scan is fine. If this ever becomes hot, add an
// in-memory cache keyed by ciphertext.
func matchWebhookKey(instanceID uint, presented string) (database.WebhookApiKey, bool) {
	if presented == "" {
		return database.WebhookApiKey{}, false
	}
	var keys []database.WebhookApiKey
	if err := database.DB.Where("instance_id = ?", instanceID).Find(&keys).Error; err != nil {
		return database.WebhookApiKey{}, false
	}
	for _, k := range keys {
		raw, err := utils.Decrypt(k.Key)
		if err != nil || raw == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(raw), []byte(presented)) == 1 {
			return k, true
		}
	}
	return database.WebhookApiKey{}, false
}

func extractBearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

func sourceIPOf(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// first hop wins
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}

func keyLast4(s string) string {
	if len(s) <= 4 {
		return s
	}
	return s[len(s)-4:]
}

type webhookCallRequest struct {
	SessionName string
	Message     string
	Attachments []WebhookAttachment
}

func parseWebhookCall(r *http.Request) (webhookCallRequest, int64, error) {
	var out webhookCallRequest

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		// 32 MiB cap on memory; larger uploads spill to /tmp (Go default).
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			return out, 0, err
		}
		out.SessionName = r.FormValue("session_name")
		out.Message = r.FormValue("message")
		var totalBytes int64
		if r.MultipartForm != nil {
			for _, headers := range r.MultipartForm.File {
				for _, fh := range headers {
					f, err := fh.Open()
					if err != nil {
						return out, 0, err
					}
					data, err := io.ReadAll(f)
					f.Close()
					if err != nil {
						return out, 0, err
					}
					totalBytes += int64(len(data))
					out.Attachments = append(out.Attachments, WebhookAttachment{Filename: fh.Filename, Content: data})
				}
			}
		}
		return out, totalBytes, nil
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		return out, 0, err
	}
	var parsed struct {
		SessionName string `json:"session_name"`
		Message     string `json:"message"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &parsed); err != nil {
			return out, int64(len(body)), err
		}
	}
	out.SessionName = parsed.SessionName
	out.Message = parsed.Message
	return out, int64(len(body)), nil
}

// PublicWebhookTrigger handles POST /webhooks/{instance-uuid} on the
// control plane. Outside the session auth middleware; authenticated by a
// webhook API key that has IsPrivate=false.
func PublicWebhookTrigger(w http.ResponseWriter, r *http.Request) {
	runWebhookRequest(w, r, false)
}

// PrivateWebhookTrigger handles POST /webhooks/{instance-uuid} on the LLM
// gateway. Authenticated by a webhook API key that has IsPrivate=true.
// The gateway is reachable only from inside instances (via the SSH agent-
// listener tunnel), so this route is the inter-agent webhook surface.
//
// The gateway mux is net/http (not chi), so the URL parameter is parsed
// out of the path here rather than via chi.URLParam.
func PrivateWebhookTrigger(w http.ResponseWriter, r *http.Request) {
	const prefix = "/webhooks/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	uuid := strings.TrimPrefix(r.URL.Path, prefix)
	if i := strings.IndexByte(uuid, '/'); i >= 0 {
		uuid = uuid[:i]
	}
	if uuid == "" {
		http.NotFound(w, r)
		return
	}
	// Stash the parsed UUID for runWebhookRequest by re-using chi's
	// context, which it reads via chi.URLParam.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("uuid", uuid)
	r = r.WithContext(contextWithChi(r, rctx))
	runWebhookRequest(w, r, true)
}

func runWebhookRequest(w http.ResponseWriter, r *http.Request, isPrivate bool) {
	start := time.Now()
	instUUID := chi.URLParam(r, "uuid")
	if instUUID == "" {
		http.NotFound(w, r)
		return
	}

	var inst database.Instance
	if err := database.DB.Where("uuid = ?", instUUID).First(&inst).Error; err != nil {
		http.NotFound(w, r)
		return
	}

	logRow := database.WebhookLog{
		InstanceID: inst.ID,
		SourceIP:   sourceIPOf(r),
		IsPrivate:  isPrivate,
	}
	defer func() {
		logRow.DurationMs = int(time.Since(start).Milliseconds())
		_ = database.DB.Create(&logRow).Error
	}()

	// Authenticate: presented bearer must match a key on this instance with
	// IsPrivate matching the endpoint.
	presented := extractBearer(r)
	key, ok := matchWebhookKey(inst.ID, presented)
	if !ok || key.IsPrivate != isPrivate {
		// Treat as 404 when there are no eligible keys at all (so callers
		// can't probe whether an instance UUID exists); 401 otherwise.
		var count int64
		database.DB.Model(&database.WebhookApiKey{}).
			Where("instance_id = ? AND is_private = ?", inst.ID, isPrivate).
			Count(&count)
		if count == 0 {
			logRow.StatusCode = http.StatusNotFound
			logRow.ErrorMessage = "no keys configured for endpoint"
			http.NotFound(w, r)
			return
		}
		logRow.StatusCode = http.StatusUnauthorized
		logRow.ErrorMessage = "invalid api key"
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	logRow.KeyLast4 = keyLast4(presented)

	call, reqBytes, err := parseWebhookCall(r)
	logRow.RequestBytes = int(reqBytes)
	if err != nil {
		logRow.StatusCode = http.StatusBadRequest
		logRow.ErrorMessage = err.Error()
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if call.SessionName == "" {
		logRow.StatusCode = http.StatusBadRequest
		logRow.ErrorMessage = "session_name required"
		http.Error(w, "session_name is required", http.StatusBadRequest)
		return
	}
	if !sessionNamePattern.MatchString(call.SessionName) {
		logRow.StatusCode = http.StatusBadRequest
		logRow.ErrorMessage = "invalid session_name"
		http.Error(w, "session_name may contain only letters, digits, dashes, underscores, and dots", http.StatusBadRequest)
		return
	}
	logRow.SessionName = call.SessionName

	// Update LastUsedAt for the matched key (best-effort).
	now := time.Now().UTC()
	database.DB.Model(&database.WebhookApiKey{}).Where("id = ?", key.ID).
		Update("last_used_at", &now)

	reply, err := runWebhookBridge(r.Context(), inst.ID, call.SessionName, call.Message, call.Attachments)
	if err != nil {
		logRow.StatusCode = http.StatusBadGateway
		logRow.ErrorMessage = err.Error()
		log.Printf("[webhook] instance=%d session=%s bridge error: %s", inst.ID, utils.SanitizeForLog(call.SessionName), utils.SanitizeForLog(err.Error()))
		http.Error(w, "agent error: "+err.Error(), http.StatusBadGateway)
		return
	}

	logRow.ResponseBytes = len(reply)
	logRow.StatusCode = http.StatusOK
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(reply))
}
