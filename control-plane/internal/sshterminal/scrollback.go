package sshterminal

import (
	"sync"
)

// defaultScrollbackSize is the default maximum scrollback buffer size (1 MB).
const defaultScrollbackSize = 1024 * 1024

// ScrollbackBuffer is a thread-safe byte buffer that stores terminal output
// for replay on reconnection. When the buffer exceeds maxLen, older data is
// trimmed from the front.
type ScrollbackBuffer struct {
	mu     sync.Mutex
	data   []byte
	maxLen int
	closed bool
	notify chan struct{} // signaled (non-blocking) when new data arrives
}

// NewScrollbackBuffer creates a new scrollback buffer with the given maximum size.
// If maxLen <= 0, defaultScrollbackSize is used.
func NewScrollbackBuffer(maxLen int) *ScrollbackBuffer {
	if maxLen <= 0 {
		maxLen = defaultScrollbackSize
	}
	return &ScrollbackBuffer{
		maxLen: maxLen,
		notify: make(chan struct{}, 1),
	}
}

// Write appends data to the scrollback buffer, trimming from the front
// if the total exceeds maxLen. It signals waiting readers via the notify channel.
func (s *ScrollbackBuffer) Write(p []byte) {
	s.mu.Lock()
	s.data = append(s.data, p...)
	if len(s.data) > s.maxLen {
		s.data = s.data[len(s.data)-s.maxLen:]
	}
	s.mu.Unlock()

	// Non-blocking signal to wake up readers
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Close marks the buffer as closed and signals readers.
func (s *ScrollbackBuffer) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()

	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Snapshot returns a copy of the current buffer contents.
func (s *ScrollbackBuffer) Snapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]byte, len(s.data))
	copy(result, s.data)
	return result
}

// Len returns the current buffer length.
func (s *ScrollbackBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data)
}

// IsClosed returns whether the buffer has been closed.
func (s *ScrollbackBuffer) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Notify returns the channel that is signaled when new data is available.
// Readers should select on this channel and then call Snapshot or Len.
func (s *ScrollbackBuffer) Notify() <-chan struct{} {
	return s.notify
}
