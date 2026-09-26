package handlers

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
)

const webhookSessionPrefix = "claworc-webhook-"

// webhookOpenSession opens the agent chat session RunWebhookBridge streams
// from. Replaced in tests to inject a fake session.
var webhookOpenSession = func(ctx context.Context, instanceID uint, sessionKey string) (agentshim.Session, error) {
	client, err := agentshim.DefaultFactory().ForInstance(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	return client.OpenSession(ctx, sessionKey)
}

// WebhookAttachment is a single file delivered alongside a webhook
// request. The bridge writes Content into the instance at
// /tmp/webhooks/<session>/<Filename> before sending the chat message.
type WebhookAttachment struct {
	Filename string
	Content  []byte
}

// RunWebhookBridge opens an agent chat session for the given instance,
// uploads any attachments into /tmp/webhooks/<sessionName>/, sends a single
// message using the claworc-webhook-<sessionName> key (so webhook sessions
// are identifiable in the agent's session list), and reads normalized chat
// events until the "end" event arrives. Returns the final cumulative
// assistant text.
//
// This synchronously blocks the HTTP caller. The supplied ctx is the HTTP
// request context — its cancellation (client disconnect) or deadline
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

	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	sess, err := webhookOpenSession(dialCtx, instanceID, webhookSessionPrefix+sessionName)
	cancel()
	if err != nil {
		return "", fmt.Errorf("dial gateway: %w", err)
	}
	defer sess.Close()

	if err := sess.Send(ctx, finalMessage); err != nil {
		return "", fmt.Errorf("send chat message: %w", err)
	}

	// Idle (activity-based) deadline: each read is bounded by idle, and the
	// timer re-arms on every event received. An agent that keeps streaming
	// events is never cut off; only a genuine stall (no events for idle) trips.
	idle := config.Cfg.WebhookIdleTimeout
	if idle <= 0 {
		idle = 120 * time.Second
	}

	var assistantText string
	for {
		readCtx, cancel := context.WithTimeout(ctx, idle)
		ev, err := sess.Recv(readCtx)
		cancel()
		if err != nil {
			// Parent ctx cancelled => client disconnected or its own deadline.
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			// Per-read deadline fired => the agent produced no events for idle.
			if readCtx.Err() == context.DeadlineExceeded {
				return "", fmt.Errorf("agent idle timeout: no events for %s", idle)
			}
			return "", fmt.Errorf("gateway read: %w", err)
		}
		switch ev.Kind {
		case agentshim.EventAssistant:
			// Assistant events carry a cumulative snapshot; the latest
			// snapshot is the final reply.
			if ev.Text != "" {
				assistantText = ev.Text
			}
		case agentshim.EventEnd:
			if ev.Text != "" {
				assistantText = ev.Text
			}
			log.Printf("[webhook-bridge] instance=%d session=%s done bytes=%d", instanceID, utils.SanitizeForLog(sessionName), len(assistantText))
			return assistantText, nil
		}
	}
}
