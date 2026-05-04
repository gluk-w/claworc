package handlers

import (
	"bytes"
	"sync"
	"testing"
)

func TestViewerRegistry_FirstJoinerIsPrimary(t *testing.T) {
	r := &viewerRegistry{perInst: make(map[uint][]*viewerSession)}
	a := r.Join(1)
	if !a.isPrimary() {
		t.Fatal("first viewer should be primary")
	}
	b := r.Join(1)
	if b.isPrimary() {
		t.Fatal("second viewer should be secondary")
	}
}

func TestViewerRegistry_LeavePromotesNextOldest(t *testing.T) {
	r := &viewerRegistry{perInst: make(map[uint][]*viewerSession)}
	a := r.Join(1)
	b := r.Join(1)
	c := r.Join(1)

	// Wire b's inject so we can capture replay.
	var mu sync.Mutex
	var captured []byte
	b.inject = func(buf []byte) { mu.Lock(); captured = append([]byte(nil), buf...); mu.Unlock() }
	b.recordSetDesktopSize([]byte{0xfb, 0, 4, 0, 3, 0, 1, 0})

	r.Leave(1, a)
	if !b.isPrimary() {
		t.Fatal("b should be promoted after a leaves")
	}
	if c.isPrimary() {
		t.Fatal("c should still be secondary")
	}
	mu.Lock()
	defer mu.Unlock()
	if !bytes.Equal(captured, []byte{0xfb, 0, 4, 0, 3, 0, 1, 0}) {
		t.Fatalf("b's last SetDesktopSize should be replayed on promotion, got %v", captured)
	}
}

func TestViewerRegistry_LeaveSecondaryDoesNotPromote(t *testing.T) {
	r := &viewerRegistry{perInst: make(map[uint][]*viewerSession)}
	a := r.Join(1)
	b := r.Join(1)

	called := false
	a.inject = func([]byte) { called = true }

	r.Leave(1, b)
	if !a.isPrimary() {
		t.Fatal("a should remain primary after b (secondary) leaves")
	}
	if called {
		t.Fatal("inject should not be called when a non-primary leaves")
	}
}

func TestViewerRegistry_LastViewerLeaveCleansEntry(t *testing.T) {
	r := &viewerRegistry{perInst: make(map[uint][]*viewerSession)}
	a := r.Join(1)
	r.Leave(1, a)
	if _, ok := r.perInst[1]; ok {
		t.Fatal("registry should drop empty per-instance lists")
	}
}
