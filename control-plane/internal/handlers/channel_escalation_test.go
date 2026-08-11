package handlers

import (
	"sync"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/channelhealth"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

type escalatorFixture struct {
	esc      *ChannelEscalator
	clock    time.Time
	mu       sync.Mutex
	restarts []uint
	notified []ChannelAlertPayload
	events   []database.ChannelHealthEvent
}

func newEscalatorFixture(t *testing.T) *escalatorFixture {
	t.Helper()
	setupHandlersTestDB(t)

	f := &escalatorFixture{clock: time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)}
	f.esc = NewChannelEscalator(ChannelEscalatorConfig{
		AlertThreshold:     3,
		RestartThreshold:   5,
		MaxRestartsPerHour: 3,
		RestartCooldown:    10 * time.Minute,
	})
	f.esc.now = func() time.Time { return f.clock }
	f.esc.dispatch = func(fn func()) { fn() }
	f.esc.restart = func(id uint, title, message string) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.restarts = append(f.restarts, id)
	}
	f.esc.notify = func(p ChannelAlertPayload) string {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.notified = append(f.notified, p)
		return webhookStatusSent
	}
	f.esc.recordEvent = func(ev database.ChannelHealthEvent) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.events = append(f.events, ev)
	}
	return f
}

func (f *escalatorFixture) advance(d time.Duration) { f.clock = f.clock.Add(d) }

func (f *escalatorFixture) snap(overall string) channelhealth.Snapshot {
	return channelhealth.Snapshot{
		InstanceID: 1,
		Overall:    overall,
		CheckedAt:  f.clock,
		Channels: []channelhealth.ChannelState{{
			Channel: "slack", AccountID: "default",
			Status: channelhealth.StatusDisconnected,
		}},
	}
}

// tick feeds one failing snapshot and advances the clock one interval.
func (f *escalatorFixture) tick(overall string) {
	f.esc.OnSnapshot(f.snap(overall))
	f.advance(time.Minute)
}

func (f *escalatorFixture) eventTypes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.events))
	for i, e := range f.events {
		out[i] = e.Type
	}
	return out
}

func enableAutoRestart(t *testing.T) {
	t.Helper()
	if err := database.SetSetting(settingChannelAutoRestartEnabled, "true"); err != nil {
		t.Fatalf("set setting: %v", err)
	}
}

func TestEscalator_NoActionBelowAlertThreshold(t *testing.T) {
	f := newEscalatorFixture(t)
	f.tick(channelhealth.OverallUnhealthy)
	f.tick(channelhealth.OverallUnhealthy)
	if len(f.events) != 0 || len(f.notified) != 0 {
		t.Fatalf("expected no actions below threshold, got events=%v", f.eventTypes())
	}
}

func TestEscalator_AlertOncePerIncident(t *testing.T) {
	f := newEscalatorFixture(t)
	for i := 0; i < 4; i++ {
		f.tick(channelhealth.OverallUnhealthy)
	}
	if got := f.eventTypes(); len(got) != 1 || got[0] != eventFailureDetected {
		t.Fatalf("expected one failure_detected, got %v", got)
	}
	if f.notified[0].Event != "channel_failure" {
		t.Fatalf("expected channel_failure payload, got %q", f.notified[0].Event)
	}
	if f.notified[0].ConsecutiveFailures != 3 {
		t.Fatalf("expected 3 consecutive failures in payload, got %d", f.notified[0].ConsecutiveFailures)
	}
	if f.notified[0].FailingSince == nil {
		t.Fatal("expected failing_since to be set")
	}
}

func TestEscalator_NoRestartWhenToggleOff(t *testing.T) {
	f := newEscalatorFixture(t)
	for i := 0; i < 8; i++ {
		f.tick(channelhealth.OverallUnhealthy)
	}
	if len(f.restarts) != 0 {
		t.Fatalf("expected no restarts with toggle off, got %d", len(f.restarts))
	}
}

func TestEscalator_RestartAtThresholdThenCooldown(t *testing.T) {
	f := newEscalatorFixture(t)
	enableAutoRestart(t)
	for i := 0; i < 5; i++ {
		f.tick(channelhealth.OverallUnhealthy)
	}
	if len(f.restarts) != 1 {
		t.Fatalf("expected exactly one restart at threshold, got %d", len(f.restarts))
	}
	if got := f.eventTypes(); len(got) != 2 || got[1] != eventAutoRestart {
		t.Fatalf("expected [failure_detected auto_restart], got %v", got)
	}
	// Failing checks during the 10m cooldown are ignored entirely.
	for i := 0; i < 9; i++ {
		f.tick(channelhealth.OverallUnhealthy)
	}
	if len(f.restarts) != 1 {
		t.Fatalf("cooldown violated: got %d restarts", len(f.restarts))
	}
	// After cooldown the counter restarts from zero: 5 more failing checks
	// trigger the second restart.
	for i := 0; i < 5; i++ {
		f.tick(channelhealth.OverallUnhealthy)
	}
	if len(f.restarts) != 2 {
		t.Fatalf("expected second restart after cooldown + threshold, got %d", len(f.restarts))
	}
}

func TestEscalator_CircuitBreaker(t *testing.T) {
	f := newEscalatorFixture(t)
	enableAutoRestart(t)
	// Drive three restarts (threshold 5 fails + 10m cooldown between).
	for r := 0; r < 3; r++ {
		for i := 0; i < 5; i++ {
			f.tick(channelhealth.OverallUnhealthy)
		}
		f.advance(10 * time.Minute)
	}
	if len(f.restarts) != 3 {
		t.Fatalf("expected 3 restarts before breaker, got %d", len(f.restarts))
	}
	// Fourth attempt within the hour trips the breaker instead.
	for i := 0; i < 5; i++ {
		f.tick(channelhealth.OverallUnhealthy)
	}
	if len(f.restarts) != 3 {
		t.Fatalf("breaker violated: got %d restarts", len(f.restarts))
	}
	types := f.eventTypes()
	if types[len(types)-1] != eventRestartLimitReached {
		t.Fatalf("expected restart_limit_reached, got %v", types)
	}
	// Breaker alert fires once per incident even as failures continue.
	for i := 0; i < 5; i++ {
		f.tick(channelhealth.OverallUnhealthy)
	}
	count := 0
	for _, tp := range f.eventTypes() {
		if tp == eventRestartLimitReached {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected one restart_limit_reached, got %d", count)
	}
}

func TestEscalator_RecoveryResetsIncidentKeepsRestartLog(t *testing.T) {
	f := newEscalatorFixture(t)
	enableAutoRestart(t)
	for r := 0; r < 3; r++ {
		for i := 0; i < 5; i++ {
			f.tick(channelhealth.OverallUnhealthy)
		}
		f.advance(10 * time.Minute)
	}
	f.tick(channelhealth.OverallHealthy)
	types := f.eventTypes()
	if types[len(types)-1] != eventRecovered {
		t.Fatalf("expected recovered event, got %v", types)
	}
	if f.notified[len(f.notified)-1].DurationSeconds <= 0 {
		t.Fatal("expected positive outage duration in recovery payload")
	}
	// New incident: restartTimes must survive the reset, so the breaker
	// trips immediately at the restart threshold (3 restarts already in
	// the rolling hour).
	for i := 0; i < 5; i++ {
		f.tick(channelhealth.OverallUnhealthy)
	}
	if len(f.restarts) != 3 {
		t.Fatalf("restart log lost across incident reset: got %d restarts", len(f.restarts))
	}
	// Second recovery fires exactly one more recovered event.
	f.tick(channelhealth.OverallHealthy)
	count := 0
	for _, tp := range f.eventTypes() {
		if tp == eventRecovered {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 recovered events, got %d", count)
	}
}

func TestEscalator_RecoveryWithoutAlertIsSilent(t *testing.T) {
	f := newEscalatorFixture(t)
	f.tick(channelhealth.OverallUnhealthy)
	f.tick(channelhealth.OverallHealthy)
	if len(f.events) != 0 {
		t.Fatalf("expected no events for sub-threshold blip, got %v", f.eventTypes())
	}
}

func TestEscalator_DegradedHoldsIncident(t *testing.T) {
	f := newEscalatorFixture(t)
	f.tick(channelhealth.OverallUnhealthy)
	f.tick(channelhealth.OverallUnhealthy)
	// Degraded neither counts nor resets.
	f.tick(channelhealth.OverallDegraded)
	f.tick(channelhealth.OverallUnknown)
	f.tick(channelhealth.OverallUnhealthy)
	if got := f.eventTypes(); len(got) != 1 || got[0] != eventFailureDetected {
		t.Fatalf("expected alert on 3rd failing check across hold, got %v", got)
	}
}

func TestEscalator_UnreachableCountsAsFailing(t *testing.T) {
	f := newEscalatorFixture(t)
	for i := 0; i < 3; i++ {
		f.tick(channelhealth.OverallUnreachable)
	}
	if got := f.eventTypes(); len(got) != 1 || got[0] != eventFailureDetected {
		t.Fatalf("expected alert for unreachable, got %v", got)
	}
}

func TestEscalator_ProductionRestartSkipsNonRunning(t *testing.T) {
	f := newEscalatorFixture(t)
	// Use the real restart dependency against a stopped instance row: it
	// must no-op via restartInstanceAsyncWithToast's status guard.
	inst := database.Instance{Name: "bot-x", DisplayName: "x", Status: "stopped"}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	f.esc.restartInstance(inst.ID, "t", "m")
	var got database.Instance
	if err := database.DB.First(&got, inst.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != "stopped" {
		t.Fatalf("expected stopped instance untouched, got status %q", got.Status)
	}
}
