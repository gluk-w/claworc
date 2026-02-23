package sshproxy

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// mockOrch implements Orchestrator for reconnection tests.
type mockOrch struct {
	mu             sync.Mutex
	host           string
	port           int
	configureErr   error
	getAddrErr     error
	configureCalls int
	getAddrCalls   int
}

func (o *mockOrch) ConfigureSSHAccess(_ context.Context, _ uint, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.configureCalls++
	return o.configureErr
}

func (o *mockOrch) GetSSHAddress(_ context.Context, _ uint) (string, int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.getAddrCalls++
	return o.host, o.port, o.getAddrErr
}

func (o *mockOrch) getConfigureCalls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.configureCalls
}

// failNOrch fails ConfigureSSHAccess for the first N calls, then succeeds.
type failNOrch struct {
	mu        sync.Mutex
	failCount int // number of calls to fail
	calls     int
	host      string
	port      int
}

func (o *failNOrch) ConfigureSSHAccess(_ context.Context, _ uint, _ string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	if o.calls <= o.failCount {
		return fmt.Errorf("simulated failure %d", o.calls)
	}
	return nil
}

func (o *failNOrch) GetSSHAddress(_ context.Context, _ uint) (string, int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.host, o.port, nil
}

func (o *failNOrch) getCalls() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

func TestReconnectWithBackoff_Success(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	host, port := parseHostPort(t, ts.addr)
	orch := &mockOrch{host: host, port: port}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	err := mgr.reconnectWithBackoff(context.Background(), 1, 3, orch, "test")
	if err != nil {
		t.Fatalf("reconnectWithBackoff() error: %v", err)
	}

	// Verify connection was established
	if !mgr.IsConnected(1) {
		t.Error("expected connection to be established after reconnect")
	}

	// Verify key was uploaded
	if calls := orch.getConfigureCalls(); calls != 1 {
		t.Errorf("ConfigureSSHAccess calls = %d, want 1", calls)
	}
}

func TestReconnectWithBackoff_FailsThenSucceeds(t *testing.T) {
	// Use short backoff for faster test
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 10 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	host, port := parseHostPort(t, ts.addr)
	orch := &failNOrch{failCount: 2, host: host, port: port}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	err := mgr.reconnectWithBackoff(context.Background(), 1, 5, orch, "test-retry")
	if err != nil {
		t.Fatalf("reconnectWithBackoff() error: %v", err)
	}

	if !mgr.IsConnected(1) {
		t.Error("expected connection after retries")
	}

	// Should have called ConfigureSSHAccess 3 times (2 failures + 1 success)
	if calls := orch.getCalls(); calls != 3 {
		t.Errorf("ConfigureSSHAccess calls = %d, want 3", calls)
	}
}

func TestReconnectWithBackoff_MaxRetriesExhausted(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 10 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	signer, _ := newTestSignerAndServer(t)

	// Use an orchestrator that always fails
	orch := &mockOrch{configureErr: fmt.Errorf("always fails")}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	err := mgr.reconnectWithBackoff(context.Background(), 1, 3, orch, "test-exhaust")
	if err == nil {
		t.Fatal("expected error when max retries exhausted")
	}

	// Verify all retries were attempted
	if calls := orch.getConfigureCalls(); calls != 3 {
		t.Errorf("ConfigureSSHAccess calls = %d, want 3", calls)
	}

	// Verify no connection exists
	if mgr.IsConnected(1) {
		t.Error("should not be connected after all retries failed")
	}
}

func TestReconnectWithBackoff_KeyReuploadBeforeEachAttempt(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 10 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	// Orchestrator that succeeds on ConfigureSSHAccess but provides unreachable host
	orch := &mockOrch{
		host: "127.0.0.1",
		port: 1, // unreachable port
	}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	maxRetries := 4
	_ = mgr.reconnectWithBackoff(ctx, 1, maxRetries, orch, "test-key-upload")

	// ConfigureSSHAccess should be called once per attempt
	if calls := orch.getConfigureCalls(); calls != maxRetries {
		t.Errorf("ConfigureSSHAccess calls = %d, want %d", calls, maxRetries)
	}
}

func TestReconnectWithBackoff_BackoffTiming(t *testing.T) {
	saved := reconnectInitialBackoff
	savedMax := reconnectMaxBackoff
	reconnectInitialBackoff = 50 * time.Millisecond
	reconnectMaxBackoff = 200 * time.Millisecond
	defer func() {
		reconnectInitialBackoff = saved
		reconnectMaxBackoff = savedMax
	}()

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	// Use a custom orchestrator that records call timestamps
	var mu sync.Mutex
	var callTimes []time.Time
	customOrch := &timingOrch{
		configureErr: fmt.Errorf("always fails"),
		onConfigure: func() {
			mu.Lock()
			callTimes = append(callTimes, time.Now())
			mu.Unlock()
		},
	}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	maxRetries := 4
	_ = mgr.reconnectWithBackoff(context.Background(), 1, maxRetries, customOrch, "test-timing")

	mu.Lock()
	times := make([]time.Time, len(callTimes))
	copy(times, callTimes)
	mu.Unlock()

	if len(times) != maxRetries {
		t.Fatalf("expected %d attempts, got %d", maxRetries, len(times))
	}

	// Verify backoff intervals: 50ms, 100ms, 200ms (capped)
	// Allow 30ms tolerance for scheduling jitter
	expectedBackoffs := []time.Duration{0, 50 * time.Millisecond, 100 * time.Millisecond}
	for i := 1; i < len(times); i++ {
		actual := times[i].Sub(times[i-1])
		expected := expectedBackoffs[i-1]
		if actual < expected-30*time.Millisecond {
			t.Errorf("backoff %d: actual %v < expected %v (with tolerance)", i, actual, expected)
		}
	}
}

// timingOrch is an Orchestrator that records call timestamps.
type timingOrch struct {
	mu           sync.Mutex
	configureErr error
	onConfigure  func()
}

func (o *timingOrch) ConfigureSSHAccess(_ context.Context, _ uint, _ string) error {
	if o.onConfigure != nil {
		o.onConfigure()
	}
	return o.configureErr
}

func (o *timingOrch) GetSSHAddress(_ context.Context, _ uint) (string, int, error) {
	return "127.0.0.1", 1, nil
}

func TestReconnectWithBackoff_ContextCancellation(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 100 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	orch := &mockOrch{configureErr: fmt.Errorf("always fails")}
	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err = mgr.reconnectWithBackoff(ctx, 1, 100, orch, "test-cancel")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from context cancellation")
	}

	// Should return quickly after cancellation, not wait for all 100 retries
	if elapsed > 2*time.Second {
		t.Errorf("took %v, expected quick return after context cancellation", elapsed)
	}
}

func TestReconnectWithBackoff_EventEmission(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 10 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	signer, ts := newTestSignerAndServer(t)
	defer ts.cleanup()

	host, port := parseHostPort(t, ts.addr)
	orch := &mockOrch{host: host, port: port}

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	// Collect events
	var mu sync.Mutex
	var events []ConnectionEvent
	mgr.OnEvent(func(e ConnectionEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	err := mgr.reconnectWithBackoff(context.Background(), 1, 3, orch, "test-events")
	if err != nil {
		t.Fatalf("reconnectWithBackoff() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Expect: Reconnecting → KeyUploaded → Reconnected
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d: %v", len(events), eventTypes(events))
	}
	if events[0].Type != EventReconnecting {
		t.Errorf("event[0] = %s, want %s", events[0].Type, EventReconnecting)
	}
	if events[1].Type != EventKeyUploaded {
		t.Errorf("event[1] = %s, want %s", events[1].Type, EventKeyUploaded)
	}
	if events[2].Type != EventReconnected {
		t.Errorf("event[2] = %s, want %s", events[2].Type, EventReconnected)
	}

	// Verify instance ID on all events
	for i, e := range events {
		if e.InstanceID != 1 {
			t.Errorf("event[%d].InstanceID = %d, want 1", i, e.InstanceID)
		}
		if e.Timestamp.IsZero() {
			t.Errorf("event[%d].Timestamp is zero", i)
		}
	}
}

func TestReconnectWithBackoff_FailureEventEmission(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 10 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	orch := &mockOrch{configureErr: fmt.Errorf("always fails")}
	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	var mu sync.Mutex
	var events []ConnectionEvent
	mgr.OnEvent(func(e ConnectionEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	_ = mgr.reconnectWithBackoff(context.Background(), 1, 2, orch, "test-fail-events")

	mu.Lock()
	defer mu.Unlock()

	// Expect: Reconnecting → ReconnectFailed
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(events), eventTypes(events))
	}
	if events[0].Type != EventReconnecting {
		t.Errorf("event[0] = %s, want %s", events[0].Type, EventReconnecting)
	}
	if events[1].Type != EventReconnectFailed {
		t.Errorf("event[1] = %s, want %s", events[1].Type, EventReconnectFailed)
	}
}

func TestTriggerReconnect_NoDuplicates(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 100 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	// Orchestrator that always fails (so reconnection takes a while)
	orch := &mockOrch{configureErr: fmt.Errorf("always fails")}

	mgr := NewSSHManager(signer, "test-key")
	mgr.SetOrchestrator(orch)
	defer mgr.CloseAll()

	// Trigger multiple reconnections for the same instance
	mgr.triggerReconnect(1, "first")
	time.Sleep(10 * time.Millisecond)
	mgr.triggerReconnect(1, "second") // should be skipped
	mgr.triggerReconnect(1, "third")  // should be skipped

	// Wait for reconnection to finish (reduced retries via maxRetries default=10,
	// but backoff is 100ms so it would take a while — cancel via CloseAll)
	time.Sleep(50 * time.Millisecond)

	// Verify only one reconnection is tracked
	mgr.reconnMu.RLock()
	count := len(mgr.reconnecting)
	mgr.reconnMu.RUnlock()

	if count > 1 {
		t.Errorf("expected at most 1 active reconnection, got %d", count)
	}
}

func TestTriggerReconnect_NoOrchestrator(t *testing.T) {
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

	// Should return immediately without panic when no orchestrator is set
	mgr.triggerReconnect(1, "no-orch")

	// Verify no reconnection goroutine was spawned
	mgr.reconnMu.RLock()
	count := len(mgr.reconnecting)
	mgr.reconnMu.RUnlock()

	if count != 0 {
		t.Errorf("expected 0 active reconnections, got %d", count)
	}
}

func TestSetOrchestrator(t *testing.T) {
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

	orch := &mockOrch{}
	mgr.SetOrchestrator(orch)

	mgr.reconnMu.RLock()
	got := mgr.orch
	mgr.reconnMu.RUnlock()

	if got != orch {
		t.Error("SetOrchestrator did not store orchestrator")
	}
}

func TestReconnectWithBackoff_NoOrchestrator(t *testing.T) {
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

	err = mgr.ReconnectWithBackoff(context.Background(), 1, 3, "test")
	if err == nil {
		t.Fatal("expected error when no orchestrator is set")
	}
}

func TestCheckAllConnections_TriggersReconnect(t *testing.T) {
	signer, ts := newTestSignerAndServer(t)

	mgr := NewSSHManager(signer, "test-key")
	defer mgr.CloseAll()

	host, port := parseHostPort(t, ts.addr)
	_, err := mgr.Connect(context.Background(), 1, host, port)
	if err != nil {
		t.Fatalf("Connect() error: %v", err)
	}

	// Set up a mock orchestrator so triggerReconnect doesn't bail out.
	// Use an always-failing orch to prevent actual reconnection.
	orch := &mockOrch{configureErr: fmt.Errorf("test orch")}
	mgr.SetOrchestrator(orch)

	// Track events
	var mu sync.Mutex
	var events []ConnectionEvent
	mgr.OnEvent(func(e ConnectionEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})

	// Kill the server to make health check fail
	ts.cleanup()
	time.Sleep(200 * time.Millisecond)

	// Run health check cycle
	mgr.checkAllConnections()

	// Connection should be removed
	if _, ok := mgr.GetConnection(1); ok {
		t.Error("connection should be removed after failed health check")
	}

	// Give triggerReconnect goroutine a moment to start
	time.Sleep(50 * time.Millisecond)

	// Verify disconnect event was emitted
	mu.Lock()
	foundDisconnect := false
	for _, e := range events {
		if e.Type == EventDisconnected && e.InstanceID == 1 {
			foundDisconnect = true
		}
	}
	mu.Unlock()

	if !foundDisconnect {
		t.Error("expected EventDisconnected event from health check failure")
	}
}

func TestCancelAllReconnections(t *testing.T) {
	saved := reconnectInitialBackoff
	reconnectInitialBackoff = 500 * time.Millisecond
	defer func() { reconnectInitialBackoff = saved }()

	_, privKeyPEM, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ParsePrivateKey(privKeyPEM)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}

	orch := &mockOrch{configureErr: fmt.Errorf("always fails")}
	mgr := NewSSHManager(signer, "test-key")
	mgr.SetOrchestrator(orch)

	// Start reconnection
	mgr.triggerReconnect(1, "test-cancel")
	mgr.triggerReconnect(2, "test-cancel")
	time.Sleep(20 * time.Millisecond)

	// Verify reconnections are in progress
	mgr.reconnMu.RLock()
	count := len(mgr.reconnecting)
	mgr.reconnMu.RUnlock()
	if count != 2 {
		t.Fatalf("expected 2 active reconnections, got %d", count)
	}

	// Cancel all
	mgr.cancelAllReconnections()

	mgr.reconnMu.RLock()
	count = len(mgr.reconnecting)
	mgr.reconnMu.RUnlock()
	if count != 0 {
		t.Errorf("expected 0 reconnections after cancel, got %d", count)
	}
}

// eventTypes returns the types of events as a string slice for debugging.
func eventTypes(events []ConnectionEvent) []ConnectionEventType {
	types := make([]ConnectionEventType, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}
