package browserprov

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/envvars"
	"github.com/gluk-w/claworc/control-plane/internal/taskmanager"
)

// SettingsReader looks up admin-level defaults that influence bridge behaviour
// at runtime (idle minutes, ready seconds, default browser image, default
// storage). The control-plane database package satisfies this interface
// directly via free functions — we accept any concrete reader so unit tests
// can swap in a stub.
type SettingsReader interface {
	GetSetting(key string) (string, error)
}

// settingsAdapter wraps the package-level database.GetSetting for the
// SettingsReader contract.
type settingsAdapter struct{}

func (settingsAdapter) GetSetting(key string) (string, error) { return database.GetSetting(key) }

// BrowserBridge is the single coordinator for non-legacy instances. The CDP
// agent-listener tunnel and the desktop VNC handler both call DialCDP /
// DialVNC, which transparently spawn a session through TaskManager when none
// is running. A background reaper goroutine spins down idle sessions.
type BrowserBridge struct {
	provider Provider
	tasks    *taskmanager.Manager
	settings SettingsReader

	// spawnMu serialises spawn/reap decisions per instance.
	spawnMu sync.Mutex
	// inflight maps instanceID → channel that closes when the in-flight
	// spawn task settles. Used to dedup concurrent EnsureSession calls.
	inflight map[uint]chan struct{}

	// activity tracking: we coalesce Touch() calls in-memory and flush
	// every 30 s to avoid hot-path DB writes on every CDP frame.
	activityMu     sync.Mutex
	pendingTouches map[uint]time.Time

	cancel func()
}

// New constructs a bridge. The caller is responsible for calling Close to
// stop background goroutines.
func New(provider Provider, tasks *taskmanager.Manager) *BrowserBridge {
	return &BrowserBridge{
		provider:       provider,
		tasks:          tasks,
		settings:       settingsAdapter{},
		inflight:       make(map[uint]chan struct{}),
		pendingTouches: make(map[uint]time.Time),
	}
}

// Provider returns the underlying provider (used by HTTP handlers that need to
// inspect capabilities).
func (b *BrowserBridge) Provider() Provider { return b.provider }

// Start kicks off the activity flusher and the idle reaper. Both stop when
// ctx is canceled.
func (b *BrowserBridge) Start(ctx context.Context) {
	bgCtx, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	go b.activityFlusher(bgCtx)
	go b.idleReaper(bgCtx)
}

// Close stops background goroutines.
func (b *BrowserBridge) Close() {
	if b.cancel != nil {
		b.cancel()
	}
}

// EnsureSession is the single entry point: it returns when the provider's
// session is in StatusRunning. If a session is already running, returns
// immediately. If a spawn task is already inflight for this instance, blocks
// on it. Otherwise starts a new spawn task through TaskManager (with an
// OnCancel callback that rolls back partial state) and waits for it.
//
// userID is the initiator for TaskManager attribution. Use 0 for system
// callers (e.g., the agent-listener loop reacting to an inbound CDP byte).
func (b *BrowserBridge) EnsureSession(ctx context.Context, instanceID, userID uint) error {
	// Fast path: existing running session.
	if status, err := b.provider.SessionStatus(ctx, instanceID); err == nil && status == StatusRunning {
		// Refresh DB row so the reaper doesn't miss a recently-created session.
		_ = database.UpsertBrowserSession(&database.BrowserSession{
			InstanceID: instanceID,
			Provider:   b.provider.Name(),
			Status:     "running",
			LastUsedAt: time.Now().UTC(),
		})
		return nil
	}

	// Dedup against concurrent spawners.
	b.spawnMu.Lock()
	if ch, ok := b.inflight[instanceID]; ok {
		b.spawnMu.Unlock()
		select {
		case <-ch:
			return b.lastSpawnError(instanceID)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ch := make(chan struct{})
	b.inflight[instanceID] = ch
	b.spawnMu.Unlock()

	instanceLabel := fmt.Sprintf("instance %d", instanceID)
	var inst database.Instance
	if err := database.DB.Select("display_name").First(&inst, instanceID).Error; err == nil && inst.DisplayName != "" {
		instanceLabel = inst.DisplayName
	}
	taskID := b.tasks.Start(taskmanager.StartOpts{
		Type:       taskmanager.TaskBrowserSpawn,
		InstanceID: instanceID,
		UserID:     userID,
		Title:      fmt.Sprintf("Starting browser for %s", instanceLabel),
		OnCancel:   b.makeRollback(instanceID),
		Run: func(taskCtx context.Context, h *taskmanager.Handle) error {
			defer func() {
				b.spawnMu.Lock()
				delete(b.inflight, instanceID)
				close(ch)
				b.spawnMu.Unlock()
			}()
			return b.doSpawn(taskCtx, h, instanceID)
		},
	})
	_ = taskID

	// Wait for spawn task to finish (or ctx cancel).
	select {
	case <-ch:
		return b.lastSpawnError(instanceID)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DialCDP ensures a session and returns a byte-stream conn to the browser's
// CDP endpoint. Activity is recorded so the idle reaper does not reap a
// session in active use.
func (b *BrowserBridge) DialCDP(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error) {
	if err := b.EnsureSession(ctx, instanceID, 0); err != nil {
		return nil, err
	}
	conn, err := b.provider.DialCDP(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	b.Touch(instanceID)
	return conn, nil
}

// DialVNC mirrors DialCDP but for the VNC websocket endpoint. Returns
// ErrNotSupported when the provider can't expose VNC.
func (b *BrowserBridge) DialVNC(ctx context.Context, instanceID uint) (io.ReadWriteCloser, error) {
	if !b.provider.Capabilities().SupportsVNC {
		return nil, ErrNotSupported
	}
	if err := b.EnsureSession(ctx, instanceID, 0); err != nil {
		return nil, err
	}
	conn, err := b.provider.DialVNC(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	b.Touch(instanceID)
	return conn, nil
}

// Touch marks an instance as recently active. The flusher persists touches
// in batches every 30 s.
func (b *BrowserBridge) Touch(instanceID uint) {
	b.activityMu.Lock()
	b.pendingTouches[instanceID] = time.Now().UTC()
	b.activityMu.Unlock()
}

// --- internals ---

// doSpawn is the body of the TaskBrowserSpawn task.
func (b *BrowserBridge) doSpawn(ctx context.Context, h *taskmanager.Handle, instanceID uint) error {
	// Look up the instance to derive params.
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return fmt.Errorf("instance %d: %w", instanceID, err)
	}
	if database.IsLegacyEmbedded(inst.ContainerImage) {
		return errors.New("instance is legacy embedded — no separate browser pod to spawn")
	}

	image := inst.BrowserImage
	if image == "" {
		def, _ := b.settings.GetSetting("default_browser_image")
		image = def
	}
	if image == "" {
		return errors.New("no browser image configured (set Instance.BrowserImage or default_browser_image)")
	}
	storage := inst.BrowserStorage
	if storage == "" {
		def, _ := b.settings.GetSetting("default_browser_storage")
		storage = def
	}

	// Ensure a row exists so handlers querying status don't 404.
	now := time.Now().UTC()
	_ = database.UpsertBrowserSession(&database.BrowserSession{
		InstanceID: instanceID,
		Provider:   b.provider.Name(),
		Status:     "starting",
		Image:      image,
		PodName:    inst.Name + "-browser",
		LastUsedAt: now,
		StartedAt:  now,
	})
	h.UpdateMessage("Starting browser pod")

	if err := ctx.Err(); err != nil {
		return err
	}

	// Merge global defaults with per-instance overrides so the browser pod
	// receives the same user-defined env vars as the agent container.
	mergedEnv := envvars.Merge(envvars.LoadGlobal(), envvars.LoadInstance(inst))

	if _, err := b.provider.EnsureSession(ctx, instanceID, SessionParams{
		Image:         image,
		StorageSize:   storage,
		VNCResolution: inst.VNCResolution,
		UserAgent:     inst.UserAgent,
		Timezone:      inst.Timezone,
		EnvVars:       mergedEnv,
	}); err != nil {
		_ = database.UpdateBrowserSessionStatus(instanceID, "error", err.Error())
		return err
	}

	// Probe CDP /json/version to confirm Chromium is fully up.
	h.UpdateMessage("Waiting for CDP")
	if err := b.waitForCDPReady(ctx, instanceID, b.readyTimeout()); err != nil {
		_ = database.UpdateBrowserSessionStatus(instanceID, "error", err.Error())
		return err
	}

	_ = database.UpdateBrowserSessionStatus(instanceID, "running", "")
	h.UpdateMessage("Browser ready")
	return nil
}

func (b *BrowserBridge) waitForCDPReady(ctx context.Context, instanceID uint, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := b.provider.DialCDP(ctx, instanceID)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("CDP not ready within %s", timeout)
}

func (b *BrowserBridge) readyTimeout() time.Duration {
	if b.settings == nil {
		return 60 * time.Second
	}
	v, _ := b.settings.GetSetting("default_browser_ready_seconds")
	d, ok := parseSeconds(v)
	if !ok {
		return 60 * time.Second
	}
	return d
}

func (b *BrowserBridge) idleTimeout() time.Duration {
	if b.settings == nil {
		return 15 * time.Minute
	}
	v, _ := b.settings.GetSetting("default_browser_idle_minutes")
	d, ok := parseMinutes(v)
	if !ok {
		return 15 * time.Minute
	}
	return d
}

// lastSpawnError reads the persisted browser session row to surface the
// terminal error from a finished spawn task.
func (b *BrowserBridge) lastSpawnError(instanceID uint) error {
	s, err := database.GetBrowserSession(instanceID)
	if err != nil {
		return err
	}
	if s.Status == "running" {
		return nil
	}
	if s.ErrorMsg != "" {
		return errors.New(s.ErrorMsg)
	}
	return fmt.Errorf("browser session for instance %d ended in status %q", instanceID, s.Status)
}

func (b *BrowserBridge) makeRollback(instanceID uint) taskmanager.OnCancel {
	return func(ctx context.Context) {
		if err := b.provider.StopSession(ctx, instanceID); err != nil {
			log.Printf("browserprov: rollback StopSession instance=%d: %v", instanceID, err)
		}
		_ = database.UpdateBrowserSessionStatus(instanceID, "stopped", "spawn canceled")
	}
}

func (b *BrowserBridge) activityFlusher(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.flushActivity()
		}
	}
}

func (b *BrowserBridge) flushActivity() {
	b.activityMu.Lock()
	pending := b.pendingTouches
	b.pendingTouches = make(map[uint]time.Time)
	b.activityMu.Unlock()
	for instanceID := range pending {
		if err := database.TouchBrowserSession(instanceID); err != nil {
			log.Printf("browserprov: flush touch instance=%d: %v", instanceID, err)
		}
	}
}

func (b *BrowserBridge) idleReaper(ctx context.Context) {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.reapOnce(ctx)
		}
	}
}

func (b *BrowserBridge) reapOnce(ctx context.Context) {
	cutoff := time.Now().UTC().Add(-b.idleTimeout())
	rows, err := database.ListIdleBrowserSessions(cutoff)
	if err != nil {
		log.Printf("browserprov: list idle sessions: %v", err)
		return
	}
	for _, row := range rows {
		// Skip if a spawn is in flight (avoids killing a freshly-restarted pod).
		b.spawnMu.Lock()
		_, busy := b.inflight[row.InstanceID]
		b.spawnMu.Unlock()
		if busy {
			continue
		}
		if err := b.provider.StopSession(ctx, row.InstanceID); err != nil {
			log.Printf("browserprov: reap StopSession instance=%d: %v", row.InstanceID, err)
			continue
		}
		_ = database.UpdateBrowserSessionStatus(row.InstanceID, "stopped", "")
	}
}

// parseMinutes accepts a plain integer string (minutes) and returns a
// duration. Returns ok=false if the value is missing or unparseable.
func parseMinutes(s string) (time.Duration, bool) {
	n, ok := parsePositiveInt(s)
	if !ok {
		return 0, false
	}
	return time.Duration(n) * time.Minute, true
}

func parseSeconds(s string) (time.Duration, bool) {
	n, ok := parsePositiveInt(s)
	if !ok {
		return 0, false
	}
	return time.Duration(n) * time.Second, true
}

func parsePositiveInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	if n <= 0 {
		return 0, false
	}
	return n, true
}
