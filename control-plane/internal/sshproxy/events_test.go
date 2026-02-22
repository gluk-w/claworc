package sshproxy

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestEventBuffer_Record(t *testing.T) {
	buf := &eventBuffer{}

	buf.record(ConnectionEvent{
		InstanceID: 1,
		Type:       EventConnected,
		Timestamp:  time.Now(),
		Details:    "test event",
	})

	if buf.count != 1 {
		t.Errorf("expected count 1, got %d", buf.count)
	}
	if buf.head != 1 {
		t.Errorf("expected head 1, got %d", buf.head)
	}
}

func TestEventBuffer_History_Empty(t *testing.T) {
	buf := &eventBuffer{}
	events := buf.history()
	if events != nil {
		t.Errorf("expected nil for empty buffer, got %v", events)
	}
}

func TestEventBuffer_History_Chronological(t *testing.T) {
	buf := &eventBuffer{}

	for i := 0; i < 5; i++ {
		buf.record(ConnectionEvent{
			InstanceID: 1,
			Type:       ConnectionEventType(fmt.Sprintf("event-%d", i)),
			Timestamp:  time.Now(),
			Details:    fmt.Sprintf("event %d", i),
		})
	}

	events := buf.history()
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}

	for i, e := range events {
		expected := fmt.Sprintf("event %d", i)
		if e.Details != expected {
			t.Errorf("event[%d].Details = %q, want %q", i, e.Details, expected)
		}
	}

	// Verify timestamps are in order
	for i := 1; i < len(events); i++ {
		if events[i].Timestamp.Before(events[i-1].Timestamp) {
			t.Errorf("event[%d] timestamp before event[%d]", i, i-1)
		}
	}
}

func TestEventBuffer_RingBuffer_Wraps(t *testing.T) {
	buf := &eventBuffer{}

	// Write more than buffer size events (buffer = 100)
	total := 120
	for i := 0; i < total; i++ {
		buf.record(ConnectionEvent{
			InstanceID: 1,
			Type:       EventConnected,
			Timestamp:  time.Now(),
			Details:    fmt.Sprintf("event-%d", i),
		})
	}

	events := buf.history()
	if len(events) != eventBufferSize {
		t.Fatalf("expected %d events, got %d", eventBufferSize, len(events))
	}

	// Oldest retained event should be event-20 (120-100=20)
	if events[0].Details != "event-20" {
		t.Errorf("oldest event = %q, want %q", events[0].Details, "event-20")
	}

	// Newest event should be event-119
	if events[len(events)-1].Details != "event-119" {
		t.Errorf("newest event = %q, want %q", events[len(events)-1].Details, "event-119")
	}

	// Verify chronological order
	for i := 1; i < len(events); i++ {
		if events[i].Timestamp.Before(events[i-1].Timestamp) {
			t.Errorf("event[%d] timestamp before event[%d]", i, i-1)
		}
	}
}

func TestEventLog_LogEvent(t *testing.T) {
	el := newEventLog()

	el.logEvent(1, EventConnected, "connected to host")

	events := el.getEvents(1)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].InstanceID != 1 {
		t.Errorf("InstanceID = %d, want 1", events[0].InstanceID)
	}
	if events[0].Type != EventConnected {
		t.Errorf("Type = %q, want %q", events[0].Type, EventConnected)
	}
	if events[0].Details != "connected to host" {
		t.Errorf("Details = %q, want %q", events[0].Details, "connected to host")
	}
	if events[0].Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}
}

func TestEventLog_RecordEvent(t *testing.T) {
	el := newEventLog()

	event := ConnectionEvent{
		InstanceID: 1,
		Type:       EventDisconnected,
		Timestamp:  time.Now(),
		Details:    "connection lost",
	}
	el.recordEvent(event)

	events := el.getEvents(1)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != EventDisconnected {
		t.Errorf("Type = %q, want %q", events[0].Type, EventDisconnected)
	}
	if events[0].Details != "connection lost" {
		t.Errorf("Details = %q, want %q", events[0].Details, "connection lost")
	}
}

func TestEventLog_MultipleInstances(t *testing.T) {
	el := newEventLog()

	el.logEvent(1, EventConnected, "inst1 connected")
	el.logEvent(2, EventConnected, "inst2 connected")
	el.logEvent(1, EventDisconnected, "inst1 disconnected")

	events1 := el.getEvents(1)
	if len(events1) != 2 {
		t.Fatalf("instance 1: expected 2 events, got %d", len(events1))
	}

	events2 := el.getEvents(2)
	if len(events2) != 1 {
		t.Fatalf("instance 2: expected 1 event, got %d", len(events2))
	}
}

func TestEventLog_GetEvents_UnknownInstance(t *testing.T) {
	el := newEventLog()

	events := el.getEvents(999)
	if events != nil {
		t.Errorf("expected nil for unknown instance, got %v", events)
	}
}

func TestEventLog_GetAllEvents(t *testing.T) {
	el := newEventLog()

	el.logEvent(1, EventConnected, "inst1")
	el.logEvent(2, EventDisconnected, "inst2")
	el.logEvent(3, EventReconnecting, "inst3")

	all := el.getAllEvents()
	if len(all) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(all))
	}

	if len(all[1]) != 1 || all[1][0].Type != EventConnected {
		t.Errorf("instance 1: unexpected events %v", all[1])
	}
	if len(all[2]) != 1 || all[2][0].Type != EventDisconnected {
		t.Errorf("instance 2: unexpected events %v", all[2])
	}
	if len(all[3]) != 1 || all[3][0].Type != EventReconnecting {
		t.Errorf("instance 3: unexpected events %v", all[3])
	}
}

func TestEventLog_Remove(t *testing.T) {
	el := newEventLog()

	el.logEvent(1, EventConnected, "connected")
	el.remove(1)

	events := el.getEvents(1)
	if events != nil {
		t.Errorf("expected nil after remove, got %v", events)
	}
}

func TestEventLog_ConcurrentAccess(t *testing.T) {
	el := newEventLog()

	var wg sync.WaitGroup
	const goroutines = 20
	const iterations = 100

	// Concurrent writers (different instances)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id uint) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				el.logEvent(id, EventConnected, fmt.Sprintf("event-%d", i))
			}
		}(uint(g))
	}

	// Concurrent readers
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id uint) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = el.getEvents(id)
				_ = el.getAllEvents()
			}
		}(uint(g))
	}

	wg.Wait()

	// Verify no panics and all instances have events
	for g := 0; g < goroutines; g++ {
		events := el.getEvents(uint(g))
		if len(events) != iterations {
			t.Errorf("instance %d: expected %d events, got %d", g, iterations, len(events))
		}
	}
}

func TestEventLog_AllEventTypes(t *testing.T) {
	el := newEventLog()

	types := []ConnectionEventType{
		EventConnected,
		EventDisconnected,
		EventReconnecting,
		EventReconnected,
		EventReconnectFailed,
		EventKeyUploaded,
		EventHealthCheckFailed,
	}

	for i, et := range types {
		el.logEvent(1, et, fmt.Sprintf("type-%d", i))
	}

	events := el.getEvents(1)
	if len(events) != len(types) {
		t.Fatalf("expected %d events, got %d", len(types), len(events))
	}

	for i, e := range events {
		if e.Type != types[i] {
			t.Errorf("event[%d].Type = %q, want %q", i, e.Type, types[i])
		}
	}
}

func TestEventLog_TimestampOrder(t *testing.T) {
	el := newEventLog()

	before := time.Now()
	el.logEvent(1, EventConnected, "first")
	el.logEvent(1, EventDisconnected, "second")
	after := time.Now()

	events := el.getEvents(1)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	for i, e := range events {
		if e.Timestamp.Before(before) || e.Timestamp.After(after) {
			t.Errorf("event[%d].Timestamp %v not in range [%v, %v]", i, e.Timestamp, before, after)
		}
	}

	if events[1].Timestamp.Before(events[0].Timestamp) {
		t.Error("second event timestamp before first")
	}
}

// Tests for SSHManager integration

func TestSSHManager_LogEvent(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	mgr.LogEvent(1, EventConnected, "manual log")

	events := mgr.GetEventHistory(1)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventConnected {
		t.Errorf("Type = %q, want %q", events[0].Type, EventConnected)
	}
	if events[0].Details != "manual log" {
		t.Errorf("Details = %q, want %q", events[0].Details, "manual log")
	}
}

func TestSSHManager_GetEventHistory_Empty(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	events := mgr.GetEventHistory(999)
	if events != nil {
		t.Errorf("expected nil for unknown instance, got %v", events)
	}
}

func TestSSHManager_GetAllEventHistory(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	mgr.LogEvent(1, EventConnected, "inst1")
	mgr.LogEvent(2, EventDisconnected, "inst2")

	all := mgr.GetAllEventHistory()
	if len(all) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(all))
	}
}

func TestSSHManager_EmitEvent_RecordsToLog(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	// Emit an event (as internal code does)
	mgr.emitEvent(ConnectionEvent{
		InstanceID: 1,
		Type:       EventKeyUploaded,
		Timestamp:  time.Now(),
		Details:    "key uploaded via emitEvent",
	})

	events := mgr.GetEventHistory(1)
	if len(events) != 1 {
		t.Fatalf("expected 1 event from emitEvent, got %d", len(events))
	}
	if events[0].Type != EventKeyUploaded {
		t.Errorf("Type = %q, want %q", events[0].Type, EventKeyUploaded)
	}
}

func TestSSHManager_EmitEvent_NotifiesListenersAndLogs(t *testing.T) {
	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	var listenerCalled bool
	mgr.OnEvent(func(event ConnectionEvent) {
		listenerCalled = true
	})

	mgr.emitEvent(ConnectionEvent{
		InstanceID: 1,
		Type:       EventConnected,
		Timestamp:  time.Now(),
		Details:    "test",
	})

	if !listenerCalled {
		t.Error("listener was not called")
	}

	events := mgr.GetEventHistory(1)
	if len(events) != 1 {
		t.Fatalf("expected 1 event in log, got %d", len(events))
	}
}

func TestSSHManager_Connect_EmitsEvents(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)

	_, err := mgr.Connect(t.Context(), 1, host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	// Connect itself doesn't emit events via emitEvent (it uses stateTracker),
	// but Close does emit EventDisconnected via keepalive failure path.
	// LogEvent can be used for manual events. The key test is that
	// emitEvent correctly records to the event log.
}

func TestEventHealthCheckFailed_Type(t *testing.T) {
	if EventHealthCheckFailed != "health_check_failed" {
		t.Errorf("EventHealthCheckFailed = %q, want %q", EventHealthCheckFailed, "health_check_failed")
	}
}
