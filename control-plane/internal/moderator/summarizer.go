package moderator

import (
	"context"
	"log"
	"strings"
	"time"
)

// StartSummarizer launches a background goroutine that periodically refreshes
// the InstanceSoul (workspace summary + skill list) for every known instance.
// It returns immediately; the goroutine exits when ctx is canceled.
func (s *Service) StartSummarizer(ctx context.Context) {
	go s.summarizerLoop(ctx)
}

func (s *Service) summarizerLoop(ctx context.Context) {
	interval := s.opts.Settings.SummaryInterval()
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	// Run once on startup so freshly-spawned services have data quickly.
	s.refreshAll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.refreshAll(ctx)
		}
	}
}

func (s *Service) refreshAll(ctx context.Context) {
	ids, err := s.opts.Instances.ListInstanceIDs(ctx)
	if err != nil {
		log.Printf("[moderator/summarizer] list instances: %v", err)
		return
	}
	for _, id := range ids {
		if err := s.refreshOne(ctx, id); err != nil {
			log.Printf("[moderator/summarizer] instance %d: %v", id, err)
		}
	}
}

func (s *Service) refreshOne(ctx context.Context, instanceID uint) error {
	workspace := s.opts.Settings.WorkspaceDir()
	entries, err := s.opts.Workspace.List(ctx, instanceID, workspace)
	if err != nil {
		return err
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir || !strings.HasSuffix(e.Path, ".md") {
			continue
		}
		body, err := s.opts.Workspace.Read(ctx, instanceID, e.Path)
		if err != nil {
			continue
		}
		b.WriteString("--- " + e.Path + " ---\n")
		b.Write(body)
		b.WriteString("\n\n")
		if b.Len() > 16000 {
			break
		}
	}

	summary := ""
	if b.Len() > 0 {
		provKey, model := s.opts.Settings.ModeratorProvider()
		prompt := "In ONE paragraph (max 120 words), describe the personality, current focus, and recent activity of the OpenClaw agent whose workspace markdown is below. Be concrete.\n\n" + b.String()
		if resp, err := s.opts.LLM.Complete(ctx, provKey, model, prompt); err == nil {
			summary = strings.TrimSpace(resp)
		}
	}

	return s.opts.Store.UpsertSoul(ctx, Soul{
		InstanceID: instanceID,
		Summary:    summary,
		Skills:     nil, // skills enumeration handled separately if available
	})
}
