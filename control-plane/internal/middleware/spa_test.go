package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func newTestSPA() *SPAHandler {
	fsys := fstest.MapFS{
		"index.html":       {Data: []byte("<html>SPA</html>")},
		"assets/style.css": {Data: []byte("body{}")},
	}
	return NewSPAHandler(fsys)
}

func TestSPAHandler_ServesFile(t *testing.T) {
	t.Parallel()
	spa := newTestSPA()
	req := httptest.NewRequest("GET", "/assets/style.css", nil)
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "body{}" {
		t.Errorf("body = %q, want %q", w.Body.String(), "body{}")
	}
}

func TestSPAHandler_FallbackToIndex(t *testing.T) {
	t.Parallel()
	spa := newTestSPA()
	req := httptest.NewRequest("GET", "/some/spa/route", nil)
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "<html>SPA</html>" {
		t.Errorf("body = %q, want SPA index", w.Body.String())
	}
}

func TestSPAHandler_SkipsAPIRoutes(t *testing.T) {
	t.Parallel()
	spa := newTestSPA()
	req := httptest.NewRequest("GET", "/api/v1/instances", nil)
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("/api/ route: status = %d, want 404", w.Code)
	}
}

func TestSPAHandler_SkipsOpenclawRoutes(t *testing.T) {
	t.Parallel()
	spa := newTestSPA()
	req := httptest.NewRequest("GET", "/openclaw/something", nil)
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("/openclaw/ route: status = %d, want 404", w.Code)
	}
}

func TestSPAHandler_SkipsHealthRoute(t *testing.T) {
	t.Parallel()
	spa := newTestSPA()
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("/health route: status = %d, want 404", w.Code)
	}
}

func TestSPAHandler_RejectsNonGET(t *testing.T) {
	t.Parallel()
	spa := newTestSPA()
	req := httptest.NewRequest("POST", "/some/route", nil)
	w := httptest.NewRecorder()
	spa.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("POST: status = %d, want 404", w.Code)
	}
}
