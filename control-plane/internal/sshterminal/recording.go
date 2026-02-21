package sshterminal

import (
	"encoding/json"
	"sync"
	"time"
)

// RecordingEntry represents a single timestamped output event in a terminal
// recording. The format is inspired by asciinema v2.
type RecordingEntry struct {
	// Elapsed is the time since session start in seconds.
	Elapsed float64 `json:"elapsed"`
	// Type is "o" for output, "i" for input.
	Type string `json:"type"`
	// Data is the terminal data.
	Data string `json:"data"`
}

// SessionRecording captures timestamped terminal I/O for audit and replay.
// It is safe for concurrent use.
//
// Recording is optional and user-configurable. When enabled, all output
// (and optionally input) is captured with timestamps. Recordings can be
// exported for audit review or replayed in compatible players.
type SessionRecording struct {
	mu        sync.Mutex
	entries   []RecordingEntry
	startTime time.Time
	maxEntries int
}

// NewSessionRecording creates a new recording. If maxEntries <= 0, there is
// no limit on the number of entries.
func NewSessionRecording(maxEntries int) *SessionRecording {
	return &SessionRecording{
		startTime:  time.Now(),
		maxEntries: maxEntries,
	}
}

// RecordOutput adds an output event to the recording.
func (sr *SessionRecording) RecordOutput(data []byte) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.maxEntries > 0 && len(sr.entries) >= sr.maxEntries {
		return // drop if at capacity
	}

	sr.entries = append(sr.entries, RecordingEntry{
		Elapsed: time.Since(sr.startTime).Seconds(),
		Type:    "o",
		Data:    string(data),
	})
}

// RecordInput adds an input event to the recording.
func (sr *SessionRecording) RecordInput(data []byte) {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	if sr.maxEntries > 0 && len(sr.entries) >= sr.maxEntries {
		return
	}

	sr.entries = append(sr.entries, RecordingEntry{
		Elapsed: time.Since(sr.startTime).Seconds(),
		Type:    "i",
		Data:    string(data),
	})
}

// Entries returns a copy of all recorded entries.
func (sr *SessionRecording) Entries() []RecordingEntry {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	result := make([]RecordingEntry, len(sr.entries))
	copy(result, sr.entries)
	return result
}

// EntryCount returns the number of recorded entries.
func (sr *SessionRecording) EntryCount() int {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return len(sr.entries)
}

// ExportJSON returns the recording as JSON-encoded bytes.
func (sr *SessionRecording) ExportJSON() ([]byte, error) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return json.Marshal(sr.entries)
}
