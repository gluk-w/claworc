package sshterminal

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionManager_CreateSession(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{HistoryLines: 100})
	defer sm.Stop()

	ms, err := sm.CreateSession(client, 1, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if ms.ID == "" {
		t.Error("session ID is empty")
	}
	if ms.InstanceID != 1 {
		t.Errorf("InstanceID = %d, want 1", ms.InstanceID)
	}
	if ms.Shell != "/bin/bash" {
		t.Errorf("Shell = %q, want /bin/bash", ms.Shell)
	}
}

func TestSessionManager_GetSession(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{})
	defer sm.Stop()

	ms, err := sm.CreateSession(client, 1, "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got := sm.GetSession(ms.ID)
	if got == nil {
		t.Fatal("GetSession returned nil")
	}
	if got.ID != ms.ID {
		t.Errorf("ID = %q, want %q", got.ID, ms.ID)
	}

	if sm.GetSession("nonexistent") != nil {
		t.Error("GetSession should return nil for nonexistent ID")
	}
}

func TestSessionManager_ListSessions(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{})
	defer sm.Stop()

	// Create sessions for two different instances
	ms1, _ := sm.CreateSession(client, 1, "")
	sm.CreateSession(client, 1, "")
	sm.CreateSession(client, 2, "")

	inst1Sessions := sm.ListSessions(1)
	if len(inst1Sessions) != 2 {
		t.Errorf("instance 1 sessions = %d, want 2", len(inst1Sessions))
	}

	inst2Sessions := sm.ListSessions(2)
	if len(inst2Sessions) != 1 {
		t.Errorf("instance 2 sessions = %d, want 1", len(inst2Sessions))
	}

	// Verify no cross-contamination
	for _, s := range inst1Sessions {
		if s.InstanceID != 1 {
			t.Errorf("instance 1 session has InstanceID = %d", s.InstanceID)
		}
	}

	_ = ms1 // reference to prevent unused
}

func TestSessionManager_CloseSession(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{})
	defer sm.Stop()

	ms, _ := sm.CreateSession(client, 1, "")

	if err := sm.CloseSession(ms.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	if sm.GetSession(ms.ID) != nil {
		t.Error("session should be removed after close")
	}

	// Closing nonexistent should return error
	if err := sm.CloseSession("nonexistent"); err == nil {
		t.Error("expected error closing nonexistent session")
	}
}

func TestSessionManager_CloseAllForInstance(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{})
	defer sm.Stop()

	sm.CreateSession(client, 1, "")
	sm.CreateSession(client, 1, "")
	ms3, _ := sm.CreateSession(client, 2, "")

	sm.CloseAllForInstance(1)

	if len(sm.ListSessions(1)) != 0 {
		t.Error("instance 1 should have no sessions after CloseAllForInstance")
	}

	// Instance 2 should be unaffected
	if sm.GetSession(ms3.ID) == nil {
		t.Error("instance 2 session should still exist")
	}
}

func TestSessionManager_MultipleConcurrentSessions(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{HistoryLines: 100})
	defer sm.Stop()

	// Create multiple sessions on the same instance
	ms1, err := sm.CreateSession(client, 1, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateSession(1): %v", err)
	}
	ms2, err := sm.CreateSession(client, 1, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateSession(2): %v", err)
	}

	// Both should be independently functional
	// Wait for initial PTY:true from both
	waitForDone := func(ms *ManagedSession, marker string) {
		deadline := time.After(3 * time.Second)
		for {
			select {
			case <-deadline:
				t.Fatalf("timeout waiting for output in session %s", ms.ID)
			case <-time.After(50 * time.Millisecond):
				if ms.history != nil && strings.Contains(string(ms.history.Bytes()), marker) {
					return
				}
			}
		}
	}

	waitForDone(ms1, "PTY:true")
	waitForDone(ms2, "PTY:true")

	// Send different data to each
	ms1.WriteInput([]byte("session_one"))
	ms2.WriteInput([]byte("session_two"))

	waitForDone(ms1, "echo:session_one")
	waitForDone(ms2, "echo:session_two")

	// Verify independence
	hist1 := string(ms1.history.Bytes())
	hist2 := string(ms2.history.Bytes())

	if strings.Contains(hist1, "session_two") {
		t.Error("session 1 received session 2 data")
	}
	if strings.Contains(hist2, "session_one") {
		t.Error("session 2 received session 1 data")
	}
}

func TestManagedSession_AttachDetach(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{HistoryLines: 100})
	defer sm.Stop()

	ms, _ := sm.CreateSession(client, 1, "/bin/bash")

	if ms.IsAttached() {
		t.Error("new session should not be attached")
	}

	var buf bytes.Buffer
	history := ms.Attach(&buf)

	if !ms.IsAttached() {
		t.Error("session should be attached after Attach()")
	}

	// History should contain initial PTY:true output (given enough time)
	_ = history

	ms.Detach()

	if ms.IsAttached() {
		t.Error("session should not be attached after Detach()")
	}
}

func TestManagedSession_HistoryReplay(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{HistoryLines: 100})
	defer sm.Stop()

	ms, _ := sm.CreateSession(client, 1, "/bin/bash")

	// Wait for PTY:true to appear in history
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for PTY:true in history")
		case <-time.After(50 * time.Millisecond):
			if ms.history != nil && strings.Contains(string(ms.history.Bytes()), "PTY:true") {
				goto foundPTY
			}
		}
	}
foundPTY:

	// Send some data so it's buffered
	ms.WriteInput([]byte("test_data"))
	deadline = time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timeout waiting for echo in history")
		case <-time.After(50 * time.Millisecond):
			if strings.Contains(string(ms.history.Bytes()), "echo:test_data") {
				goto foundEcho
			}
		}
	}
foundEcho:

	// Now attach and get history replay
	var buf bytes.Buffer
	history := ms.Attach(&buf)
	defer ms.Detach()

	if !strings.Contains(string(history), "PTY:true") {
		t.Error("history replay missing PTY:true")
	}
	if !strings.Contains(string(history), "echo:test_data") {
		t.Error("history replay missing echo:test_data")
	}
}

func TestManagedSession_WriteInputAndResize(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{HistoryLines: 100})
	defer sm.Stop()

	ms, _ := sm.CreateSession(client, 1, "/bin/bash")

	// Wait for PTY:true
	waitForHistory(t, ms, "PTY:true", 3*time.Second)

	// Test WriteInput
	if _, err := ms.WriteInput([]byte("hello")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	waitForHistory(t, ms, "echo:hello", 3*time.Second)

	// Test Resize
	if err := ms.Resize(120, 40); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	waitForHistory(t, ms, "resize:120x40", 3*time.Second)
}

func TestManagedSession_DoneChannel(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{})
	defer sm.Stop()

	ms, _ := sm.CreateSession(client, 1, "/bin/bash")

	// Done channel should not be closed yet
	select {
	case <-ms.Done():
		t.Fatal("Done() closed prematurely")
	default:
	}

	// Close the session and verify Done() fires
	sm.CloseSession(ms.ID)

	select {
	case <-ms.Done():
		// expected
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for Done() after close")
	}
}

func TestSessionManager_Recording(t *testing.T) {
	client := newTestClient(t)
	recordDir := t.TempDir()

	sm := NewSessionManager(SessionManagerConfig{
		HistoryLines: 100,
		RecordingDir: recordDir,
	})
	defer sm.Stop()

	ms, err := sm.CreateSession(client, 42, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Wait for PTY output to be recorded
	waitForHistory(t, ms, "PTY:true", 3*time.Second)

	// Send some input
	ms.WriteInput([]byte("recorded_command"))
	waitForHistory(t, ms, "echo:recorded_command", 3*time.Second)

	// Close session to flush recording
	sm.CloseSession(ms.ID)

	// Verify recording file exists and contains data
	entries, err := os.ReadDir(recordDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 recording file, got %d", len(entries))
	}

	content, err := os.ReadFile(filepath.Join(recordDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if !strings.Contains(string(content), "PTY:true") {
		t.Error("recording missing PTY:true")
	}
	if !strings.Contains(string(content), "recorded_command") {
		t.Error("recording missing echoed command")
	}

	// Verify filename format
	name := entries[0].Name()
	if !strings.HasPrefix(name, "terminal_42_") {
		t.Errorf("recording filename %q doesn't have expected prefix", name)
	}
	if !strings.HasSuffix(name, ".log") {
		t.Errorf("recording filename %q doesn't have .log suffix", name)
	}
}

func TestSessionManager_NoRecordingWhenDisabled(t *testing.T) {
	client := newTestClient(t)

	sm := NewSessionManager(SessionManagerConfig{
		RecordingDir: "", // disabled
	})
	defer sm.Stop()

	ms, err := sm.CreateSession(client, 1, "/bin/bash")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ms.mu.Lock()
	hasRecording := ms.recording != nil
	ms.mu.Unlock()

	if hasRecording {
		t.Error("recording should be nil when disabled")
	}
}

func TestSessionManager_ConcurrentCreateClose(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{})
	defer sm.Stop()

	var wg sync.WaitGroup
	const count = 10

	ids := make([]string, count)
	var mu sync.Mutex

	// Create sessions concurrently
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ms, err := sm.CreateSession(client, 1, "")
			if err != nil {
				t.Errorf("CreateSession(%d): %v", idx, err)
				return
			}
			mu.Lock()
			ids[idx] = ms.ID
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	sessions := sm.ListSessions(1)
	if len(sessions) != count {
		t.Errorf("session count = %d, want %d", len(sessions), count)
	}

	// Close them all concurrently
	for _, id := range ids {
		wg.Add(1)
		go func(sid string) {
			defer wg.Done()
			sm.CloseSession(sid)
		}(id)
	}
	wg.Wait()

	if len(sm.ListSessions(1)) != 0 {
		t.Error("expected no sessions after closing all")
	}
}

func TestSessionManager_Stop(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{})

	sm.CreateSession(client, 1, "")
	sm.CreateSession(client, 2, "")

	sm.Stop()

	if len(sm.ListSessions(1)) != 0 {
		t.Error("Stop() should clear all sessions")
	}
	if len(sm.ListSessions(2)) != 0 {
		t.Error("Stop() should clear all sessions")
	}
}

func TestManagedSession_OutputToAttachedWriter(t *testing.T) {
	client := newTestClient(t)
	sm := NewSessionManager(SessionManagerConfig{HistoryLines: 100})
	defer sm.Stop()

	ms, _ := sm.CreateSession(client, 1, "/bin/bash")

	var buf bytes.Buffer
	ms.Attach(&buf)
	defer ms.Detach()

	// Wait for output to arrive through the attached writer
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for output in writer, got: %q", buf.String())
		case <-time.After(50 * time.Millisecond):
			if strings.Contains(buf.String(), "PTY:true") {
				return
			}
		}
	}
}

// waitForHistory polls the session's scrollback buffer until the target string appears.
func waitForHistory(t *testing.T, ms *ManagedSession, target string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			var hist string
			if ms.history != nil {
				hist = string(ms.history.Bytes())
			}
			t.Fatalf("timeout waiting for %q in history, got: %q", target, hist)
		case <-time.After(50 * time.Millisecond):
			if ms.history != nil && strings.Contains(string(ms.history.Bytes()), target) {
				return
			}
		}
	}
}
