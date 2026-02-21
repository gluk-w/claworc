package sshterminal

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

// SessionState represents the lifecycle state of a managed terminal session.
type SessionState string

const (
	// SessionActive means the SSH session is alive and a WebSocket is connected.
	SessionActive SessionState = "active"
	// SessionDetached means the SSH session is alive but no WebSocket is attached.
	SessionDetached SessionState = "detached"
	// SessionClosed means the SSH session has ended.
	SessionClosed SessionState = "closed"
)

// ManagedSession wraps a TerminalSession with metadata for multi-session
// management and session persistence. It maintains a scrollback buffer so
// that disconnected users can reconnect and see output produced while away.
//
// Lifecycle:
//  1. Created via SessionManager.CreateSession() → state=Active
//  2. WebSocket disconnects → state=Detached (SSH session stays alive)
//  3. WebSocket reconnects → state=Active (scrollback replayed)
//  4. SSH session ends or explicit close → state=Closed
type ManagedSession struct {
	// ID is a unique identifier for this session (UUID).
	ID string
	// InstanceID is the database ID of the instance this session belongs to.
	InstanceID uint
	// UserID is the database ID of the user who created this session.
	UserID uint
	// Shell is the shell command used for this session.
	Shell string
	// CreatedAt is when the session was created.
	CreatedAt time.Time
	// ClosedAt is when the session was closed (nil if still active).
	ClosedAt *time.Time

	// Terminal is the underlying SSH terminal session.
	Terminal *TerminalSession
	// Scrollback stores terminal output for replay on reconnection.
	Scrollback *ScrollbackBuffer
	// Recording captures timestamped I/O for audit (nil if disabled).
	Recording *SessionRecording

	mu           sync.Mutex
	state        SessionState
	lastActivity time.Time
	// stdoutDone is closed when the SSH stdout relay goroutine exits.
	stdoutDone chan struct{}
}

// State returns the current session state.
func (ms *ManagedSession) State() SessionState {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.state
}

// SetState updates the session state.
func (ms *ManagedSession) SetState(state SessionState) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.state = state
	ms.lastActivity = time.Now()
}

// LastActivity returns the time of the last state change or activity.
func (ms *ManagedSession) LastActivity() time.Time {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return ms.lastActivity
}

// Close terminates the managed session and its underlying SSH session.
func (ms *ManagedSession) Close() {
	ms.mu.Lock()
	if ms.state == SessionClosed {
		ms.mu.Unlock()
		return
	}
	ms.state = SessionClosed
	now := time.Now()
	ms.ClosedAt = &now
	ms.mu.Unlock()

	ms.Terminal.Close()
	ms.Scrollback.Close()
}

// StdoutDone returns a channel that is closed when the SSH stdout relay exits.
func (ms *ManagedSession) StdoutDone() <-chan struct{} {
	return ms.stdoutDone
}

// SessionManager tracks all active terminal sessions across all instances.
// It provides session creation, lookup, reconnection, and cleanup.
//
// Key capabilities:
//   - Multiple concurrent sessions per instance (SSH multiplexing)
//   - Session persistence across WebSocket disconnects
//   - Scrollback buffer for reconnection replay
//   - Optional session recording for audit
//   - Automatic cleanup of idle detached sessions
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*ManagedSession // session ID → session

	// RecordingEnabled controls whether new sessions have recording enabled.
	RecordingEnabled bool
	// ScrollbackSize is the max scrollback buffer size for new sessions.
	ScrollbackSize int
	// IdleTimeout is how long a detached session stays alive before cleanup.
	// Zero means no automatic cleanup.
	IdleTimeout time.Duration
}

// DefaultIdleTimeout is the default duration after which detached sessions
// are automatically cleaned up. Set to 30 minutes.
const DefaultIdleTimeout = 30 * time.Minute

// NewSessionManager creates a new SessionManager with sensible defaults.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions:       make(map[string]*ManagedSession),
		ScrollbackSize: defaultScrollbackSize,
		IdleTimeout:    DefaultIdleTimeout,
	}
}

// CreateSession creates a new managed terminal session over the given SSH client.
// It starts the SSH shell, initializes the scrollback buffer, and begins
// relaying SSH stdout into the buffer.
func (sm *SessionManager) CreateSession(ctx context.Context, sshClient *ssh.Client, instanceID, userID uint, shell string) (*ManagedSession, error) {
	if err := ValidateShell(shell); err != nil {
		return nil, fmt.Errorf("validate shell: %w", err)
	}

	terminal, err := CreateInteractiveSession(sshClient, shell)
	if err != nil {
		return nil, fmt.Errorf("create terminal session: %w", err)
	}

	if shell == "" {
		shell = DefaultShell
	}

	ms := &ManagedSession{
		ID:           uuid.New().String(),
		InstanceID:   instanceID,
		UserID:       userID,
		Shell:        shell,
		CreatedAt:    time.Now(),
		Terminal:     terminal,
		Scrollback:   NewScrollbackBuffer(sm.ScrollbackSize),
		state:        SessionActive,
		lastActivity: time.Now(),
		stdoutDone:   make(chan struct{}),
	}

	if sm.RecordingEnabled {
		ms.Recording = NewSessionRecording(0)
	}

	// Start SSH stdout → scrollback buffer relay
	go sm.relayStdout(ms)

	sm.mu.Lock()
	sm.sessions[ms.ID] = ms
	sm.mu.Unlock()

	log.Printf("[session-mgr] created session %s for instance %d (user %d, shell %s)",
		ms.ID, instanceID, userID, shell)

	return ms, nil
}

// relayStdout reads from the SSH session stdout and writes to the scrollback
// buffer (and recording if enabled). This goroutine runs for the lifetime
// of the SSH session regardless of WebSocket connections.
func (sm *SessionManager) relayStdout(ms *ManagedSession) {
	defer close(ms.stdoutDone)
	buf := make([]byte, 32*1024)
	for {
		n, err := ms.Terminal.Stdout.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			ms.Scrollback.Write(data)
			if ms.Recording != nil {
				ms.Recording.RecordOutput(data)
			}
		}
		if err != nil {
			log.Printf("[session-mgr] session %s stdout ended: %v", ms.ID, err)
			ms.Close()
			return
		}
	}
}

// GetSession returns a managed session by ID, or nil if not found.
func (sm *SessionManager) GetSession(sessionID string) *ManagedSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[sessionID]
}

// ListSessions returns all sessions for a given instance, optionally
// filtering to only active/detached sessions.
func (sm *SessionManager) ListSessions(instanceID uint, activeOnly bool) []*ManagedSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var result []*ManagedSession
	for _, ms := range sm.sessions {
		if ms.InstanceID != instanceID {
			continue
		}
		if activeOnly && ms.State() == SessionClosed {
			continue
		}
		result = append(result, ms)
	}
	return result
}

// CloseSession closes a specific session by ID.
func (sm *SessionManager) CloseSession(sessionID string) error {
	sm.mu.Lock()
	ms, ok := sm.sessions[sessionID]
	sm.mu.Unlock()

	if !ok {
		return fmt.Errorf("session %q not found", sessionID)
	}

	ms.Close()
	log.Printf("[session-mgr] closed session %s", sessionID)
	return nil
}

// RemoveSession removes a closed session from the manager.
func (sm *SessionManager) RemoveSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}

// CloseAllForInstance closes all sessions for a given instance.
func (sm *SessionManager) CloseAllForInstance(instanceID uint) {
	sm.mu.RLock()
	var toClose []*ManagedSession
	for _, ms := range sm.sessions {
		if ms.InstanceID == instanceID {
			toClose = append(toClose, ms)
		}
	}
	sm.mu.RUnlock()

	for _, ms := range toClose {
		ms.Close()
	}
}

// CleanupIdle removes detached sessions that have been idle longer than
// the configured IdleTimeout. Should be called periodically.
func (sm *SessionManager) CleanupIdle() int {
	if sm.IdleTimeout <= 0 {
		return 0
	}

	sm.mu.RLock()
	var toClean []*ManagedSession
	cutoff := time.Now().Add(-sm.IdleTimeout)
	for _, ms := range sm.sessions {
		if ms.State() == SessionDetached && ms.LastActivity().Before(cutoff) {
			toClean = append(toClean, ms)
		}
	}
	sm.mu.RUnlock()

	for _, ms := range toClean {
		log.Printf("[session-mgr] cleaning up idle session %s (detached since %s)",
			ms.ID, ms.LastActivity().Format(time.RFC3339))
		ms.Close()
		sm.mu.Lock()
		delete(sm.sessions, ms.ID)
		sm.mu.Unlock()
	}

	// Also remove closed sessions older than the idle timeout
	sm.mu.Lock()
	for id, ms := range sm.sessions {
		if ms.State() == SessionClosed && ms.LastActivity().Before(cutoff) {
			delete(sm.sessions, id)
		}
	}
	sm.mu.Unlock()

	return len(toClean)
}

// SessionCount returns the total number of tracked sessions.
func (sm *SessionManager) SessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// ActiveCount returns the number of active or detached sessions.
func (sm *SessionManager) ActiveCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	count := 0
	for _, ms := range sm.sessions {
		state := ms.State()
		if state == SessionActive || state == SessionDetached {
			count++
		}
	}
	return count
}
