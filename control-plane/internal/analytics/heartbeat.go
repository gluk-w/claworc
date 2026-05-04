package analytics

import (
	"context"
	"math/rand"
	"time"
)

// HeartbeatInterval is the cadence between heartbeat pings.
var HeartbeatInterval = 24 * time.Hour

// HeartbeatStartupDelayMax bounds the random jitter applied before the first
// ping, so a fleet of installations restarting together doesn't burst the
// collector. Set small in tests.
var HeartbeatStartupDelayMax = 30 * time.Second

// StartHeartbeat launches a goroutine that emits a heartbeat event shortly
// after startup and then once every HeartbeatInterval. Track() handles the
// consent gate, so this is safe to call unconditionally. The goroutine exits
// when ctx is cancelled.
func StartHeartbeat(ctx context.Context) {
	go func() {
		delay := time.Duration(0)
		if HeartbeatStartupDelayMax > 0 {
			delay = time.Duration(rand.Int63n(int64(HeartbeatStartupDelayMax)))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		Track(ctx, EventHeartbeat, nil)

		ticker := time.NewTicker(HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				Track(ctx, EventHeartbeat, nil)
			}
		}
	}()
}
