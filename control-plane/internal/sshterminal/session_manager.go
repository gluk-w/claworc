// Package sshterminal provides interactive terminal sessions over SSH connections.
//
// SessionManager tracks multiple concurrent terminal sessions per instance,
// supports session persistence (reconnect after WebSocket disconnect), optional
// scrollback history, and optional audit recording to disk.
//
// Architecture:
//   - Each session has a unique ID and belongs to one instance (keyed by instance ID).
//   - Sessions remain alive after the WebSocket disconnects ("detached" state),
//     allowing clients to reconnect and resume where they left off.
//   - A scrollback buffer captures recent output so reconnecting clients can
//     replay missed content. Buffer size is configurable (0 disables).
//   - When recording is enabled, all session output is additionally written
//     to a timestamped file on disk for audit purposes.
//   - Idle detached sessions are reaped after a configurable timeout.
//
// Limitations:
//   - Sessions are in-memory only; a control-plane restart loses all sessions.
//   - Recording files are append-only raw terminal output (no timing metadata).
//   - The scrollback buffer stores raw bytes; very long lines may consume
//     disproportionate memory.
package sshterminal

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

// SessionManagerConfig holds configuration for the SessionManager.
type SessionManagerConfig struct {
	// HistoryLines is the number of output lines kept in the scrollback buffer.
	// Set to 0 to disable history.
	HistoryLines int

	// RecordingDir is the directory where session recordings are written.
	// Empty string disables recording.
	RecordingDir string

	// IdleTimeout is how long a detached session stays alive before being reaped.
	IdleTimeout time.Duration
}

// ManagedSession wraps a TerminalSession with persistence and history support.
//
// A ManagedSession transitions between two states:
//   - Attached: a WebSocket client is actively reading/writing. Output flows
//     from SSH stdout through the OutputReader pipe to the WebSocket relay.
//   - Detached: no client is connected. Output is still consumed (to prevent
//     the SSH channel from blocking) and stored in the scrollback buffer,
//     but there is no active reader on the other end.
//
// Callers use Attach/Detach to transition between states and WriteTo to relay
// output to a writer (typically the WebSocket connection).
type ManagedSession struct {
	ID         string    `json:"id"`
	InstanceID uint      `json:"instance_id"`
	CreatedAt  time.Time `json:"created_at"`
	Shell      string    `json:"shell"`

	// terminal is the underlying SSH terminal session.
	terminal *TerminalSession

	// history stores the scrollback buffer (ring buffer of raw bytes).
	history *scrollbackBuffer

	// recording is the optional audit file writer. Nil if recording is disabled.
	recording *os.File

	// mu protects attached, detachedAt, outputWriter, and done.
	mu           sync.Mutex
	attached     bool
	detachedAt   time.Time
	outputWriter io.Writer // current writer for output (set during Attach)
	done         chan struct{}

	// closed tracks whether the session has been fully closed.
	closed bool
}

// SessionManager tracks all active terminal sessions across instances.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*ManagedSession // keyed by session ID
	config   SessionManagerConfig

	stopReaper chan struct{}
}

// NewSessionManager creates a SessionManager with the given configuration and
// starts a background goroutine that reaps idle detached sessions.
func NewSessionManager(cfg SessionManagerConfig) *SessionManager {
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 30 * time.Minute
	}
	sm := &SessionManager{
		sessions:   make(map[string]*ManagedSession),
		config:     cfg,
		stopReaper: make(chan struct{}),
	}
	go sm.reapLoop()
	return sm
}

// CreateSession opens a new SSH terminal session for the given instance and
// registers it with the manager. The session starts in a detached state; call
// Attach to connect a WebSocket client. The shell parameter is validated
// against AllowedShells before the session is created.
func (sm *SessionManager) CreateSession(client *ssh.Client, instanceID uint, shell string) (*ManagedSession, error) {
	ts, err := CreateInteractiveSession(client, shell)
	if err != nil {
		return nil, err
	}

	ms := &ManagedSession{
		ID:         uuid.New().String(),
		InstanceID: instanceID,
		CreatedAt:  time.Now(),
		Shell:      shell,
		terminal:   ts,
		done:       make(chan struct{}),
	}

	if sm.config.HistoryLines > 0 {
		ms.history = newScrollbackBuffer(sm.config.HistoryLines)
	}

	if sm.config.RecordingDir != "" {
		if err := os.MkdirAll(sm.config.RecordingDir, 0750); err != nil {
			ts.Close()
			return nil, fmt.Errorf("create recording dir: %w", err)
		}
		filename := fmt.Sprintf("terminal_%d_%s_%s.log",
			instanceID,
			ms.ID[:8],
			ms.CreatedAt.Format("20060102_150405"),
		)
		f, err := os.OpenFile(
			filepath.Join(sm.config.RecordingDir, filename),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND,
			0640,
		)
		if err != nil {
			ts.Close()
			return nil, fmt.Errorf("open recording file: %w", err)
		}
		ms.recording = f
	}

	// Start the output pump goroutine that continuously reads SSH stdout.
	go ms.pumpOutput()

	sm.mu.Lock()
	sm.sessions[ms.ID] = ms
	sm.mu.Unlock()

	return ms, nil
}

// GetSession returns a session by ID, or nil if not found.
func (sm *SessionManager) GetSession(id string) *ManagedSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[id]
}

// ListSessions returns all sessions for a given instance.
func (sm *SessionManager) ListSessions(instanceID uint) []*ManagedSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var result []*ManagedSession
	for _, ms := range sm.sessions {
		if ms.InstanceID == instanceID {
			result = append(result, ms)
		}
	}
	return result
}

// CloseSession terminates and removes a session.
func (sm *SessionManager) CloseSession(id string) error {
	sm.mu.Lock()
	ms, ok := sm.sessions[id]
	if !ok {
		sm.mu.Unlock()
		return fmt.Errorf("session %s not found", id)
	}
	delete(sm.sessions, id)
	sm.mu.Unlock()

	log.Printf("Terminal session closed: session=%s instance=%d", id, ms.InstanceID)
	return ms.close()
}

// CloseAllForInstance terminates all sessions for a given instance.
func (sm *SessionManager) CloseAllForInstance(instanceID uint) {
	sm.mu.Lock()
	var toClose []*ManagedSession
	for id, ms := range sm.sessions {
		if ms.InstanceID == instanceID {
			toClose = append(toClose, ms)
			delete(sm.sessions, id)
		}
	}
	sm.mu.Unlock()

	for _, ms := range toClose {
		ms.close()
	}
}

// Stop terminates all sessions and stops the reaper goroutine.
func (sm *SessionManager) Stop() {
	close(sm.stopReaper)

	sm.mu.Lock()
	sessions := make([]*ManagedSession, 0, len(sm.sessions))
	for _, ms := range sm.sessions {
		sessions = append(sessions, ms)
	}
	sm.sessions = make(map[string]*ManagedSession)
	sm.mu.Unlock()

	for _, ms := range sessions {
		ms.close()
	}
}

// reapLoop periodically checks for idle detached sessions and closes them.
func (sm *SessionManager) reapLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-sm.stopReaper:
			return
		case <-ticker.C:
			sm.reapIdle()
		}
	}
}

func (sm *SessionManager) reapIdle() {
	sm.mu.Lock()
	var toClose []*ManagedSession
	now := time.Now()
	for id, ms := range sm.sessions {
		ms.mu.Lock()
		idle := !ms.attached && !ms.detachedAt.IsZero() && now.Sub(ms.detachedAt) > sm.config.IdleTimeout
		ms.mu.Unlock()
		if idle {
			toClose = append(toClose, ms)
			delete(sm.sessions, id)
			log.Printf("Reaping idle terminal session %s (instance %d)", id, ms.InstanceID)
		}
	}
	sm.mu.Unlock()

	for _, ms := range toClose {
		ms.close()
	}
}

// Attach connects a writer (typically WebSocket) to receive session output.
// It returns the scrollback history so the client can replay missed output.
func (ms *ManagedSession) Attach(w io.Writer) []byte {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.attached = true
	ms.detachedAt = time.Time{}
	ms.outputWriter = w

	if ms.history != nil {
		return ms.history.Bytes()
	}
	return nil
}

// Detach disconnects the current writer. The session stays alive and continues
// buffering output in the scrollback buffer.
func (ms *ManagedSession) Detach() {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.attached = false
	ms.detachedAt = time.Now()
	ms.outputWriter = nil
}

// IsAttached returns whether a client is currently attached.
func (ms *ManagedSession) IsAttached() bool {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.attached
}

// WriteInput sends data to the terminal's stdin.
func (ms *ManagedSession) WriteInput(data []byte) (int, error) {
	return ms.terminal.Stdin.Write(data)
}

// Resize changes the terminal dimensions.
func (ms *ManagedSession) Resize(cols, rows uint16) error {
	return ms.terminal.Resize(cols, rows)
}

// Done returns a channel that is closed when the session's SSH process exits.
func (ms *ManagedSession) Done() <-chan struct{} {
	return ms.done
}

// pumpOutput continuously reads from SSH stdout and dispatches output to:
//   - The attached writer (if any)
//   - The scrollback buffer (if enabled)
//   - The recording file (if enabled)
//
// This goroutine runs for the lifetime of the session, ensuring the SSH channel
// never blocks even when no client is attached.
func (ms *ManagedSession) pumpOutput() {
	defer close(ms.done)

	buf := make([]byte, 32*1024)
	for {
		n, err := ms.terminal.Stdout.Read(buf)
		if n > 0 {
			data := buf[:n]

			ms.mu.Lock()
			// Write to scrollback buffer
			if ms.history != nil {
				ms.history.Write(data)
			}
			// Write to recording file
			if ms.recording != nil {
				ms.recording.Write(data)
			}
			// Write to attached client
			w := ms.outputWriter
			ms.mu.Unlock()

			if w != nil {
				// Best-effort write to client; errors are handled by the caller
				w.Write(data)
			}
		}
		if err != nil {
			return
		}
	}
}

// close terminates the SSH session and cleans up resources.
func (ms *ManagedSession) close() error {
	ms.mu.Lock()
	if ms.closed {
		ms.mu.Unlock()
		return nil
	}
	ms.closed = true
	rec := ms.recording
	ms.outputWriter = nil
	ms.mu.Unlock()

	if rec != nil {
		rec.Close()
	}
	return ms.terminal.Close()
}
