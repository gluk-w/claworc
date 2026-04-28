package analytics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&database.Setting{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	database.DB = db
	t.Cleanup(func() { database.DB = nil })
}

// captureServer spins up a test HTTP server that records request bodies.
func captureServer(t *testing.T) (*httptest.Server, *[][]byte, *sync.WaitGroup) {
	t.Helper()
	var mu sync.Mutex
	var bodies [][]byte
	var wg sync.WaitGroup
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer wg.Done()
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	SetEndpoint(srv.URL)
	t.Cleanup(func() { SetEndpoint(CollectorURL) })
	return srv, &bodies, &wg
}

func TestInstallationID_GeneratedAndStable(t *testing.T) {
	setupDB(t)
	id1, err := GetOrCreateInstallationID()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(id1) != 32 {
		t.Errorf("expected 32-hex id, got %q", id1)
	}
	id2, _ := GetOrCreateInstallationID()
	if id1 != id2 {
		t.Errorf("installation id is not stable: %q vs %q", id1, id2)
	}
}

func TestTrack_GatedByConsent(t *testing.T) {
	setupDB(t)
	_, bodies, wg := captureServer(t)

	// Default unset — should not send.
	Track(context.Background(), EventInstanceCreated, nil)
	time.Sleep(50 * time.Millisecond)
	if len(*bodies) != 0 {
		t.Fatalf("expected no events while consent unset, got %d", len(*bodies))
	}

	// Opt out — should not send.
	database.SetSetting(settingConsent, ConsentOptOut)
	Track(context.Background(), EventInstanceCreated, nil)
	time.Sleep(50 * time.Millisecond)
	if len(*bodies) != 0 {
		t.Fatalf("expected no events while opted out, got %d", len(*bodies))
	}

	// Opt in — should send.
	database.SetSetting(settingConsent, ConsentOptIn)
	wg.Add(1)
	Track(context.Background(), EventInstanceCreated, map[string]any{"total_instances": 3})
	wg.Wait()
	if len(*bodies) != 1 {
		t.Fatalf("expected 1 event, got %d", len(*bodies))
	}
	var p payload
	if err := json.Unmarshal((*bodies)[0], &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.Event != EventInstanceCreated {
		t.Errorf("event = %q", p.Event)
	}
	if len(p.InstallationID) != 32 {
		t.Errorf("installation_id missing/short: %q", p.InstallationID)
	}
	if p.Props["total_instances"].(float64) != 3 {
		t.Errorf("props not propagated: %#v", p.Props)
	}
}

func TestTrack_UnknownEventDropped(t *testing.T) {
	setupDB(t)
	_, bodies, _ := captureServer(t)
	database.SetSetting(settingConsent, ConsentOptIn)
	Track(context.Background(), "made_up_event", nil)
	time.Sleep(50 * time.Millisecond)
	if len(*bodies) != 0 {
		t.Fatalf("unknown events must not be sent")
	}
}

func TestTrack_NonBlocking(t *testing.T) {
	setupDB(t)
	// Server that sleeps longer than the test would tolerate synchronously.
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer slow.Close()
	SetEndpoint(slow.URL)
	defer SetEndpoint(CollectorURL)
	database.SetSetting(settingConsent, ConsentOptIn)

	start := time.Now()
	Track(context.Background(), EventInstanceCreated, nil)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("Track blocked caller for %v", elapsed)
	}
}

func TestTrackForceOptOut_BypassesConsent(t *testing.T) {
	setupDB(t)
	_, bodies, wg := captureServer(t)
	database.SetSetting(settingConsent, ConsentOptOut)
	wg.Add(1)
	TrackForceOptOut()
	wg.Wait()
	if len(*bodies) != 1 {
		t.Fatalf("expected 1 opt_out event, got %d", len(*bodies))
	}
	var p payload
	if err := json.Unmarshal((*bodies)[0], &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Event != EventOptOut {
		t.Errorf("event = %q", p.Event)
	}
}

func TestGetConsent_DefaultUnset(t *testing.T) {
	setupDB(t)
	if c := GetConsent(); c != ConsentUnset {
		t.Errorf("default consent = %q, want %q", c, ConsentUnset)
	}
}
