package channelhealth

import (
	"fmt"
	"testing"
	"time"
)

var testNow = time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

func tp(t time.Time) *time.Time { return &t }

func TestEvaluateAccount(t *testing.T) {
	fresh := tp(testNow.Add(-1 * time.Minute))
	old := tp(testNow.Add(-31 * time.Minute))

	tests := []struct {
		name string
		acc  AccountState
		want string
	}{
		{
			name: "disabled account",
			acc:  AccountState{Enabled: false, Configured: true, Running: true, Connected: true},
			want: StatusDisabled,
		},
		{
			name: "unconfigured account",
			acc:  AccountState{Enabled: true, Configured: false, Running: true, Connected: true},
			want: StatusDisabled,
		},
		{
			name: "running but not connected",
			acc:  AccountState{Enabled: true, Configured: true, Running: true, Connected: false},
			want: StatusDisconnected,
		},
		{
			name: "enabled but not running",
			acc:  AccountState{Enabled: true, Configured: true, Running: false, Connected: false},
			want: StatusNotRunning,
		},
		{
			name: "connected socket mode with recent event",
			acc:  AccountState{Enabled: true, Configured: true, Running: true, Connected: true, Mode: "socket", LastEventAt: fresh},
			want: StatusHealthy,
		},
		{
			name: "connected socket mode with stale event",
			acc:  AccountState{Enabled: true, Configured: true, Running: true, Connected: true, Mode: "socket", LastEventAt: old},
			want: StatusStale,
		},
		{
			name: "connected http mode with old event is not stale",
			acc:  AccountState{Enabled: true, Configured: true, Running: true, Connected: true, Mode: "http", LastEventAt: old},
			want: StatusHealthy,
		},
		{
			name: "connected webhook mode with old event is not stale",
			acc:  AccountState{Enabled: true, Configured: true, Running: true, Connected: true, Mode: "webhook", LastEventAt: old},
			want: StatusHealthy,
		},
		{
			name: "connected persistent mode without lastEventAt",
			acc:  AccountState{Enabled: true, Configured: true, Running: true, Connected: true, Mode: "socket"},
			want: StatusHealthy,
		},
		{
			name: "stale boundary: exactly at threshold is not stale",
			acc:  AccountState{Enabled: true, Configured: true, Running: true, Connected: true, Mode: "socket", LastEventAt: tp(testNow.Add(-StaleThreshold))},
			want: StatusHealthy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EvaluateAccount(tt.acc, testNow); got != tt.want {
				t.Errorf("EvaluateAccount() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildChannelStates(t *testing.T) {
	now := time.Date(2026, 8, 6, 12, 1, 0, 0, time.UTC)
	wantEvent := time.Date(2026, 8, 6, 11, 59, 0, 0, time.UTC)
	eventMs := wantEvent.UnixMilli()

	payload := []byte(fmt.Sprintf(`{
		"ts": %d,
		"channelOrder": ["slack", "telegram"],
		"channels": {"slack": {"configured": true}, "telegram": {"configured": true}},
		"channelAccounts": {
			"slack": [{
				"accountId": "default",
				"enabled": true,
				"configured": true,
				"running": true,
				"connected": true,
				"lastConnectedAt": %d,
				"lastEventAt": %d,
				"lastInboundAt": %d,
				"lastError": null,
				"reconnectAttempts": 0,
				"mode": "socket",
				"restartPending": false
			}],
			"telegram": [{
				"accountId": "bot1",
				"enabled": true,
				"configured": true,
				"running": true,
				"connected": false,
				"lastError": {"message": "invalid token", "code": "AUTH"},
				"reconnectAttempts": 3,
				"mode": "socket"
			}, {
				"accountId": "bot2",
				"enabled": false,
				"configured": true,
				"running": false,
				"connected": false
			}]
		},
		"channelDefaultAccountId": {"slack": "default"}
	}`, now.UnixMilli(), wantEvent.Add(-1*time.Hour).UnixMilli(), eventMs, eventMs))

	states, err := BuildChannelStates(payload, now)
	if err != nil {
		t.Fatalf("BuildChannelStates() error: %v", err)
	}
	if len(states) != 3 {
		t.Fatalf("got %d states, want 3", len(states))
	}

	// Sorted by (channel, account_id): slack/default, telegram/bot1, telegram/bot2.
	slack := states[0]
	if slack.Channel != "slack" || slack.AccountID != "default" {
		t.Fatalf("states[0] = %s/%s, want slack/default", slack.Channel, slack.AccountID)
	}
	if slack.Status != StatusHealthy {
		t.Errorf("slack status = %q, want %q", slack.Status, StatusHealthy)
	}
	if slack.LastEventAt == nil || !slack.LastEventAt.Equal(wantEvent) {
		t.Errorf("slack.LastEventAt = %v, want %v", slack.LastEventAt, wantEvent)
	}
	if slack.LastInboundAt == nil || !slack.LastInboundAt.Equal(wantEvent) {
		t.Errorf("slack.LastInboundAt = %v, want %v", slack.LastInboundAt, wantEvent)
	}
	if slack.LastOutboundAt != nil {
		t.Errorf("slack.LastOutboundAt = %v, want nil (absent field)", slack.LastOutboundAt)
	}
	if slack.LastError != "" {
		t.Errorf("slack.LastError = %q, want empty (null on wire)", slack.LastError)
	}
	if !slack.CheckedAt.Equal(now) {
		t.Errorf("slack.CheckedAt = %v, want %v", slack.CheckedAt, now)
	}

	bot1 := states[1]
	if bot1.Channel != "telegram" || bot1.AccountID != "bot1" {
		t.Fatalf("states[1] = %s/%s, want telegram/bot1", bot1.Channel, bot1.AccountID)
	}
	if bot1.Status != StatusDisconnected {
		t.Errorf("bot1 status = %q, want %q", bot1.Status, StatusDisconnected)
	}
	if bot1.LastError != "invalid token" {
		t.Errorf("bot1.LastError = %q, want %q (object message extracted)", bot1.LastError, "invalid token")
	}
	if bot1.ReconnectAttempts != 3 {
		t.Errorf("bot1.ReconnectAttempts = %d, want 3", bot1.ReconnectAttempts)
	}

	bot2 := states[2]
	if bot2.Status != StatusDisabled {
		t.Errorf("bot2 status = %q, want %q (disabled accounts are recorded)", bot2.Status, StatusDisabled)
	}
}

func TestBuildChannelStatesDefensive(t *testing.T) {
	t.Run("missing fields default sensibly", func(t *testing.T) {
		payload := []byte(`{"channelAccounts": {"slack": [{}]}}`)
		states, err := BuildChannelStates(payload, testNow)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if len(states) != 1 {
			t.Fatalf("got %d states, want 1", len(states))
		}
		s := states[0]
		if s.AccountID != "default" {
			t.Errorf("AccountID = %q, want %q", s.AccountID, "default")
		}
		// enabled/configured default true, running/connected default false
		// => enabled && !running => not_running.
		if s.Status != StatusNotRunning {
			t.Errorf("Status = %q, want %q", s.Status, StatusNotRunning)
		}
		if s.LastEventAt != nil || s.LastInboundAt != nil || s.LastOutboundAt != nil {
			t.Errorf("timestamps should be nil for absent fields")
		}
	})

	t.Run("lastError as plain string", func(t *testing.T) {
		payload := []byte(`{"channelAccounts": {"slack": [{"running": true, "lastError": "boom"}]}}`)
		states, err := BuildChannelStates(payload, testNow)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if states[0].LastError != "boom" {
			t.Errorf("LastError = %q, want %q", states[0].LastError, "boom")
		}
	})

	t.Run("lastError object without message falls back to raw JSON", func(t *testing.T) {
		payload := []byte(`{"channelAccounts": {"slack": [{"lastError": {"weird": 1}}]}}`)
		states, err := BuildChannelStates(payload, testNow)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if states[0].LastError != `{"weird": 1}` {
			t.Errorf("LastError = %q, want raw JSON", states[0].LastError)
		}
	})

	t.Run("empty payload", func(t *testing.T) {
		states, err := BuildChannelStates([]byte(`{}`), testNow)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if len(states) != 0 {
			t.Errorf("got %d states, want 0", len(states))
		}
	})

	t.Run("invalid payload", func(t *testing.T) {
		if _, err := BuildChannelStates([]byte(`not json`), testNow); err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("zero-ms timestamp treated as absent", func(t *testing.T) {
		payload := []byte(`{"channelAccounts": {"slack": [{"lastEventAt": 0}]}}`)
		states, err := BuildChannelStates(payload, testNow)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if states[0].LastEventAt != nil {
			t.Errorf("LastEventAt = %v, want nil for 0", states[0].LastEventAt)
		}
	})
}

func TestDeriveOverall(t *testing.T) {
	ch := func(status string) ChannelState { return ChannelState{Status: status} }

	tests := []struct {
		name      string
		reachable bool
		checked   bool
		channels  []ChannelState
		want      string
	}{
		{"never checked", false, false, nil, OverallUnknown},
		{"gateway unreachable", false, true, []ChannelState{ch(StatusHealthy)}, OverallUnreachable},
		{"no channels", true, true, nil, OverallNoChannels},
		{"only disabled channels", true, true, []ChannelState{ch(StatusDisabled)}, OverallNoChannels},
		{"one disconnected", true, true, []ChannelState{ch(StatusHealthy), ch(StatusDisconnected)}, OverallUnhealthy},
		{"one not_running", true, true, []ChannelState{ch(StatusHealthy), ch(StatusNotRunning)}, OverallUnhealthy},
		{"disconnected trumps stale", true, true, []ChannelState{ch(StatusStale), ch(StatusDisconnected)}, OverallUnhealthy},
		{"one stale", true, true, []ChannelState{ch(StatusHealthy), ch(StatusStale)}, OverallDegraded},
		{"one unknown", true, true, []ChannelState{ch(StatusHealthy), ch(StatusUnknown)}, OverallDegraded},
		{"all healthy", true, true, []ChannelState{ch(StatusHealthy), ch(StatusHealthy)}, OverallHealthy},
		{"healthy plus disabled", true, true, []ChannelState{ch(StatusHealthy), ch(StatusDisabled)}, OverallHealthy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveOverall(tt.reachable, tt.checked, tt.channels); got != tt.want {
				t.Errorf("DeriveOverall() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListenerFiresOnEveryStore(t *testing.T) {
	m := New(nil, time.Minute)
	var got []string
	m.SetListener(func(snap Snapshot) { got = append(got, snap.Overall) })

	// Same overall twice: the listener must fire both times even though
	// the transition-logging path early-returns on no-change.
	m.store(Snapshot{InstanceID: 1, Overall: OverallUnhealthy})
	m.store(Snapshot{InstanceID: 1, Overall: OverallUnhealthy})
	m.store(Snapshot{InstanceID: 1, Overall: OverallHealthy})

	want := []string{OverallUnhealthy, OverallUnhealthy, OverallHealthy}
	if len(got) != len(want) {
		t.Fatalf("expected %d listener calls, got %d", len(want), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}
