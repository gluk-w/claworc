package sshterminal

import (
	"testing"
	"time"
)

func TestScrollbackBuffer_Write(t *testing.T) {
	sb := NewScrollbackBuffer(100)

	sb.Write([]byte("hello"))
	if sb.Len() != 5 {
		t.Errorf("expected len 5, got %d", sb.Len())
	}

	snapshot := sb.Snapshot()
	if string(snapshot) != "hello" {
		t.Errorf("expected 'hello', got %q", snapshot)
	}
}

func TestScrollbackBuffer_WriteTrimming(t *testing.T) {
	sb := NewScrollbackBuffer(10)

	sb.Write([]byte("0123456789"))
	if sb.Len() != 10 {
		t.Errorf("expected len 10, got %d", sb.Len())
	}

	// Write more data to trigger trimming
	sb.Write([]byte("ABCDE"))
	if sb.Len() != 10 {
		t.Errorf("expected len 10 after trimming, got %d", sb.Len())
	}

	snapshot := sb.Snapshot()
	expected := "56789ABCDE"
	if string(snapshot) != expected {
		t.Errorf("expected %q, got %q", expected, snapshot)
	}
}

func TestScrollbackBuffer_DefaultSize(t *testing.T) {
	sb := NewScrollbackBuffer(0)
	if sb.maxLen != defaultScrollbackSize {
		t.Errorf("expected default size %d, got %d", defaultScrollbackSize, sb.maxLen)
	}
}

func TestScrollbackBuffer_Notify(t *testing.T) {
	sb := NewScrollbackBuffer(100)

	// Write should signal on notify channel
	sb.Write([]byte("data"))

	select {
	case <-sb.Notify():
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for notify signal")
	}
}

func TestScrollbackBuffer_Close(t *testing.T) {
	sb := NewScrollbackBuffer(100)

	sb.Write([]byte("before close"))
	sb.Close()

	if !sb.IsClosed() {
		t.Error("expected buffer to be closed")
	}

	// Data should still be readable after close
	snapshot := sb.Snapshot()
	if string(snapshot) != "before close" {
		t.Errorf("expected 'before close', got %q", snapshot)
	}
}

func TestScrollbackBuffer_SnapshotIsCopy(t *testing.T) {
	sb := NewScrollbackBuffer(100)
	sb.Write([]byte("original"))

	snapshot := sb.Snapshot()
	// Modify snapshot
	snapshot[0] = 'X'

	// Original should be unchanged
	snapshot2 := sb.Snapshot()
	if string(snapshot2) != "original" {
		t.Errorf("snapshot modification affected original data: %q", snapshot2)
	}
}

func TestScrollbackBuffer_ConcurrentAccess(t *testing.T) {
	sb := NewScrollbackBuffer(10000)
	done := make(chan struct{})

	// Writer goroutine
	go func() {
		for i := 0; i < 1000; i++ {
			sb.Write([]byte("data"))
		}
		close(done)
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 1000; i++ {
			sb.Snapshot()
			sb.Len()
			sb.IsClosed()
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for concurrent access test")
	}
}
