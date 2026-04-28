// Package taskmanager owns every long-running goroutine spawned by a
// user-initiated request (instance create/restart/clone/image-update,
// backup create, skill deploy). It is an in-memory source of truth for
// "what work is currently happening" — see docs/task-manager.md.
package taskmanager

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TaskType identifies the kind of work a task represents.
type TaskType string

const (
	TaskInstanceCreate      TaskType = "instance.create"
	TaskInstanceRestart     TaskType = "instance.restart"
	TaskInstanceImageUpdate TaskType = "instance.image_update"
	TaskInstanceClone       TaskType = "instance.clone"
	TaskBackupCreate        TaskType = "backup.create"
	TaskSkillDeploy         TaskType = "skill.deploy"
	// Browser-pod lifecycle tasks (on-demand browser feature).
	TaskBrowserSpawn   TaskType = "browser.spawn"
	TaskBrowserMigrate TaskType = "browser.migrate"
)

// State is the lifecycle position of a task.
type State string

const (
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateCanceled  State = "canceled"
)

// OnCancel is invoked exactly once when a task is canceled, after ctx is
// canceled but before the task transitions to the terminal "canceled" state.
// It is used to clean up partial side effects (remove in-progress files,
// roll back DB rows, etc.).
//
// A nil OnCancel means the task is NOT user-cancellable: Manager.Cancel
// returns ErrNotCancellable for it.
type OnCancel func(ctx context.Context)

// RunFunc is the actual work performed by a task. It must honour ctx
// cancellation — returning promptly when ctx.Done() fires. Its return value
// determines whether the task ends in StateSucceeded (nil) or StateFailed
// (non-nil error, whose message is stored in Task.Message). If the task was
// already canceled, the return value is ignored and the state stays
// StateCanceled.
type RunFunc func(ctx context.Context, h *Handle) error

// Task is the public, JSON-serialisable view of a task. Internal fields
// (cancel, onCancel, etc.) live on taskInternal.
type Task struct {
	ID           string   `json:"id"`
	Type         TaskType `json:"type"`
	InstanceID   uint     `json:"instance_id,omitempty"` // metadata only — used for filtering
	UserID       uint     `json:"user_id,omitempty"`     // visibility anchor; 0 = system task (admin-only)
	ResourceID   string   `json:"resource_id,omitempty"` // type-specific (backup id, etc.)
	ResourceName string   `json:"resource_name,omitempty"`
	Title        string   `json:"title"` // toast title, e.g. "Migrating instance foo"; the toast icon conveys success/error/canceled
	State        State    `json:"state"`
	Message      string   `json:"message,omitempty"`
	// Cancellable tells clients whether they can show a Cancel UI for this
	// task. Mirrors `OnCancel != nil` at Start time.
	Cancellable bool       `json:"cancellable,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
}

// StartOpts is the input to Manager.Start. Title is required — the task
// manager itself never derives toast text from Type. The same Title is shown
// across running/succeeded/failed/canceled states; the toast icon (loading,
// check, X, info) is what differentiates state on the UI.
type StartOpts struct {
	Type         TaskType
	InstanceID   uint
	UserID       uint // initiating user; 0 = system task (admin-only visibility)
	ResourceID   string
	ResourceName string
	Title        string
	OnCancel     OnCancel
	Run          RunFunc
}

// Handle is passed into RunFunc so the running goroutine can update the
// task's user-visible Message (drives the toast description on the frontend).
type Handle struct {
	mgr *Manager
	id  string
}

// UpdateMessage sets the task's Message. Thread-safe; triggers an "updated"
// event to SSE subscribers.
func (h *Handle) UpdateMessage(msg string) {
	if h == nil || h.mgr == nil {
		return
	}
	h.mgr.updateMessage(h.id, msg)
}

// ID returns the task ID this handle refers to.
func (h *Handle) ID() string { return h.id }

// EventType describes what happened to a task.
type EventType string

const (
	EventStarted EventType = "started"
	EventUpdated EventType = "updated"
	EventEnded   EventType = "ended"
)

// Event is what SSE subscribers receive.
type Event struct {
	Type EventType `json:"type"`
	Task Task      `json:"task"`
}

// Filter narrows List results. Zero values match everything.
type Filter struct {
	Type       TaskType
	InstanceID uint // 0 = any
	UserID     uint // 0 = any
	ResourceID string
	State      State
	OnlyActive bool // if true, exclude tasks that ended more than `since` ago
}

// Errors.
var (
	ErrNotFound        = errors.New("task not found")
	ErrNotCancellable  = errors.New("task is not cancellable")
	ErrAlreadyTerminal = errors.New("task already finished")
)

// Config tunes the manager.
type Config struct {
	// RetainTerminal is how long a terminal task (succeeded/failed/canceled)
	// remains visible before being GC'd. Default 1h.
	RetainTerminal time.Duration
	// SubscriberBuffer is the per-subscriber channel buffer; drops occur
	// when a subscriber is slower than the producer. Default 32.
	SubscriberBuffer int
	// GCInterval is how often the background sweeper runs. Default 5m.
	GCInterval time.Duration
}

// Manager owns the task registry.
type Manager struct {
	cfg Config

	mu      sync.RWMutex
	tasks   map[string]*taskInternal
	subs    map[int]chan Event
	nextSub int

	stopGC chan struct{}
	gcWG   sync.WaitGroup
}

type taskInternal struct {
	mu       sync.Mutex
	pub      Task
	cancel   context.CancelFunc
	onCancel OnCancel
	canceled bool // OnCancel already fired / pending-cancel flag
}

// New returns a started Manager. Call Close to shut it down.
func New(cfg Config) *Manager {
	if cfg.RetainTerminal <= 0 {
		cfg.RetainTerminal = time.Hour
	}
	if cfg.SubscriberBuffer <= 0 {
		cfg.SubscriberBuffer = 32
	}
	if cfg.GCInterval <= 0 {
		cfg.GCInterval = 5 * time.Minute
	}
	m := &Manager{
		cfg:    cfg,
		tasks:  make(map[string]*taskInternal),
		subs:   make(map[int]chan Event),
		stopGC: make(chan struct{}),
	}
	m.gcWG.Add(1)
	go m.gcLoop()
	return m
}

// Close stops background workers. Does not cancel in-flight tasks; callers
// that want that should Cancel each in turn or rely on process exit.
func (m *Manager) Close() {
	close(m.stopGC)
	m.gcWG.Wait()
	m.mu.Lock()
	for id, ch := range m.subs {
		close(ch)
		delete(m.subs, id)
	}
	m.mu.Unlock()
}

// Start registers a task, spawns a goroutine to run opts.Run, and returns
// the task ID immediately. The task is created in StateRunning and will
// transition to a terminal state when Run returns or when Cancel is called.
func (m *Manager) Start(opts StartOpts) string {
	if opts.Run == nil {
		panic("taskmanager.Start: Run is required")
	}
	if opts.Title == "" {
		panic("taskmanager.Start: Title is required")
	}
	id := uuid.NewString()
	ctx, cancel := context.WithCancel(context.Background())

	ti := &taskInternal{
		pub: Task{
			ID:           id,
			Type:         opts.Type,
			InstanceID:   opts.InstanceID,
			UserID:       opts.UserID,
			ResourceID:   opts.ResourceID,
			ResourceName: opts.ResourceName,
			Title:        opts.Title,
			State:        StateRunning,
			Cancellable:  opts.OnCancel != nil,
			StartedAt:    time.Now().UTC(),
		},
		cancel:   cancel,
		onCancel: opts.OnCancel,
	}

	m.mu.Lock()
	m.tasks[id] = ti
	m.mu.Unlock()

	m.broadcast(Event{Type: EventStarted, Task: ti.snapshot()})

	go func() {
		defer cancel()
		h := &Handle{mgr: m, id: id}
		runErr := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = errFromPanic(r)
				}
			}()
			return opts.Run(ctx, h)
		}()
		m.finish(id, runErr)
	}()

	return id
}

// Get returns a snapshot of the task by ID.
func (m *Manager) Get(id string) (Task, bool) {
	m.mu.RLock()
	ti, ok := m.tasks[id]
	m.mu.RUnlock()
	if !ok {
		return Task{}, false
	}
	return ti.snapshot(), true
}

// List returns snapshots of all tasks matching the filter, ordered by
// StartedAt ascending.
func (m *Manager) List(f Filter) []Task {
	m.mu.RLock()
	out := make([]Task, 0, len(m.tasks))
	for _, ti := range m.tasks {
		t := ti.snapshot()
		if f.Type != "" && t.Type != f.Type {
			continue
		}
		if f.InstanceID != 0 && t.InstanceID != f.InstanceID {
			continue
		}
		if f.UserID != 0 && t.UserID != f.UserID {
			continue
		}
		if f.ResourceID != "" && t.ResourceID != f.ResourceID {
			continue
		}
		if f.State != "" && t.State != f.State {
			continue
		}
		if f.OnlyActive && t.State != StateRunning {
			continue
		}
		out = append(out, t)
	}
	m.mu.RUnlock()
	// Sort ascending by StartedAt for stable output.
	// Keep simple bubble/insertion since the set is small.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].StartedAt.After(out[j].StartedAt); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Cancel requests cancellation of a running task. Returns ErrNotFound if the
// task does not exist, ErrAlreadyTerminal if it already ended, or
// ErrNotCancellable if the task was started without an OnCancel callback.
// Cancel is idempotent on a running cancellable task — repeated calls return
// nil without double-invoking OnCancel.
func (m *Manager) Cancel(id string) error {
	m.mu.RLock()
	ti, ok := m.tasks[id]
	m.mu.RUnlock()
	if !ok {
		return ErrNotFound
	}

	ti.mu.Lock()
	if ti.pub.State != StateRunning {
		ti.mu.Unlock()
		return ErrAlreadyTerminal
	}
	if ti.onCancel == nil {
		ti.mu.Unlock()
		return ErrNotCancellable
	}
	alreadyCanceled := ti.canceled
	ti.canceled = true
	onCancel := ti.onCancel
	cancel := ti.cancel
	ti.mu.Unlock()

	if alreadyCanceled {
		return nil
	}

	// Cancel ctx first so the running goroutine can start unwinding; then
	// invoke the cleanup callback with a fresh context (so cleanup itself
	// isn't instantly cancelled).
	cancel()
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cleanupCancel()
	onCancel(cleanupCtx)
	return nil
}

// Subscribe returns a channel that receives every task lifecycle event and a
// cancel func that unsubscribes and closes the channel.
func (m *Manager) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, m.cfg.SubscriberBuffer)
	m.mu.Lock()
	id := m.nextSub
	m.nextSub++
	m.subs[id] = ch
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		if c, ok := m.subs[id]; ok {
			close(c)
			delete(m.subs, id)
		}
		m.mu.Unlock()
	}
}

// --- internal ---

func (m *Manager) updateMessage(id, msg string) {
	m.mu.RLock()
	ti, ok := m.tasks[id]
	m.mu.RUnlock()
	if !ok {
		return
	}
	ti.mu.Lock()
	if ti.pub.State != StateRunning {
		ti.mu.Unlock()
		return
	}
	ti.pub.Message = msg
	snap := ti.pub
	ti.mu.Unlock()
	m.broadcast(Event{Type: EventUpdated, Task: snap})
}

func (m *Manager) finish(id string, runErr error) {
	m.mu.RLock()
	ti, ok := m.tasks[id]
	m.mu.RUnlock()
	if !ok {
		return
	}
	now := time.Now().UTC()
	ti.mu.Lock()
	// If cancelation already applied, runErr is ignored and state stays canceled.
	if ti.canceled {
		ti.pub.State = StateCanceled
		if ti.pub.Message == "" {
			ti.pub.Message = "canceled"
		}
	} else if runErr != nil {
		ti.pub.State = StateFailed
		ti.pub.Message = runErr.Error()
	} else {
		ti.pub.State = StateSucceeded
	}
	ti.pub.EndedAt = &now
	snap := ti.pub
	ti.mu.Unlock()
	m.broadcast(Event{Type: EventEnded, Task: snap})
}

func (m *Manager) broadcast(ev Event) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ch := range m.subs {
		// Non-blocking: drop the event for slow subscribers so they can't
		// stall the pipeline. Reconnection refreshes state via List().
		select {
		case ch <- ev:
		default:
		}
	}
}

func (m *Manager) gcLoop() {
	defer m.gcWG.Done()
	t := time.NewTicker(m.cfg.GCInterval)
	defer t.Stop()
	for {
		select {
		case <-m.stopGC:
			return
		case now := <-t.C:
			m.gcOnce(now)
		}
	}
}

func (m *Manager) gcOnce(now time.Time) {
	cutoff := now.Add(-m.cfg.RetainTerminal)
	m.mu.Lock()
	for id, ti := range m.tasks {
		ti.mu.Lock()
		if ti.pub.EndedAt != nil && ti.pub.EndedAt.Before(cutoff) {
			delete(m.tasks, id)
		}
		ti.mu.Unlock()
	}
	m.mu.Unlock()
}

func (ti *taskInternal) snapshot() Task {
	ti.mu.Lock()
	defer ti.mu.Unlock()
	t := ti.pub
	if t.EndedAt != nil {
		end := *t.EndedAt
		t.EndedAt = &end
	}
	return t
}

func errFromPanic(r interface{}) error {
	if err, ok := r.(error); ok {
		return err
	}
	return errors.New(panicString(r))
}

func panicString(r interface{}) string {
	if s, ok := r.(string); ok {
		return "panic: " + s
	}
	return "panic"
}
