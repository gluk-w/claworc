package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

const webhookSessionPrefix = "claworc-webhook-"

// webhookGetTunnelPort is the getTunnelPort call used by RunWebhookBridge.
// Replaced in tests to inject a local fake-gateway port.
var webhookGetTunnelPort = getTunnelPort

// WebhookAttachment is a single file delivered alongside a webhook
// request. The bridge writes Content into the instance at
// /tmp/webhooks/<session>/<Filename> before sending chat.send.
type WebhookAttachment struct {
	Filename string
	Content  []byte
}

// RunWebhookBridge dials the OpenClaw gateway for the given instance,
// uploads any attachments into /tmp/webhooks/<sessionName>/, sends a
// single chat.send frame using the claworc-webhook-<sessionName> key
// (so webhook sessions are identifiable in OpenClaw's session list),
// and reads gateway events until the lifecycle/end frame arrives.
// Returns the final cumulative assistant text.
//
// This mirrors the moderator runner's gateway loop (see
// internal/moderator/runner.go) but synchronously blocks the HTTP caller
// instead of streaming into Kanban comments. The supplied ctx is the
// HTTP request context — its cancellation (client disconnect) or deadline
// (client HTTP timeout) terminates the call.
func RunWebhookBridge(ctx context.Context, instanceID uint, sessionName, message string, attachments []WebhookAttachment) (reply string, err error) {
	if sessionName == "" {
		return "", fmt.Errorf("session_name is required")
	}

	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return "", fmt.Errorf("instance not found")
	}

	// Drop attachments into /tmp/webhooks/<session>/<filename>. Build the
	// preamble describing them for the agent.
	var attachmentPaths []string
	for _, a := range attachments {
		safe := filepath.Base(a.Filename)
		if safe == "" || safe == "." || safe == "/" {
			continue
		}
		dst := "/tmp/webhooks/" + sessionName + "/" + safe
		if err := WriteInstanceFile(instanceID, dst, a.Content); err != nil {
			return "", fmt.Errorf("upload %s: %w", a.Filename, err)
		}
		attachmentPaths = append(attachmentPaths, dst)
	}

	finalMessage := message
	if len(attachmentPaths) > 0 {
		var b strings.Builder
		b.WriteString("Attached files:\n")
		for _, p := range attachmentPaths {
			b.WriteString("- ")
			b.WriteString(p)
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(message)
		finalMessage = b.String()
	}

	port, err := webhookGetTunnelPort(instanceID, "gateway")
	if err != nil {
		return "", fmt.Errorf("no gateway tunnel: %w", err)
	}

	var gatewayToken string
	if inst.GatewayToken != "" {
		if tok, derr := utils.Decrypt(inst.GatewayToken); derr == nil && tok != "" {
			gatewayToken = tok
		}
	}

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	gwConn, err := sshproxy.DialGateway(dialCtx, port, gatewayToken)
	cancel()
	if err != nil {
		return "", fmt.Errorf("dial gateway: %w", err)
	}
	defer gwConn.CloseNow()

	ocSessionKey := webhookSessionPrefix + sessionName
	requestID := fmt.Sprintf("webhook-%d", time.Now().UnixNano())
	sendFrame := map[string]any{
		"type":   "req",
		"id":     requestID,
		"method": "chat.send",
		"params": map[string]any{
			"sessionKey":     ocSessionKey,
			"message":        finalMessage,
			"idempotencyKey": ocSessionKey + "-" + requestID,
		},
	}
	sendJSON, err := json.Marshal(sendFrame)
	if err != nil {
		return "", fmt.Errorf("marshal chat.send: %w", err)
	}
	if err := gwConn.Write(ctx, websocket.MessageText, sendJSON); err != nil {
		return "", fmt.Errorf("send chat.send: %w", err)
	}

	var assistantText string
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		_, data, err := gwConn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("gateway read: %w", err)
		}
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg["type"] != "event" {
			continue
		}
		payload, _ := msg["payload"].(map[string]any)
		if payload == nil {
			continue
		}
		stream, _ := payload["stream"].(string)
		eventData, _ := payload["data"].(map[string]any)
		switch stream {
		case "assistant":
			// OpenClaw assistant events carry the cumulative snapshot in
			// data.text. The latest snapshot is the final reply.
			if eventData != nil {
				if text, _ := eventData["text"].(string); text != "" {
					assistantText = text
				}
			}
		case "lifecycle":
			if eventData != nil {
				phase, _ := eventData["phase"].(string)
				if phase == "end" {
					log.Printf("[webhook-bridge] instance=%d session=%s done bytes=%d", instanceID, utils.SanitizeForLog(sessionName), len(assistantText))
					return assistantText, nil
				}
			}
		}
	}
}
