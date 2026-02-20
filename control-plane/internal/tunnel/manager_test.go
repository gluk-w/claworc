package tunnel

import "testing"

func TestInitManager(t *testing.T) {
	old := Manager
	defer func() { Manager = old }()

	Manager = nil
	InitManager()

	if Manager == nil {
		t.Fatal("InitManager did not set Manager")
	}
}

func TestManagerGetSet(t *testing.T) {
	tm := NewTunnelManager()

	if tc := tm.Get(42); tc != nil {
		t.Fatalf("expected nil for unknown instance, got %v", tc)
	}

	client := &TunnelClient{instanceID: 42}
	tm.Set(42, client)

	got := tm.Get(42)
	if got != client {
		t.Fatalf("Get(42) = %v, want %v", got, client)
	}
}

func TestManagerRemove(t *testing.T) {
	tm := NewTunnelManager()

	client := &TunnelClient{instanceID: 7}
	tm.Set(7, client)
	tm.Remove(7)

	if tc := tm.Get(7); tc != nil {
		t.Fatalf("expected nil after Remove, got %v", tc)
	}
}

func TestManagerRemoveNonexistent(t *testing.T) {
	tm := NewTunnelManager()
	// Should not panic.
	tm.Remove(999)
}
