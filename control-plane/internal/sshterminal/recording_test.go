package sshterminal

import (
	"encoding/json"
	"testing"
)

func TestSessionRecording_RecordOutput(t *testing.T) {
	sr := NewSessionRecording(0)

	sr.RecordOutput([]byte("hello"))
	sr.RecordOutput([]byte("world"))

	entries := sr.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Type != "o" {
		t.Errorf("expected type 'o', got %q", entries[0].Type)
	}
	if entries[0].Data != "hello" {
		t.Errorf("expected data 'hello', got %q", entries[0].Data)
	}
	if entries[1].Data != "world" {
		t.Errorf("expected data 'world', got %q", entries[1].Data)
	}
	if entries[1].Elapsed <= 0 && entries[0].Elapsed <= 0 {
		// At least first entry should have very small elapsed (can be 0)
		// but second should have elapsed >= 0
	}
}

func TestSessionRecording_RecordInput(t *testing.T) {
	sr := NewSessionRecording(0)

	sr.RecordInput([]byte("ls -la\n"))

	entries := sr.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	if entries[0].Type != "i" {
		t.Errorf("expected type 'i', got %q", entries[0].Type)
	}
	if entries[0].Data != "ls -la\n" {
		t.Errorf("expected data 'ls -la\\n', got %q", entries[0].Data)
	}
}

func TestSessionRecording_MaxEntries(t *testing.T) {
	sr := NewSessionRecording(3)

	sr.RecordOutput([]byte("1"))
	sr.RecordOutput([]byte("2"))
	sr.RecordOutput([]byte("3"))
	sr.RecordOutput([]byte("4")) // should be dropped

	if sr.EntryCount() != 3 {
		t.Errorf("expected 3 entries, got %d", sr.EntryCount())
	}

	entries := sr.Entries()
	if entries[2].Data != "3" {
		t.Errorf("expected last entry data '3', got %q", entries[2].Data)
	}
}

func TestSessionRecording_ExportJSON(t *testing.T) {
	sr := NewSessionRecording(0)

	sr.RecordOutput([]byte("output"))
	sr.RecordInput([]byte("input"))

	data, err := sr.ExportJSON()
	if err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}

	var entries []RecordingEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries in JSON, got %d", len(entries))
	}

	if entries[0].Type != "o" || entries[0].Data != "output" {
		t.Errorf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].Type != "i" || entries[1].Data != "input" {
		t.Errorf("unexpected second entry: %+v", entries[1])
	}
}

func TestSessionRecording_EntriesIsCopy(t *testing.T) {
	sr := NewSessionRecording(0)
	sr.RecordOutput([]byte("original"))

	entries := sr.Entries()
	entries[0].Data = "modified"

	// Original should be unchanged
	entries2 := sr.Entries()
	if entries2[0].Data != "original" {
		t.Errorf("Entries() returned reference, not copy: %q", entries2[0].Data)
	}
}
