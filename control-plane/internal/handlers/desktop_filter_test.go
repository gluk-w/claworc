package handlers

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildSetDesktopSize returns a 24-byte ClientSetDesktopSize message with one
// screen (the shape noVNC always sends).
func buildSetDesktopSize(width, height uint16) []byte {
	msg := make([]byte, 24)
	msg[0] = 251 // type
	msg[1] = 0   // padding
	binary.BigEndian.PutUint16(msg[2:4], width)
	binary.BigEndian.PutUint16(msg[4:6], height)
	msg[6] = 1 // num-screens
	msg[7] = 0 // padding
	// ScreenInfo: id(4) x(2) y(2) w(2) h(2) flags(4)
	binary.BigEndian.PutUint16(msg[16:18], width)
	binary.BigEndian.PutUint16(msg[18:20], height)
	return msg
}

func TestRFBFilter_DropsSetDesktopSizeFromSecondary(t *testing.T) {
	s := &viewerSession{}
	s.primary.Store(false)
	f := newRFBClientFilter(s)
	// Skip the 14-byte handshake.
	f.handshakeRemaining = 0

	in := buildSetDesktopSize(1024, 768)
	out := f.Process(in)
	if len(out) != 0 {
		t.Fatalf("secondary's SetDesktopSize should be dropped, got %d bytes", len(out))
	}
	s.mu.Lock()
	cached := append([]byte(nil), s.lastSetDesktopSize...)
	s.mu.Unlock()
	if !bytes.Equal(cached, in) {
		t.Fatalf("filter must record the dropped SetDesktopSize for replay, got %v", cached)
	}
}

func TestRFBFilter_ForwardsSetDesktopSizeFromPrimary(t *testing.T) {
	s := &viewerSession{}
	s.primary.Store(true)
	f := newRFBClientFilter(s)
	f.handshakeRemaining = 0

	in := buildSetDesktopSize(1920, 1080)
	out := f.Process(in)
	if !bytes.Equal(out, in) {
		t.Fatalf("primary's SetDesktopSize should pass through, got %d bytes", len(out))
	}
}

func TestRFBFilter_PassesThroughOtherMessages(t *testing.T) {
	s := &viewerSession{}
	s.primary.Store(false) // would drop SetDesktopSize, but other types must still flow
	f := newRFBClientFilter(s)
	f.handshakeRemaining = 0

	// PointerEvent (type 5, 6 bytes) followed by KeyEvent (type 4, 8 bytes).
	pointer := []byte{5, 1, 0, 100, 0, 200}
	key := []byte{4, 1, 0, 0, 0, 0, 0, 0x41}
	in := append(append([]byte{}, pointer...), key...)
	out := f.Process(in)
	if !bytes.Equal(out, in) {
		t.Fatalf("non-resize messages must pass through unchanged; got %v", out)
	}
}

func TestRFBFilter_HandlesPartialMessages(t *testing.T) {
	s := &viewerSession{}
	s.primary.Store(true)
	f := newRFBClientFilter(s)
	f.handshakeRemaining = 0

	full := buildSetDesktopSize(800, 600)
	// Feed one byte at a time.
	var got []byte
	for i := 0; i < len(full); i++ {
		got = append(got, f.Process(full[i:i+1])...)
	}
	if !bytes.Equal(got, full) {
		t.Fatalf("byte-by-byte fed message should reassemble; got %d bytes want %d", len(got), len(full))
	}
}

func TestRFBFilter_ConsumesHandshakeBeforeParsing(t *testing.T) {
	s := &viewerSession{}
	s.primary.Store(false)
	f := newRFBClientFilter(s) // handshakeRemaining=14 by default

	handshake := bytes.Repeat([]byte("RFB 003.008\n"), 1)[:12]
	handshake = append(handshake, 1)    // chosen security type
	handshake = append(handshake, 0)    // ClientInit shared-flag
	combined := append(handshake, buildSetDesktopSize(640, 480)...)

	out := f.Process(combined)
	if !bytes.Equal(out, handshake) {
		t.Fatalf("handshake bytes must pass through verbatim; subsequent secondary SetDesktopSize must be dropped")
	}
}

func TestRFBFilter_BypassesOnUnknownType(t *testing.T) {
	s := &viewerSession{}
	s.primary.Store(false)
	f := newRFBClientFilter(s)
	f.handshakeRemaining = 0

	// An unknown message type forces bypass; everything passes through.
	mixed := append([]byte{0x99, 0xAA}, buildSetDesktopSize(1000, 700)...)
	out := f.Process(mixed)
	if !bytes.Equal(out, mixed) {
		t.Fatalf("unknown msg type should bypass filter and pass-through; got %d bytes want %d", len(out), len(mixed))
	}
}
