// Package analytics implements opt-in, anonymous product telemetry. It exposes
// a single fire-and-forget Track() function that delivers events to the
// Claworc collector. All calls are gated by the analytics_consent setting and
// silently no-op unless the user has opted in (with one exception: the
// EventOptOut event itself is sent via TrackForceOptOut so the toggle-off
// transition is visible to us).
package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// CollectorURL is the Claworc analytics ingestion endpoint.
const CollectorURL = "https://claworc.com/stats/collect"

// Version is set at startup by the main package; defaults to "dev" for tests.
var Version = "dev"

// httpClient is package-level so tests can swap it. Keep timeout snug — Track
// is fire-and-forget but we still bound goroutine lifetime.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// endpoint is the URL Track POSTs to. Tests override this; production uses
// CollectorURL.
var endpoint atomic.Value // string

func init() {
	endpoint.Store(CollectorURL)
}

// SetEndpoint overrides the collector URL (intended for tests).
func SetEndpoint(url string) { endpoint.Store(url) }

// payload is the JSON body posted to the collector. Field names are part of
// the wire contract — keep in sync with the worker.
type payload struct {
	InstallationID string         `json:"installation_id"`
	Event          string         `json:"event"`
	Props          map[string]any `json:"props,omitempty"`
	TS             int64          `json:"ts"`
	Version        string         `json:"version"`
}

// Track records an event if the user has opted in. Non-blocking: spawns a
// goroutine and swallows all errors. Unknown event names are dropped.
func Track(ctx context.Context, event string, props map[string]any) {
	if !AllowedEvents[event] {
		return
	}
	if GetConsent() != ConsentOptIn {
		return
	}
	send(event, props)
}

// TrackForceOptOut sends the EventOptOut event without checking consent. Used
// to record the opt-in→opt-out transition before the consent flips.
func TrackForceOptOut() {
	send(EventOptOut, nil)
}

func send(event string, props map[string]any) {
	id, err := GetOrCreateInstallationID()
	if err != nil || id == "" {
		return
	}
	body := payload{
		InstallationID: id,
		Event:          event,
		Props:          props,
		TS:             time.Now().Unix(),
		Version:        Version,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return
	}
	url, _ := endpoint.Load().(string)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
		if err != nil {
			log.Printf("analytics: build request for %s failed: %v", event, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		log.Printf("analytics: POST %s event=%s installation_id=%s", url, event, id)
		resp, err := httpClient.Do(req)
		if err != nil {
			log.Printf("analytics: POST %s event=%s failed: %v", url, event, err)
			return
		}
		log.Printf("analytics: POST %s event=%s status=%d", url, event, resp.StatusCode)
		_ = resp.Body.Close()
	}()
}
