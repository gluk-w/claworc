package sshterminal

import (
	"context"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

func TestSessionManager_CreateSession(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			ch.Write([]byte("hello\r\n"))
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()

	ms, err := mgr.CreateSession(context.Background(), client, 1, 10, "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if ms.ID == "" {
		t.Error("expected non-empty session ID")
	}
	if ms.InstanceID != 1 {
		t.Errorf("expected instance ID 1, got %d", ms.InstanceID)
	}
	if ms.UserID != 10 {
		t.Errorf("expected user ID 10, got %d", ms.UserID)
	}
	if ms.Shell != DefaultShell {
		t.Errorf("expected shell %q, got %q", DefaultShell, ms.Shell)
	}
	if ms.State() != SessionActive {
		t.Errorf("expected state active, got %s", ms.State())
	}

	// Wait for output to appear in scrollback
	time.Sleep(100 * time.Millisecond)
	if ms.Scrollback.Len() == 0 {
		t.Error("expected scrollback to have data")
	}

	ms.Close()
	if ms.State() != SessionClosed {
		t.Errorf("expected state closed after Close(), got %s", ms.State())
	}
}

func TestSessionManager_MultipleSessions(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()

	// Create 3 sessions on the same instance
	sessions := make([]*ManagedSession, 3)
	for i := 0; i < 3; i++ {
		ms, err := mgr.CreateSession(context.Background(), client, 1, uint(i+1), "")
		if err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
		sessions[i] = ms
	}

	if mgr.SessionCount() != 3 {
		t.Errorf("expected 3 sessions, got %d", mgr.SessionCount())
	}

	// List sessions for instance
	list := mgr.ListSessions(1, false)
	if len(list) != 3 {
		t.Errorf("expected 3 sessions in list, got %d", len(list))
	}

	// List sessions for different instance should be empty
	list2 := mgr.ListSessions(999, false)
	if len(list2) != 0 {
		t.Errorf("expected 0 sessions for instance 999, got %d", len(list2))
	}

	// Close all
	for _, ms := range sessions {
		ms.Close()
	}
}

func TestSessionManager_GetSession(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()
	ms, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")
	defer ms.Close()

	// Should find by ID
	found := mgr.GetSession(ms.ID)
	if found == nil {
		t.Fatal("expected to find session by ID")
	}
	if found.ID != ms.ID {
		t.Errorf("expected ID %q, got %q", ms.ID, found.ID)
	}

	// Should not find non-existent
	notFound := mgr.GetSession("non-existent")
	if notFound != nil {
		t.Error("expected nil for non-existent session")
	}
}

func TestSessionManager_CloseSession(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()
	ms, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")

	if err := mgr.CloseSession(ms.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	if ms.State() != SessionClosed {
		t.Errorf("expected closed state, got %s", ms.State())
	}

	// Close non-existent should error
	if err := mgr.CloseSession("non-existent"); err == nil {
		t.Error("expected error for non-existent session")
	}
}

func TestSessionManager_SessionDetachReattach(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			ch.Write([]byte("initial output\r\n"))
			buf := make([]byte, 4096)
			for {
				n, err := ch.Read(buf)
				if n > 0 {
					ch.Write(buf[:n])
				}
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()
	ms, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")

	// Wait for initial output
	time.Sleep(100 * time.Millisecond)

	// Simulate detach
	ms.SetState(SessionDetached)
	if ms.State() != SessionDetached {
		t.Errorf("expected detached state, got %s", ms.State())
	}

	// Send some input while detached (output goes to scrollback)
	ms.Terminal.Stdin.Write([]byte("while detached\n"))
	time.Sleep(100 * time.Millisecond)

	// Simulate reattach
	ms.SetState(SessionActive)
	scrollback := string(ms.Scrollback.Snapshot())
	if !strings.Contains(scrollback, "initial output") {
		t.Errorf("scrollback missing initial output: %q", scrollback)
	}
	if !strings.Contains(scrollback, "while detached") {
		t.Errorf("scrollback missing detached output: %q", scrollback)
	}

	ms.Close()
}

func TestSessionManager_CloseAllForInstance(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()

	// Create sessions for two instances
	ms1a, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")
	ms1b, _ := mgr.CreateSession(context.Background(), client, 1, 2, "")
	ms2a, _ := mgr.CreateSession(context.Background(), client, 2, 1, "")

	mgr.CloseAllForInstance(1)

	if ms1a.State() != SessionClosed {
		t.Error("expected session 1a to be closed")
	}
	if ms1b.State() != SessionClosed {
		t.Error("expected session 1b to be closed")
	}
	if ms2a.State() == SessionClosed {
		t.Error("session 2a should not be closed")
	}

	ms2a.Close()
}

func TestSessionManager_ActiveCount(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()
	ms1, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")
	ms2, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")

	if mgr.ActiveCount() != 2 {
		t.Errorf("expected 2 active, got %d", mgr.ActiveCount())
	}

	ms1.SetState(SessionDetached)
	if mgr.ActiveCount() != 2 {
		t.Errorf("expected 2 active (detached counts), got %d", mgr.ActiveCount())
	}

	ms1.Close()
	if mgr.ActiveCount() != 1 {
		t.Errorf("expected 1 active after close, got %d", mgr.ActiveCount())
	}

	ms2.Close()
}

func TestSessionManager_RecordingEnabled(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			ch.Write([]byte("recorded output\r\n"))
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()
	mgr.RecordingEnabled = true

	ms, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")
	defer ms.Close()

	if ms.Recording == nil {
		t.Fatal("expected recording to be non-nil when enabled")
	}

	// Wait for output
	time.Sleep(100 * time.Millisecond)

	if ms.Recording.EntryCount() == 0 {
		t.Error("expected recording to have entries")
	}

	entries := ms.Recording.Entries()
	hasOutput := false
	for _, e := range entries {
		if e.Type == "o" && strings.Contains(e.Data, "recorded output") {
			hasOutput = true
			break
		}
	}
	if !hasOutput {
		t.Error("recording missing expected output")
	}
}

func TestSessionManager_RecordingDisabled(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()
	// RecordingEnabled defaults to false

	ms, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")
	defer ms.Close()

	if ms.Recording != nil {
		t.Error("expected recording to be nil when disabled")
	}
}

func TestSessionManager_CleanupIdle(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()
	mgr.IdleTimeout = 50 * time.Millisecond

	ms, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")
	ms.SetState(SessionDetached)

	// Before timeout
	cleaned := mgr.CleanupIdle()
	if cleaned != 0 {
		t.Errorf("expected 0 cleaned before timeout, got %d", cleaned)
	}

	// Wait past timeout
	time.Sleep(100 * time.Millisecond)

	cleaned = mgr.CleanupIdle()
	if cleaned != 1 {
		t.Errorf("expected 1 cleaned after timeout, got %d", cleaned)
	}

	if mgr.SessionCount() != 0 {
		t.Errorf("expected 0 sessions after cleanup, got %d", mgr.SessionCount())
	}
}

func TestSessionManager_ListActiveOnly(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()
	ms1, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")
	ms2, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")

	ms1.Close()

	// All sessions (including closed)
	all := mgr.ListSessions(1, false)
	if len(all) != 2 {
		t.Errorf("expected 2 total sessions, got %d", len(all))
	}

	// Active only
	active := mgr.ListSessions(1, true)
	if len(active) != 1 {
		t.Errorf("expected 1 active session, got %d", len(active))
	}

	ms2.Close()
}

func TestSessionManager_RemoveSession(t *testing.T) {
	client, cleanup := startPTYServer(t, ptyHandler{
		onExec: func(cmd string, ch gossh.Channel) {
			buf := make([]byte, 1)
			for {
				_, err := ch.Read(buf)
				if err != nil {
					break
				}
			}
		},
	})
	defer cleanup()

	mgr := NewSessionManager()
	ms, _ := mgr.CreateSession(context.Background(), client, 1, 1, "")
	ms.Close()

	mgr.RemoveSession(ms.ID)
	if mgr.SessionCount() != 0 {
		t.Errorf("expected 0 sessions after remove, got %d", mgr.SessionCount())
	}

	// Remove non-existent should be safe
	mgr.RemoveSession("non-existent")
}
