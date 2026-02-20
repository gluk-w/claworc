package services

import (
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestByteReader(t *testing.T) {
	// Test that byteReader correctly adapts an io.Reader to io.ByteReader.
	data := []byte{0x80, 0x01} // varint encoding of 128
	br := &byteReader{r: &bytesReader{data: data}}

	val, err := binary.ReadUvarint(br)
	if err != nil {
		t.Fatalf("ReadUvarint: %v", err)
	}
	if val != 128 {
		t.Fatalf("expected 128, got %d", val)
	}
}

func TestTermFrameConstants(t *testing.T) {
	if frameBinary != 0x01 {
		t.Fatalf("frameBinary should be 0x01, got 0x%02x", frameBinary)
	}
	if frameControl != 0x02 {
		t.Fatalf("frameControl should be 0x02, got 0x%02x", frameControl)
	}
}

func TestTermInitHeaderJSON(t *testing.T) {
	h := termInitHeader{Cols: 120, Rows: 40}
	data, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed termInitHeader
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Cols != 120 || parsed.Rows != 40 {
		t.Fatalf("round trip failed: got %+v", parsed)
	}
}

func TestTermControlMsgJSON(t *testing.T) {
	msg := termControlMsg{Type: "resize", Cols: 200, Rows: 50}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed termControlMsg
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Type != "resize" || parsed.Cols != 200 || parsed.Rows != 50 {
		t.Fatalf("round trip failed: got %+v", parsed)
	}
}

// bytesReader is a simple io.Reader over a byte slice for testing.
type bytesReader struct {
	data []byte
	pos  int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, nil
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
