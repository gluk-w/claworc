package moderator

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Run drives a dispatched task to completion: opens an agent chat session
// with a per-run sessionKey, sends the task description, streams events into
// comments, pulls mentioned files as artifacts, and runs the evaluator LLM.
func (s *Service) Run(ctx context.Context, taskID uint) error {
	task, err := s.opts.Store.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("load task: %w", err)
	}
	if task.AssignedInstanceID == nil {
		return fmt.Errorf("task %d has no assigned instance", taskID)
	}
	instanceID := *task.AssignedInstanceID
	instanceName, _ := s.opts.Instances.InstanceName(ctx, instanceID)
	if instanceName == "" {
		instanceName = fmt.Sprintf("#%d", instanceID)
	}
	agentAuthor := "agent:" + instanceName

	taskIDStr := fmt.Sprintf("%d", taskID)

	// Inject prior artifacts and build comment history for the agent prompt.
	artifactDesc := s.injectPriorArtifacts(ctx, taskID, instanceID)
	historyDesc := s.buildCommentHistory(ctx, taskID)

	// Build message with full context.
	var parts []string
	if artifactDesc != "" {
		parts = append(parts, "--- Artifacts from prior run ---\n"+
			"Files from the previous run of this task are at ~/tasks/"+taskIDStr+"/:\n"+artifactDesc)
	}
	if historyDesc != "" {
		parts = append(parts, "--- Prior conversation history ---\n"+historyDesc)
	}
	parts = append(parts, "--- Task ---\n"+task.Description)
	parts = append(parts, "--- Instructions ---\n"+
		"Save all output files to: ~/tasks/"+taskIDStr+"/\n"+
		"Use artifacts from the prior run if they are relevant to your work.")

	message := strings.Join(parts, "\n\n")

	// Append user feedback notes (if any).
	if existing, err := s.opts.Store.ListComments(ctx, taskID); err == nil {
		var userNotes []string
		for _, c := range existing {
			if c.Kind == "user" {
				userNotes = append(userNotes, c.Body)
			}
		}
		if len(userNotes) > 0 {
			message += "\n\n--- User feedback ---\n" + strings.Join(userNotes, "\n\n")
		}
	}

	sessionKey := fmt.Sprintf("kanban-task-%d-%s", task.ID, randomSuffix(6))
	if err := s.opts.Store.UpdateTask(ctx, taskID, map[string]any{
		"status":               "in_progress",
		"open_claw_session_id": sessionKey,
	}); err != nil {
		return fmt.Errorf("mark in_progress: %w", err)
	}

	sess, err := s.opts.Agents.OpenSession(ctx, instanceID, sessionKey)
	if err != nil {
		return fmt.Errorf("open agent session: %w", err)
	}
	defer sess.Close()

	if err := sess.Send(ctx, message); err != nil {
		return fmt.Errorf("send task: %w", err)
	}

	// Insert empty rolling assistant comment.
	assistantID, err := s.opts.Store.InsertComment(ctx, Comment{
		TaskID:            taskID,
		Kind:              "assistant",
		Author:            agentAuthor,
		Body:              "",
		OpenClawSessionID: sessionKey,
	})
	if err != nil {
		return fmt.Errorf("insert assistant comment: %w", err)
	}

	// Assistant events carry CUMULATIVE snapshots per message_id, not
	// deltas: each snapshot replaces its message's text. A turn may contain
	// several messages (text → tool calls → more text); the comment body is
	// all of them in order.
	var (
		msgOrder      []string
		msgText       = map[string]string{}
		assistantText string
	)
	for {
		ev, err := sess.Recv(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ErrStopped
			}
			return fmt.Errorf("recv: %w", err)
		}
		switch ev.Kind {
		case "start":
			if ev.Turn != "" {
				_ = s.opts.Store.UpdateTask(ctx, taskID, map[string]any{"open_claw_run_id": ev.Turn})
			}
		case "assistant":
			if _, seen := msgText[ev.MessageID]; !seen {
				msgOrder = append(msgOrder, ev.MessageID)
			}
			if msgText[ev.MessageID] == ev.Text {
				continue
			}
			msgText[ev.MessageID] = ev.Text
			parts := make([]string, 0, len(msgOrder))
			for _, id := range msgOrder {
				if t := msgText[id]; t != "" {
					parts = append(parts, t)
				}
			}
			assistantText = strings.Join(parts, "\n\n")
			_ = s.opts.Store.SetCommentBody(ctx, assistantID, assistantText)
		case "tool":
			body, _ := json.Marshal(map[string]any{
				"name":   ev.Name,
				"phase":  ev.Phase,
				"detail": json.RawMessage(nonEmptyJSON(ev.Detail)),
			})
			_, _ = s.opts.Store.InsertComment(ctx, Comment{
				TaskID:            taskID,
				Kind:              "tool",
				Author:            agentAuthor,
				Body:              string(body),
				OpenClawSessionID: sessionKey,
			})
		case "error":
			body := ev.Text
			if ev.Code != "" {
				body = ev.Code + ": " + body
			}
			_, _ = s.opts.Store.InsertComment(ctx, Comment{
				TaskID:            taskID,
				Kind:              "error",
				Author:            agentAuthor,
				Body:              body,
				OpenClawSessionID: sessionKey,
			})
		case "end":
			if ev.Text != "" {
				// The end event carries the final text of the last message.
				if assistantText == "" {
					assistantText = ev.Text
					_ = s.opts.Store.SetCommentBody(ctx, assistantID, assistantText)
				}
			}
			if ev.StopReason != "" && ev.StopReason != "complete" {
				_, _ = s.opts.Store.InsertComment(ctx, Comment{
					TaskID: taskID, Kind: "error", Author: "moderator",
					Body: "Agent run ended: " + ev.StopReason,
				})
			}
			goto done
		}
	}
done:

	// Directory-based artifact collection with mention-based fallback.
	pulled, skipped := s.collectOutcomes(ctx, taskID, instanceID, assistantText)
	report := fmt.Sprintf("Pulled %d artifact(s).", len(pulled))
	if len(pulled) > 0 {
		report += "\n\nPulled:\n- " + strings.Join(pulled, "\n- ")
	}
	if len(skipped) > 0 {
		report += "\n\nSkipped:\n- " + strings.Join(skipped, "\n- ")
	}
	_, _ = s.opts.Store.InsertComment(ctx, Comment{
		TaskID: taskID,
		Kind:   "moderator",
		Author: "moderator",
		Body:   report,
	})

	// Clean up task output directory on the instance.
	outcomeDir := fmt.Sprintf("%s/%s", s.opts.Settings.TaskOutcomeDir(), taskIDStr)
	if err := s.opts.Workspace.RemoveAll(ctx, instanceID, outcomeDir); err != nil {
		log.Printf("[moderator] task %d: cleanup ~/tasks/%s failed: %v", taskID, taskIDStr, err)
	}

	// Evaluator LLM pass.
	if err := s.evaluate(ctx, task, assistantText, pulled); err != nil {
		_, _ = s.opts.Store.InsertComment(ctx, Comment{
			TaskID: taskID, Kind: "error", Author: "moderator",
			Body: "Evaluator failed: " + err.Error(),
		})
	}

	return s.opts.Store.UpdateTask(ctx, taskID, map[string]any{"status": "done"})
}

// injectPriorArtifacts uploads artifacts from prior run(s) of this task to
// the instance at ~/tasks/<taskID>/ and returns a description for the prompt.
// Returns empty string on first run (no prior artifacts).
func (s *Service) injectPriorArtifacts(ctx context.Context, taskID uint, instanceID uint) string {
	artifacts, err := s.opts.Store.ListTaskArtifacts(ctx, taskID)
	if err != nil || len(artifacts) == 0 {
		return ""
	}

	taskIDStr := fmt.Sprintf("%d", taskID)
	outcomeDir := s.opts.Settings.TaskOutcomeDir() + "/" + taskIDStr
	maxBytes := s.opts.Settings.ArtifactMaxBytes()

	var injected []string
	var skipped []string

	for _, a := range artifacts {
		data, err := os.ReadFile(a.StoragePath)
		if err != nil {
			skipped = append(skipped, a.Path+" (read error: "+err.Error()+")")
			continue
		}
		if int64(len(data)) > maxBytes {
			skipped = append(skipped, fmt.Sprintf("%s (size %d > max %d)", a.Path, len(data), maxBytes))
			continue
		}
		remotePath := filepath.Join(outcomeDir, a.Path)
		if err := s.opts.Workspace.Write(ctx, instanceID, remotePath, data); err != nil {
			skipped = append(skipped, a.Path+" (upload error: "+err.Error()+")")
			continue
		}
		injected = append(injected, a.Path)
	}

	if len(injected) > 0 {
		body := fmt.Sprintf("Injected %d artifact(s) from prior run.", len(injected))
		body += "\n- " + strings.Join(injected, "\n- ")
		if len(skipped) > 0 {
			body += "\n\nSkipped:\n- " + strings.Join(skipped, "\n- ")
		}
		_, _ = s.opts.Store.InsertComment(ctx, Comment{
			TaskID: taskID, Kind: "moderator", Author: "moderator", Body: body,
		})
	}

	// Build description for the agent prompt.
	var desc strings.Builder
	for _, p := range injected {
		desc.WriteString("- ~/tasks/" + fmt.Sprintf("%d", taskID) + "/" + p + "\n")
	}
	return desc.String()
}

// buildCommentHistory formats the full comment history of this task as a
// readable transcript for inclusion in the agent prompt. Empty on first run.
func (s *Service) buildCommentHistory(ctx context.Context, taskID uint) string {
	comments, err := s.opts.Store.ListComments(ctx, taskID)
	if err != nil || len(comments) == 0 {
		return ""
	}

	var b strings.Builder
	for _, c := range comments {
		body := strings.TrimSpace(c.Body)
		if body == "" {
			continue
		}
		// Skip tool comments (raw JSON, not useful as text context).
		if c.Kind == "tool" {
			continue
		}
		b.WriteString(fmt.Sprintf("[%s] %s: %s\n\n", c.Kind, c.Author, truncateForHistory(body, 2000)))
	}

	result := b.String()
	if len(result) > 8000 {
		result = result[:8000] + "\n... (truncated)"
	}
	return result
}

// collectOutcomes tries directory-based artifact collection from ~/tasks/<id>/
// on the instance. Falls back to mention-based scanning if the directory is
// empty or doesn't exist.
func (s *Service) collectOutcomes(ctx context.Context, taskID, instanceID uint, assistantText string) (pulled, skipped []string) {
	outcomeDir := fmt.Sprintf("%s/%d", s.opts.Settings.TaskOutcomeDir(), taskID)
	entries, err := s.walkDir(ctx, instanceID, outcomeDir)
	if err == nil && len(entries) > 0 {
		return s.collectFromDir(ctx, taskID, instanceID, outcomeDir, entries)
	}
	// Fallback to mention-based collection.
	log.Printf("[moderator] task %d: ~/tasks/%d/ empty or missing, falling back to mention-based collection", taskID, taskID)
	return s.collectArtifactsMentionBased(ctx, taskID, instanceID, assistantText)
}

// collectFromDir downloads files from the instance's outcome directory and
// stores them as artifacts on the control-plane.
func (s *Service) collectFromDir(ctx context.Context, taskID, instanceID uint, baseDir string, entries []FileEntry) (pulled, skipped []string) {
	maxBytes := s.opts.Settings.ArtifactMaxBytes()
	storageRoot := filepath.Join(s.opts.Settings.ArtifactStorageDir(), fmt.Sprintf("%d", taskID))

	for _, e := range entries {
		rel, err := filepath.Rel(baseDir, e.Path)
		if err != nil {
			continue
		}

		if e.Size > maxBytes {
			skipped = append(skipped, fmt.Sprintf("%s (size %d > max %d)", rel, e.Size, maxBytes))
			continue
		}

		data, err := s.opts.Workspace.Read(ctx, instanceID, e.Path)
		if err != nil {
			skipped = append(skipped, rel+" (read error: "+err.Error()+")")
			continue
		}

		dst := filepath.Join(storageRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			skipped = append(skipped, rel+" (mkdir: "+err.Error()+")")
			continue
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			skipped = append(skipped, rel+" (write: "+err.Error()+")")
			continue
		}
		sum := sha256.Sum256(data)
		_ = s.opts.Store.InsertArtifact(ctx, Artifact{
			TaskID:      taskID,
			Path:        rel,
			SizeBytes:   int64(len(data)),
			SHA256:      hex.EncodeToString(sum[:]),
			StoragePath: dst,
		})
		pulled = append(pulled, rel)
	}
	return
}

// walkDir recursively lists all files under a directory on the instance.
func (s *Service) walkDir(ctx context.Context, instanceID uint, dir string) ([]FileEntry, error) {
	entries, err := s.opts.Workspace.List(ctx, instanceID, dir)
	if err != nil {
		return nil, err
	}
	var all []FileEntry
	for _, e := range entries {
		if e.IsDir {
			sub, _ := s.walkDir(ctx, instanceID, e.Path)
			all = append(all, sub...)
		} else {
			all = append(all, e)
		}
	}
	return all, nil
}

// collectArtifactsMentionBased is the legacy fallback: scans the agent
// transcript for explicit file mentions under the workspace dir.
func (s *Service) collectArtifactsMentionBased(ctx context.Context, taskID, instanceID uint, transcript string) (pulled, skipped []string) {
	workspace := s.workspaceDir(ctx, instanceID)
	maxBytes := s.opts.Settings.ArtifactMaxBytes()
	storageRoot := filepath.Join(s.opts.Settings.ArtifactStorageDir(), fmt.Sprintf("%d", taskID))

	mentions := ExtractMentionedPaths(transcript, workspace)
	seen := map[string]bool{}
	for _, rel := range mentions {
		if seen[rel] {
			continue
		}
		seen[rel] = true

		absInInstance := filepath.Join(workspace, rel)
		bytes, err := s.opts.Workspace.Read(ctx, instanceID, absInInstance)
		if err != nil {
			skipped = append(skipped, rel+" (read error: "+err.Error()+")")
			continue
		}
		if int64(len(bytes)) > maxBytes {
			skipped = append(skipped, fmt.Sprintf("%s (size %d > max %d)", rel, len(bytes), maxBytes))
			continue
		}

		dst := filepath.Join(storageRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			skipped = append(skipped, rel+" (mkdir: "+err.Error()+")")
			continue
		}
		if err := os.WriteFile(dst, bytes, 0o644); err != nil {
			skipped = append(skipped, rel+" (write: "+err.Error()+")")
			continue
		}
		sum := sha256.Sum256(bytes)
		_ = s.opts.Store.InsertArtifact(ctx, Artifact{
			TaskID:      taskID,
			Path:        rel,
			SizeBytes:   int64(len(bytes)),
			SHA256:      hex.EncodeToString(sum[:]),
			StoragePath: dst,
		})
		pulled = append(pulled, rel)
	}
	return
}

func (s *Service) evaluate(ctx context.Context, task Task, finalText string, artifacts []string) error {
	provKey, model := s.opts.Settings.ModeratorProvider()
	if task.EvaluatorProviderKey != "" {
		provKey = task.EvaluatorProviderKey
	}
	if task.EvaluatorModel != "" {
		model = task.EvaluatorModel
	}
	prompt := "Evaluate whether this agent run accomplished the user's task.\n\n" +
		"TASK:\n" + task.Description + "\n\n" +
		"AGENT FINAL OUTPUT:\n" + truncate(finalText, 4000) + "\n\n" +
		"ARTIFACTS PULLED: " + strings.Join(artifacts, ", ") + "\n\n" +
		"Reply with a brief evaluation (3-6 sentences) and a verdict line: VERDICT: success|partial|failed."
	resp, err := s.opts.LLM.Complete(ctx, provKey, model, prompt)
	if err != nil {
		return err
	}
	_, err = s.opts.Store.InsertComment(ctx, Comment{
		TaskID: task.ID,
		Kind:   "evaluation",
		Author: "moderator",
		Body:   resp,
	})
	return err
}

func truncateForHistory(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func randomSuffix(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// workspaceDir is the agent's declared workspace directory, falling back to
// the kanban_workspace_dir setting.
func (s *Service) workspaceDir(ctx context.Context, instanceID uint) string {
	if s.opts.Agents != nil {
		if d := s.opts.Agents.WorkspaceDir(ctx, instanceID); d != "" {
			return d
		}
	}
	return s.opts.Settings.WorkspaceDir()
}

// nonEmptyJSON returns raw as JSON, or null when empty or invalid.
func nonEmptyJSON(raw string) string {
	if raw == "" || !json.Valid([]byte(raw)) {
		return "null"
	}
	return raw
}
