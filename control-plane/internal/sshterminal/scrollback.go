package sshterminal

import "sync"

// scrollbackBuffer is a fixed-capacity ring buffer that stores raw terminal
// output bytes. When the buffer is full, the oldest data is overwritten.
//
// The buffer operates on raw bytes rather than lines to avoid the overhead of
// line splitting in a high-throughput terminal stream. The capacity is specified
// in "lines" (approximate), where each line is estimated at 120 bytes. This
// gives a reasonable default for typical terminal output without requiring
// actual newline tracking.
type scrollbackBuffer struct {
	mu   sync.Mutex
	data []byte
	size int // max capacity in bytes
	pos  int // write position (ring)
	full bool
}

const bytesPerLine = 120

// newScrollbackBuffer creates a buffer that holds approximately the given
// number of lines of terminal output.
func newScrollbackBuffer(lines int) *scrollbackBuffer {
	size := lines * bytesPerLine
	if size < 1024 {
		size = 1024
	}
	return &scrollbackBuffer{
		data: make([]byte, size),
		size: size,
	}
}

// Write appends data to the ring buffer, overwriting old data if necessary.
func (sb *scrollbackBuffer) Write(p []byte) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	for len(p) > 0 {
		n := copy(sb.data[sb.pos:], p)
		sb.pos += n
		p = p[n:]
		if sb.pos >= sb.size {
			sb.pos = 0
			sb.full = true
		}
	}
}

// Bytes returns the buffered content in chronological order.
func (sb *scrollbackBuffer) Bytes() []byte {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	if !sb.full {
		result := make([]byte, sb.pos)
		copy(result, sb.data[:sb.pos])
		return result
	}

	// Ring has wrapped: return [pos..end] + [0..pos]
	result := make([]byte, sb.size)
	copy(result, sb.data[sb.pos:])
	copy(result[sb.size-sb.pos:], sb.data[:sb.pos])
	return result
}

// Len returns the number of bytes currently stored.
func (sb *scrollbackBuffer) Len() int {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	if sb.full {
		return sb.size
	}
	return sb.pos
}
