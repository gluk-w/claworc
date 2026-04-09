package moderator

import (
	"context"
	"errors"
	"log"
	"sync"
)

// ErrStopped is returned by Run when the task was canceled via Stop.
var ErrStopped = errors.New("task stopped by user")

// Options holds all dependencies needed to construct a Service. Every field
// is an interface so the package has zero claworc-internal imports.
type Options struct {
	Dialer    GatewayDialer
	Workspace WorkspaceFS
	LLM       LLMClient
	Store     Store
	Settings  Settings
	Instances InstanceLister
}

// Service is the entry point for moderator operations. It is safe for
// concurrent use; long-running operations are launched as goroutines that
// share the underlying ports.
type Service struct {
	opts Options

	mu      sync.Mutex
	running map[uint]context.CancelFunc // taskID → cancel
}

// New constructs a Service. Callers must supply non-nil ports.
func New(opts Options) *Service {
	return &Service{opts: opts, running: map[uint]context.CancelFunc{}}
}

// EnqueueTask kicks off Dispatch + Run for a freshly created task in a
// background goroutine. Returns immediately so HTTP handlers stay snappy.
func (s *Service) EnqueueTask(taskID uint) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.running[taskID] = cancel
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.running, taskID)
			s.mu.Unlock()
			cancel()
		}()
		if err := s.Dispatch(ctx, taskID); err != nil {
			if errors.Is(err, context.Canceled) {
				s.markStopped(taskID)
				return
			}
			log.Printf("[moderator] dispatch task %d: %v", taskID, err)
			s.markFailed(ctx, taskID, err)
			return
		}
		if err := s.Run(ctx, taskID); err != nil {
			if errors.Is(err, ErrStopped) || errors.Is(err, context.Canceled) {
				s.markStopped(taskID)
				return
			}
			log.Printf("[moderator] run task %d: %v", taskID, err)
			s.markFailed(context.Background(), taskID, err)
		}
	}()
}

// Reopen re-runs a task whose prior run is finished or failed. The runner
// reads existing comments and includes the user's latest comments as
// follow-up context for the agent.
func (s *Service) Reopen(taskID uint) {
	_ = s.opts.Store.UpdateTask(context.Background(), taskID, map[string]any{"status": "todo"})
	s.EnqueueTask(taskID)
}

// Stop cancels a running task if present. Returns true if it was running.
func (s *Service) Stop(taskID uint) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.running[taskID]; ok {
		cancel()
		return true
	}
	return false
}

func (s *Service) markStopped(taskID uint) {
	ctx := context.Background()
	_, _ = s.opts.Store.InsertComment(ctx, Comment{
		TaskID: taskID, Kind: "moderator", Author: "moderator",
		Body: "Task stopped.",
	})
	_ = s.opts.Store.UpdateTask(ctx, taskID, map[string]any{"status": "todo"})
}

func (s *Service) markFailed(ctx context.Context, taskID uint, cause error) {
	_, _ = s.opts.Store.InsertComment(ctx, Comment{
		TaskID: taskID,
		Kind:   "error",
		Author: "moderator",
		Body:   cause.Error(),
	})
	_ = s.opts.Store.UpdateTask(ctx, taskID, map[string]any{"status": "failed"})
}
