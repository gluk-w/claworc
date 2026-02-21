package sshtunnel

import (
	"testing"
)

func TestInitGlobal(t *testing.T) {
	// Reset state
	registryMu.Lock()
	globalSSHManager = nil
	globalTunnelManager = nil
	registryMu.Unlock()

	InitGlobal()

	sm := GetSSHManager()
	if sm == nil {
		t.Fatal("GetSSHManager returned nil after InitGlobal")
	}

	tm := GetTunnelManager()
	if tm == nil {
		t.Fatal("GetTunnelManager returned nil after InitGlobal")
	}

	// TunnelManager should reference the same SSHManager
	if tm.sshManager != sm {
		t.Error("TunnelManager should reference the global SSHManager")
	}
}

func TestGetSSHManagerBeforeInit(t *testing.T) {
	registryMu.Lock()
	globalSSHManager = nil
	registryMu.Unlock()

	sm := GetSSHManager()
	if sm != nil {
		t.Error("GetSSHManager should return nil before InitGlobal")
	}
}

func TestGetTunnelManagerBeforeInit(t *testing.T) {
	registryMu.Lock()
	globalTunnelManager = nil
	registryMu.Unlock()

	tm := GetTunnelManager()
	if tm != nil {
		t.Error("GetTunnelManager should return nil before InitGlobal")
	}
}

func TestInitGlobalIdempotent(t *testing.T) {
	registryMu.Lock()
	globalSSHManager = nil
	globalTunnelManager = nil
	registryMu.Unlock()

	InitGlobal()
	sm1 := GetSSHManager()
	tm1 := GetTunnelManager()

	// Calling again creates new instances
	InitGlobal()
	sm2 := GetSSHManager()
	tm2 := GetTunnelManager()

	if sm1 == sm2 {
		t.Error("second InitGlobal should create a new SSHManager")
	}
	if tm1 == tm2 {
		t.Error("second InitGlobal should create a new TunnelManager")
	}
}
