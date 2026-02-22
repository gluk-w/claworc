package sshterminal

import (
	"strings"
	"testing"
)

func TestScrollbackBuffer_BasicWriteRead(t *testing.T) {
	sb := newScrollbackBuffer(10) // ~1200 bytes

	sb.Write([]byte("hello world"))
	got := string(sb.Bytes())
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestScrollbackBuffer_MultipleWrites(t *testing.T) {
	sb := newScrollbackBuffer(10)

	sb.Write([]byte("hello "))
	sb.Write([]byte("world"))
	got := string(sb.Bytes())
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestScrollbackBuffer_WrapAround(t *testing.T) {
	// Create a small buffer (minimum 1024 bytes)
	sb := newScrollbackBuffer(1) // 1 * 120 = 120 -> clamped to 1024

	// Write more than 1024 bytes
	first := strings.Repeat("A", 512)
	second := strings.Repeat("B", 1024)
	sb.Write([]byte(first))
	sb.Write([]byte(second))

	got := sb.Bytes()
	// After wrap, we should have the most recent 1024 bytes
	if len(got) != 1024 {
		t.Errorf("len = %d, want 1024", len(got))
	}
	// The last 1024 bytes of (first + second) = last 512 of first + first 512 of second?
	// No: first is 512 bytes, second is 1024, total 1536. After wrap the ring holds the last 1024 bytes.
	// That's second[0:1024] = all of second = "BBB...B" (1024 B's)
	// But wait: first(512) fills 0..511, then second(1024) fills 512..1023 (wraps at 1024) then 0..511
	// After wrap: pos=512, full=true
	// Bytes(): data[512:1024] + data[0:512] = second[512:1024] + second[0:512]? No...
	// Let me think: first fills pos 0..511 (pos=512). second fills pos 512..1023 (512 bytes, pos wraps to 0, full=true)
	// then remaining 512 bytes of second fills 0..511 (pos=512).
	// So data = second[512:1024] at positions 0..511, second[0:512] at positions 512..1023
	// Bytes() with full=true: data[pos=512:] + data[:pos=512]
	// = data[512..1023] + data[0..511] = second[0:512] + second[512:1024] = all of second
	expected := second
	if string(got) != expected {
		t.Errorf("content mismatch after wrap")
	}
}

func TestScrollbackBuffer_EmptyRead(t *testing.T) {
	sb := newScrollbackBuffer(10)
	got := sb.Bytes()
	if len(got) != 0 {
		t.Errorf("expected empty bytes, got %d bytes", len(got))
	}
}

func TestScrollbackBuffer_Len(t *testing.T) {
	sb := newScrollbackBuffer(10)

	if sb.Len() != 0 {
		t.Errorf("empty buffer Len() = %d, want 0", sb.Len())
	}

	sb.Write([]byte("hello"))
	if sb.Len() != 5 {
		t.Errorf("after write Len() = %d, want 5", sb.Len())
	}
}

func TestScrollbackBuffer_LenAfterWrap(t *testing.T) {
	sb := newScrollbackBuffer(1) // 1024 bytes

	// Write more than capacity
	sb.Write([]byte(strings.Repeat("X", 2000)))
	if sb.Len() != 1024 {
		t.Errorf("after wrap Len() = %d, want 1024", sb.Len())
	}
}

func TestScrollbackBuffer_ExactCapacity(t *testing.T) {
	sb := newScrollbackBuffer(1) // 1024 bytes

	data := strings.Repeat("Z", 1024)
	sb.Write([]byte(data))

	got := sb.Bytes()
	if string(got) != data {
		t.Error("exact capacity write didn't preserve content")
	}
	// At exactly capacity, pos wraps to 0 and full becomes true
	if sb.Len() != 1024 {
		t.Errorf("Len() = %d, want 1024", sb.Len())
	}
}
