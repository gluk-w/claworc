package sshmanager

import (
	"fmt"
	"sync"
	"testing"
)

func TestLogEventStoresEvent(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.LogEvent("test-inst", EventConnected, "connected to host")

	events := m.GetEvents("test-inst")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventConnected {
		t.Errorf("expected type %s, got %s", EventConnected, events[0].Type)
	}
	if events[0].InstanceName != "test-inst" {
		t.Errorf("expected instance 'test-inst', got %q", events[0].InstanceName)
	}
	if events[0].Details != "connected to host" {
		t.Errorf("expected details 'connected to host', got %q", events[0].Details)
	}
	if events[0].Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestLogEventAllTypes(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	types := []EventType{
		EventConnected,
		EventDisconnected,
		EventHealthCheckFailed,
		EventReconnecting,
		EventReconnectSuccess,
		EventReconnectFailed,
	}

	for _, et := range types {
		m.LogEvent("test", et, "detail")
	}

	events := m.GetEvents("test")
	if len(events) != len(types) {
		t.Fatalf("expected %d events, got %d", len(types), len(events))
	}

	for i, et := range types {
		if events[i].Type != et {
			t.Errorf("event %d: expected type %s, got %s", i, et, events[i].Type)
		}
	}
}

func TestLogEventRingBuffer(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	// Fill beyond the ring buffer limit
	for i := 0; i < maxEventsPerInstance+50; i++ {
		m.LogEvent("test", EventConnected, fmt.Sprintf("event %d", i))
	}

	events := m.GetEvents("test")
	if len(events) != maxEventsPerInstance {
		t.Fatalf("expected %d events (ring buffer), got %d", maxEventsPerInstance, len(events))
	}

	// Oldest event should be event 50 (dropped first 50)
	if events[0].Details != "event 50" {
		t.Errorf("expected first event 'event 50', got %q", events[0].Details)
	}

	// Last event should be event 149
	if events[len(events)-1].Details != "event 149" {
		t.Errorf("expected last event 'event 149', got %q", events[len(events)-1].Details)
	}
}

func TestGetRecentEventsFromLogEvent(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	for i := 0; i < 10; i++ {
		m.LogEvent("test", EventConnected, fmt.Sprintf("event %d", i))
	}

	recent := m.GetRecentEvents("test", 3)
	if len(recent) != 3 {
		t.Fatalf("expected 3 recent events, got %d", len(recent))
	}
	if recent[0].Details != "event 7" {
		t.Errorf("expected 'event 7', got %q", recent[0].Details)
	}
	if recent[2].Details != "event 9" {
		t.Errorf("expected 'event 9', got %q", recent[2].Details)
	}
}

func TestGetRecentEventsFewerThanRequested(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.LogEvent("test", EventConnected, "only one")
	recent := m.GetRecentEvents("test", 5)
	if len(recent) != 1 {
		t.Errorf("expected 1 event, got %d", len(recent))
	}
}

func TestGetEventsEmpty(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	events := m.GetEvents("nonexistent")
	if len(events) != 0 {
		t.Errorf("expected 0 events for unknown instance, got %d", len(events))
	}
}

func TestClearEvents(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.LogEvent("test", EventConnected, "connected")
	m.LogEvent("test", EventDisconnected, "disconnected")

	if len(m.GetEvents("test")) != 2 {
		t.Fatal("expected 2 events before clear")
	}

	m.ClearEvents("test")
	if len(m.GetEvents("test")) != 0 {
		t.Error("expected 0 events after clear")
	}
}

func TestClearEventsDoesNotAffectOtherInstances(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.LogEvent("inst-a", EventConnected, "connected")
	m.LogEvent("inst-b", EventConnected, "connected")

	m.ClearEvents("inst-a")

	if len(m.GetEvents("inst-a")) != 0 {
		t.Error("expected 0 events for inst-a after clear")
	}
	if len(m.GetEvents("inst-b")) != 1 {
		t.Error("expected 1 event for inst-b (untouched)")
	}
}

func TestGetEventsReturnsCopy(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.LogEvent("test", EventConnected, "original")

	events := m.GetEvents("test")
	events[0].Details = "mutated"

	fresh := m.GetEvents("test")
	if fresh[0].Details != "original" {
		t.Error("GetEvents returned reference, not copy")
	}
}

func TestGetRecentEventsReturnsCopy(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.LogEvent("test", EventConnected, "original")

	events := m.GetRecentEvents("test", 1)
	events[0].Details = "mutated"

	fresh := m.GetRecentEvents("test", 1)
	if fresh[0].Details != "original" {
		t.Error("GetRecentEvents returned reference, not copy")
	}
}

func TestLogEventConcurrentAccess(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			m.LogEvent("test", EventConnected, fmt.Sprintf("event %d", n))
		}(i)
	}
	wg.Wait()

	events := m.GetEvents("test")
	if len(events) != 50 {
		t.Errorf("expected 50 events, got %d", len(events))
	}
}

func TestEventHealthCheckFailedType(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.LogEvent("test", EventHealthCheckFailed, "connection timed out")

	events := m.GetEvents("test")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventHealthCheckFailed {
		t.Errorf("expected type %s, got %s", EventHealthCheckFailed, events[0].Type)
	}
	if string(events[0].Type) != "health_check_failed" {
		t.Errorf("expected string value 'health_check_failed', got %q", string(events[0].Type))
	}
}

func TestEventTypeStringValues(t *testing.T) {
	tests := []struct {
		eventType EventType
		expected  string
	}{
		{EventConnected, "connected"},
		{EventDisconnected, "disconnected"},
		{EventHealthCheckFailed, "health_check_failed"},
		{EventReconnecting, "reconnecting"},
		{EventReconnectSuccess, "reconnect_success"},
		{EventReconnectFailed, "reconnect_failed"},
	}

	for _, tt := range tests {
		if string(tt.eventType) != tt.expected {
			t.Errorf("EventType %v: expected string %q, got %q", tt.eventType, tt.expected, string(tt.eventType))
		}
	}
}

func TestMultipleInstanceEvents(t *testing.T) {
	m := NewSSHManager(0)
	defer m.CloseAll()

	m.LogEvent("inst-a", EventConnected, "a connected")
	m.LogEvent("inst-b", EventConnected, "b connected")
	m.LogEvent("inst-a", EventDisconnected, "a disconnected")

	eventsA := m.GetEvents("inst-a")
	eventsB := m.GetEvents("inst-b")

	if len(eventsA) != 2 {
		t.Errorf("expected 2 events for inst-a, got %d", len(eventsA))
	}
	if len(eventsB) != 1 {
		t.Errorf("expected 1 event for inst-b, got %d", len(eventsB))
	}
}

func TestGetEventCountsByType_Empty(t *testing.T) {
	sm := NewSSHManager(0)
	defer sm.CloseAll()

	counts := sm.GetEventCountsByType(EventReconnecting)
	if len(counts) != 0 {
		t.Errorf("expected empty map, got %v", counts)
	}
}

func TestGetEventCountsByType_SingleInstance(t *testing.T) {
	sm := NewSSHManager(0)
	defer sm.CloseAll()

	sm.LogEvent("bot-a", EventReconnecting, "attempt 1")
	sm.LogEvent("bot-a", EventReconnecting, "attempt 2")
	sm.LogEvent("bot-a", EventConnected, "connected")

	counts := sm.GetEventCountsByType(EventReconnecting)
	if counts["bot-a"] != 2 {
		t.Errorf("expected 2 reconnecting events for bot-a, got %d", counts["bot-a"])
	}

	counts = sm.GetEventCountsByType(EventConnected)
	if counts["bot-a"] != 1 {
		t.Errorf("expected 1 connected event for bot-a, got %d", counts["bot-a"])
	}
}

func TestGetEventCountsByType_MultipleInstances(t *testing.T) {
	sm := NewSSHManager(0)
	defer sm.CloseAll()

	sm.LogEvent("bot-a", EventReconnecting, "attempt 1")
	sm.LogEvent("bot-b", EventReconnecting, "attempt 1")
	sm.LogEvent("bot-b", EventReconnecting, "attempt 2")
	sm.LogEvent("bot-c", EventConnected, "connected")

	counts := sm.GetEventCountsByType(EventReconnecting)
	if counts["bot-a"] != 1 {
		t.Errorf("expected 1 for bot-a, got %d", counts["bot-a"])
	}
	if counts["bot-b"] != 2 {
		t.Errorf("expected 2 for bot-b, got %d", counts["bot-b"])
	}
	if _, ok := counts["bot-c"]; ok {
		t.Error("bot-c should not have reconnecting events")
	}
}

func TestGetEventCountsByType_NoMatches(t *testing.T) {
	sm := NewSSHManager(0)
	defer sm.CloseAll()

	sm.LogEvent("bot-a", EventConnected, "connected")
	sm.LogEvent("bot-a", EventDisconnected, "disconnected")

	counts := sm.GetEventCountsByType(EventReconnecting)
	if len(counts) != 0 {
		t.Errorf("expected empty map, got %v", counts)
	}
}
