package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/channelhealth"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

func configureAlertWebhook(t *testing.T, url, token string) {
	t.Helper()
	if err := database.SetSetting(settingChannelAlertWebhookURL, url); err != nil {
		t.Fatalf("set url: %v", err)
	}
	if token != "" {
		enc, err := utils.Encrypt(token)
		if err != nil {
			t.Fatalf("encrypt token: %v", err)
		}
		if err := database.SetSetting(settingChannelAlertWebhookToken, enc); err != nil {
			t.Fatalf("set token: %v", err)
		}
	}
}

func TestSendChannelAlert_DeliversPayloadWithBearer(t *testing.T) {
	setupHandlersTestDB(t)

	var gotBody []byte
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	configureAlertWebhook(t, srv.URL, "sekret")

	p := buildChannelAlertPayload(channelhealth.Snapshot{
		InstanceID: 42,
		Overall:    channelhealth.OverallUnhealthy,
		Channels: []channelhealth.ChannelState{
			{Channel: "slack", AccountID: "default", Status: channelhealth.StatusDisconnected, LastError: "socket closed"},
			{Channel: "telegram", AccountID: "default", Status: channelhealth.StatusHealthy},
		},
	}, eventFailureDetected, map[string]any{"consecutive_failures": 3})

	if got := sendChannelAlert(p); got != webhookStatusSent {
		t.Fatalf("expected sent, got %q", got)
	}
	if gotAuth != "Bearer sekret" {
		t.Fatalf("expected bearer header, got %q", gotAuth)
	}

	var decoded ChannelAlertPayload
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if decoded.Event != "channel_failure" || decoded.Text == "" {
		t.Fatalf("unexpected payload: %+v", decoded)
	}
	if len(decoded.Channels) != 1 || decoded.Channels[0].Channel != "slack" {
		t.Fatalf("expected only non-healthy channels, got %+v", decoded.Channels)
	}
	if decoded.ConsecutiveFailures != 3 {
		t.Fatalf("expected consecutive_failures=3, got %d", decoded.ConsecutiveFailures)
	}
}

func TestSendChannelAlert_RetriesOn5xx(t *testing.T) {
	setupHandlersTestDB(t)

	old := channelAlertRetryDelay
	channelAlertRetryDelay = time.Millisecond
	t.Cleanup(func() { channelAlertRetryDelay = old })

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	configureAlertWebhook(t, srv.URL, "")

	if got := sendChannelAlert(ChannelAlertPayload{Event: "test"}); got != webhookStatusSent {
		t.Fatalf("expected sent after retry, got %q", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 attempts, got %d", calls.Load())
	}
}

func TestSendChannelAlert_FailsAfterRetry(t *testing.T) {
	setupHandlersTestDB(t)

	old := channelAlertRetryDelay
	channelAlertRetryDelay = time.Millisecond
	t.Cleanup(func() { channelAlertRetryDelay = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	configureAlertWebhook(t, srv.URL, "")

	if got := sendChannelAlert(ChannelAlertPayload{Event: "test"}); got != webhookStatusFailed {
		t.Fatalf("expected failed, got %q", got)
	}
}

func TestSendChannelAlert_SkippedWhenUnconfigured(t *testing.T) {
	setupHandlersTestDB(t)
	if got := sendChannelAlert(ChannelAlertPayload{Event: "test"}); got != webhookStatusSkipped {
		t.Fatalf("expected skipped without URL, got %q", got)
	}
}

func TestSendChannelAlert_SkippedWhenDisabled(t *testing.T) {
	setupHandlersTestDB(t)
	configureAlertWebhook(t, "http://127.0.0.1:1/never", "")
	if err := database.SetSetting(settingChannelAlertsEnabled, "false"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
	if got := sendChannelAlert(ChannelAlertPayload{Event: "test"}); got != webhookStatusSkipped {
		t.Fatalf("expected skipped when disabled, got %q", got)
	}
}
