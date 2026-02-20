package neko

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildEmbedOptions_Defaults(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("VNC_RESOLUTION", "")

	opts := buildEmbedOptions()

	if opts.Display != ":0" {
		t.Errorf("Display = %q, want %q", opts.Display, ":0")
	}
	if opts.ScreenSize != "1920x1080@30" {
		t.Errorf("ScreenSize = %q, want %q", opts.ScreenSize, "1920x1080@30")
	}
	if opts.PathPrefix != "/" {
		t.Errorf("PathPrefix = %q, want %q", opts.PathPrefix, "/")
	}
}

func TestBuildEmbedOptions_CustomDisplay(t *testing.T) {
	t.Setenv("DISPLAY", ":1")
	t.Setenv("VNC_RESOLUTION", "")

	opts := buildEmbedOptions()

	if opts.Display != ":1" {
		t.Errorf("Display = %q, want %q", opts.Display, ":1")
	}
}

func TestBuildEmbedOptions_CustomResolution(t *testing.T) {
	t.Setenv("VNC_RESOLUTION", "1280x720")

	opts := buildEmbedOptions()

	if opts.ScreenSize != "1280x720@30" {
		t.Errorf("ScreenSize = %q, want %q", opts.ScreenSize, "1280x720@30")
	}
}

func TestBuildEmbedOptions_ResolutionWithRate(t *testing.T) {
	t.Setenv("VNC_RESOLUTION", "1280x720@60")

	opts := buildEmbedOptions()

	if opts.ScreenSize != "1280x720@60" {
		t.Errorf("ScreenSize = %q, want %q", opts.ScreenSize, "1280x720@60")
	}
}

func TestNekoServer_NilHandler(t *testing.T) {
	var n *NekoServer
	h := n.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestNekoServer_ZeroValueHandler(t *testing.T) {
	n := &NekoServer{} // embedded is nil
	h := n.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestNekoServer_NilStart(t *testing.T) {
	var n *NekoServer
	err := n.Start(context.Background())
	if err == nil {
		t.Error("Start on nil NekoServer should return error")
	}
}

func TestNekoServer_NilStop(t *testing.T) {
	var n *NekoServer
	err := n.Stop()
	if err != nil {
		t.Errorf("Stop on nil NekoServer should return nil, got %v", err)
	}
}
