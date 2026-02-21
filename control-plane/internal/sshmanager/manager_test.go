package sshmanager

import (
	"testing"
)

func TestNewSSHManager(t *testing.T) {
	m := NewSSHManager()
	if m == nil {
		t.Fatal("NewSSHManager returned nil")
	}
	if m.clients == nil {
		t.Error("clients map not initialized")
	}
}

func TestHasClientEmpty(t *testing.T) {
	m := NewSSHManager()
	if m.HasClient("nonexistent") {
		t.Error("HasClient should return false for nonexistent instance")
	}
}

func TestGetClientMissing(t *testing.T) {
	m := NewSSHManager()
	_, err := m.GetClient("nonexistent")
	if err == nil {
		t.Error("GetClient should return error for nonexistent instance")
	}
}

func TestRemoveClientMissing(t *testing.T) {
	m := NewSSHManager()
	client := m.RemoveClient("nonexistent")
	if client != nil {
		t.Error("RemoveClient should return nil for nonexistent instance")
	}
}

func TestSetAndHasClient(t *testing.T) {
	m := NewSSHManager()
	// We can't create a real *ssh.Client without a server, but we can test the map operations
	// by using SetClient with nil (which is a valid pointer value to store)
	m.SetClient("test-instance", nil)
	if !m.HasClient("test-instance") {
		t.Error("HasClient should return true after SetClient")
	}
}

func TestRemoveClient(t *testing.T) {
	m := NewSSHManager()
	m.SetClient("test-instance", nil)
	m.RemoveClient("test-instance")
	if m.HasClient("test-instance") {
		t.Error("HasClient should return false after RemoveClient")
	}
}

func TestCloseAllEmpty(t *testing.T) {
	m := NewSSHManager()
	// Should not panic on empty manager
	m.CloseAll()
	if len(m.clients) != 0 {
		t.Errorf("expected 0 clients after CloseAll, got %d", len(m.clients))
	}
}

func TestCloseAllClearsClients(t *testing.T) {
	m := NewSSHManager()
	// Store nil clients (we can't create real SSH clients without a server)
	m.SetClient("instance-a", nil)
	m.SetClient("instance-b", nil)

	if len(m.clients) != 2 {
		t.Fatalf("expected 2 clients before CloseAll, got %d", len(m.clients))
	}

	m.CloseAll()

	if len(m.clients) != 0 {
		t.Errorf("expected 0 clients after CloseAll, got %d", len(m.clients))
	}
	if m.HasClient("instance-a") {
		t.Error("HasClient should return false for instance-a after CloseAll")
	}
	if m.HasClient("instance-b") {
		t.Error("HasClient should return false for instance-b after CloseAll")
	}
}

func TestCloseAllIdempotent(t *testing.T) {
	m := NewSSHManager()
	m.SetClient("test", nil)
	m.CloseAll()
	// Calling again should not panic
	m.CloseAll()
	if len(m.clients) != 0 {
		t.Errorf("expected 0 clients after double CloseAll, got %d", len(m.clients))
	}
}
