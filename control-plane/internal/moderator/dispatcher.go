package moderator

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Dispatch picks the best instance for a task using the moderator LLM,
// records the routing decision as a comment, and updates the task with the
// chosen instance + dispatching status. The runner takes over from there.
func (s *Service) Dispatch(ctx context.Context, taskID uint) error {
	task, err := s.opts.Store.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("load task: %w", err)
	}
	board, err := s.opts.Store.GetBoard(ctx, task.BoardID)
	if err != nil {
		return fmt.Errorf("load board: %w", err)
	}
	if len(board.EligibleInstances) == 0 {
		return fmt.Errorf("board %d has no eligible instances", board.ID)
	}

	// Drop ids that no longer correspond to a live instance row — the board's
	// EligibleInstances JSON can lag behind instance deletion.
	liveIDs, err := s.opts.Instances.ListInstanceIDs(ctx)
	if err != nil {
		return fmt.Errorf("list instances: %w", err)
	}
	live := make(map[uint]struct{}, len(liveIDs))
	for _, id := range liveIDs {
		live[id] = struct{}{}
	}
	filtered := board.EligibleInstances[:0:0]
	for _, id := range board.EligibleInstances {
		if _, ok := live[id]; ok {
			filtered = append(filtered, id)
		}
	}
	board.EligibleInstances = filtered
	if len(board.EligibleInstances) == 0 {
		return fmt.Errorf("board %d has no eligible instances", board.ID)
	}

	souls, err := s.opts.Store.GetSouls(ctx, board.EligibleInstances)
	if err != nil {
		return fmt.Errorf("load souls: %w", err)
	}

	// If only one candidate, skip the LLM call.
	var chosen uint
	var reason string
	if len(board.EligibleInstances) == 1 {
		chosen = board.EligibleInstances[0]
		reason = "Only one eligible instance on this board."
	} else {
		chosen, reason, err = s.rank(ctx, task, board.EligibleInstances, souls)
		if err != nil {
			return fmt.Errorf("rank: %w", err)
		}
	}

	name, _ := s.opts.Instances.InstanceName(ctx, chosen)
	if name == "" {
		name = fmt.Sprintf("#%d", chosen)
	}
	if _, err := s.opts.Store.InsertComment(ctx, Comment{
		TaskID: taskID,
		Kind:   "routing",
		Author: "moderator",
		Body:   fmt.Sprintf("Routed to %s.\n\n%s", name, reason),
	}); err != nil {
		return fmt.Errorf("insert routing comment: %w", err)
	}

	if err := s.opts.Store.UpdateTask(ctx, taskID, map[string]any{
		"status":               "dispatching",
		"assigned_instance_id": chosen,
	}); err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	return nil
}

// rank asks the moderator LLM to pick the best-fit instance from candidates,
// using each candidate's cached "soul" (workspace summary + skills) as
// context. Falls back to the first candidate if the LLM returns garbage.
func (s *Service) rank(ctx context.Context, task Task, candidates []uint, souls []Soul) (uint, string, error) {
	soulByID := make(map[uint]Soul, len(souls))
	for _, sl := range souls {
		soulByID[sl.InstanceID] = sl
	}

	var b strings.Builder
	b.WriteString("You are routing a task to the best-fit OpenClaw agent.\n\n")
	b.WriteString("TASK:\n")
	b.WriteString("Title: " + task.Title + "\n")
	b.WriteString("Description: " + task.Description + "\n\n")
	b.WriteString("CANDIDATES:\n")
	for _, id := range candidates {
		sl := soulByID[id]
		b.WriteString(fmt.Sprintf("- instance_id=%d skills=%v\n  soul: %s\n", id, sl.Skills, truncate(sl.Summary, 600)))
	}
	b.WriteString("\nReply with strict JSON: {\"instance_id\": <id>, \"reason\": \"<one paragraph>\"}.")

	provKey, model := s.opts.Settings.ModeratorProvider()
	if task.EvaluatorProviderKey != "" {
		provKey = task.EvaluatorProviderKey
	}
	if task.EvaluatorModel != "" {
		model = task.EvaluatorModel
	}

	resp, err := s.opts.LLM.Complete(ctx, provKey, model, b.String())
	if err != nil {
		return candidates[0], "LLM unavailable; defaulted to first candidate. Error: " + err.Error(), nil
	}

	// Tolerant JSON extraction.
	if id, reason, ok := parseRankReply(resp); ok && containsUint(candidates, id) {
		return id, reason, nil
	}
	return candidates[0], "Could not parse LLM ranking reply; defaulted to first candidate.\nRaw: " + truncate(resp, 400), nil
}

func parseRankReply(s string) (uint, string, bool) {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return 0, "", false
	}
	var out struct {
		InstanceID json.Number `json:"instance_id"`
		Reason     string      `json:"reason"`
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &out); err != nil {
		return 0, "", false
	}
	n, err := strconv.ParseUint(string(out.InstanceID), 10, 64)
	if err != nil || n > math.MaxUint {
		return 0, "", false
	}
	return uint(n), out.Reason, true
}

func containsUint(xs []uint, x uint) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
