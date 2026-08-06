package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/channelhealth"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

// Settings keys for the channel alert webhook. The URL is a plain setting;
// the bearer token is encrypted at rest like brave_api_key.
const (
	settingChannelAlertsEnabled     = "channel_alerts_enabled"
	settingChannelAlertWebhookURL   = "channel_alert_webhook_url"
	settingChannelAlertWebhookToken = "channel_alert_webhook_token"
)

// Webhook delivery outcomes recorded on ChannelHealthEvent rows.
const (
	webhookStatusSent    = "sent"
	webhookStatusFailed  = "failed"
	webhookStatusSkipped = "skipped"
)

var channelAlertClient = &http.Client{Timeout: 10 * time.Second}

// channelAlertRetryDelay is overridable in tests.
var channelAlertRetryDelay = 5 * time.Second

// AlertInstance identifies the instance an alert is about.
type AlertInstance struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// AlertChannel is one non-healthy channel included in an alert.
type AlertChannel struct {
	Channel   string `json:"channel"`
	AccountID string `json:"account_id"`
	Status    string `json:"status"`
	LastError string `json:"last_error,omitempty"`
}

// ChannelAlertPayload is the JSON body POSTed to the configured channel
// alert webhook. The Text field is a self-contained human-readable summary
// so bare Slack/Discord incoming-webhook style receivers are useful as-is.
type ChannelAlertPayload struct {
	Event               string         `json:"event"` // channel_failure|auto_restart|restart_limit_reached|recovery|test
	Text                string         `json:"text"`
	Timestamp           time.Time      `json:"timestamp"`
	Instance            AlertInstance  `json:"instance"`
	Overall             string         `json:"overall,omitempty"`
	ConsecutiveFailures int            `json:"consecutive_failures,omitempty"`
	FailingSince        *time.Time     `json:"failing_since,omitempty"`
	DurationSeconds     int            `json:"duration_seconds,omitempty"`
	Channels            []AlertChannel `json:"channels,omitempty"`
}

// buildChannelAlertPayload assembles the webhook payload for an escalation
// event from the health snapshot that triggered it.
func buildChannelAlertPayload(snap channelhealth.Snapshot, eventType string, extra map[string]any) ChannelAlertPayload {
	var inst database.Instance
	_ = database.DB.First(&inst, snap.InstanceID).Error

	p := ChannelAlertPayload{
		Timestamp: time.Now().UTC(),
		Overall:   snap.Overall,
		Instance: AlertInstance{
			ID:          snap.InstanceID,
			Name:        inst.Name,
			DisplayName: inst.DisplayName,
		},
	}
	for _, ch := range snap.Channels {
		if ch.Status == channelhealth.StatusHealthy || ch.Status == channelhealth.StatusDisabled {
			continue
		}
		p.Channels = append(p.Channels, AlertChannel{
			Channel:   ch.Channel,
			AccountID: ch.AccountID,
			Status:    ch.Status,
			LastError: ch.LastError,
		})
	}
	if v, ok := extra["consecutive_failures"].(int); ok {
		p.ConsecutiveFailures = v
	}
	if v, ok := extra["duration_seconds"].(int); ok {
		p.DurationSeconds = v
	}
	if v, ok := extra["failing_since"].(time.Time); ok && !v.IsZero() {
		t := v
		p.FailingSince = &t
	}

	name := inst.DisplayName
	if name == "" {
		name = fmt.Sprintf("#%d", snap.InstanceID)
	}
	chansText := ""
	for i, ch := range p.Channels {
		if i > 0 {
			chansText += ", "
		}
		chansText += fmt.Sprintf("%s/%s: %s", ch.Channel, ch.AccountID, ch.Status)
	}
	if chansText != "" {
		chansText = " (" + chansText + ")"
	}

	switch eventType {
	case eventFailureDetected:
		p.Event = "channel_failure"
		p.Text = fmt.Sprintf("Claworc: agent %q channels %s for %d consecutive checks%s",
			name, snap.Overall, p.ConsecutiveFailures, chansText)
	case eventAutoRestart:
		p.Event = "auto_restart"
		p.Text = fmt.Sprintf("Claworc: auto-restarting agent %q — channels %s for %d consecutive checks%s",
			name, snap.Overall, p.ConsecutiveFailures, chansText)
	case eventRestartLimitReached:
		p.Event = "restart_limit_reached"
		p.Text = fmt.Sprintf("Claworc: agent %q still %s but the auto-restart limit was reached; manual intervention needed%s",
			name, snap.Overall, chansText)
	case eventRecovered:
		p.Event = "recovery"
		p.Text = fmt.Sprintf("Claworc: agent %q channel health recovered after %s",
			name, (time.Duration(p.DurationSeconds) * time.Second).String())
	default:
		p.Event = eventType
		p.Text = fmt.Sprintf("Claworc: agent %q channel health event %q", name, eventType)
	}
	return p
}

// channelAlertConfig reads the alert delivery settings. Returns ok=false
// when alerts are disabled or no URL is configured.
func channelAlertConfig() (url, token string, ok bool) {
	if enabled, err := database.GetSetting(settingChannelAlertsEnabled); err == nil && enabled == "false" {
		return "", "", false
	}
	url, err := database.GetSetting(settingChannelAlertWebhookURL)
	if err != nil || url == "" {
		return "", "", false
	}
	if enc, err := database.GetSetting(settingChannelAlertWebhookToken); err == nil && enc != "" {
		if tok, err := utils.Decrypt(enc); err == nil {
			token = tok
		}
	}
	return url, token, true
}

// sendChannelAlert delivers the payload to the configured webhook with one
// retry on network error or 5xx. Returns the delivery outcome for the
// audit row. Callers must not invoke this on the monitor goroutine.
func sendChannelAlert(p ChannelAlertPayload) string {
	url, token, ok := channelAlertConfig()
	if !ok {
		return webhookStatusSkipped
	}
	status, _, err := postChannelAlert(url, token, p)
	if err != nil || status >= 500 {
		time.Sleep(channelAlertRetryDelay)
		status, _, err = postChannelAlert(url, token, p)
	}
	if err != nil {
		log.Printf("[channelalert] delivery failed: %v", err)
		return webhookStatusFailed
	}
	if status >= 300 {
		log.Printf("[channelalert] delivery failed: HTTP %d", status)
		return webhookStatusFailed
	}
	return webhookStatusSent
}

func postChannelAlert(url, token string, p ChannelAlertPayload) (int, string, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return 0, "", err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := channelAlertClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	return resp.StatusCode, resp.Status, nil
}

// TestChannelAlertWebhook sends a synchronous test alert to the configured
// webhook so admins can verify delivery from the Settings page.
// POST /api/v1/settings/channel-alerts/test (admin only).
func TestChannelAlertWebhook(w http.ResponseWriter, r *http.Request) {
	url, token, ok := channelAlertConfig()
	if !ok {
		writeError(w, http.StatusBadRequest, "Channel alerts are disabled or no webhook URL is configured")
		return
	}
	p := ChannelAlertPayload{
		Event:     "test",
		Text:      "Claworc: test alert — channel alert webhook is configured correctly",
		Timestamp: time.Now().UTC(),
	}
	status, statusText, err := postChannelAlert(url, token, p)

	ev := database.ChannelHealthEvent{
		Type:          "webhook_test",
		WebhookStatus: webhookStatusSent,
	}
	if err != nil || status >= 300 {
		ev.WebhookStatus = webhookStatusFailed
	}
	if dbErr := database.DB.Create(&ev).Error; dbErr != nil {
		log.Printf("[channelalert] record test event: %v", dbErr)
	}

	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"status": "failed",
			"error":  err.Error(),
		})
		return
	}
	if status >= 300 {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"status":      "failed",
			"http_status": status,
			"error":       statusText,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "sent",
		"http_status": status,
	})
}
