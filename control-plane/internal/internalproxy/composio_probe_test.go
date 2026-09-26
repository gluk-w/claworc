package internalproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// probeServer stands up a fake Composio that answers each probe path with a
// caller-supplied status. Paths not listed default to 200.
func probeServer(t *testing.T, statuses map[string]int) (*ComposioClient, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		status := http.StatusOK
		for prefix, s := range statuses {
			if strings.HasPrefix(r.URL.Path, prefix) {
				status = s
				break
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status >= 400 {
			_, _ = w.Write([]byte(`{"error":{"message":"nope","slug":"Some_Slug"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)

	old := ComposioBaseURL
	ComposioBaseURL = srv.URL
	t.Cleanup(func() { ComposioBaseURL = old })

	return NewComposioClient("test-key"), &calls
}

func checkByID(t *testing.T, report PermissionReport, id string) PermissionCheck {
	t.Helper()
	for _, c := range report.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %q not present in report (%d checks)", id, len(report.Checks))
	return PermissionCheck{}
}

func TestCheckPermissions_AllGranted(t *testing.T) {
	// Write probes get validation errors — that means the request cleared the
	// auth layer, so the permission is present.
	client, _ := probeServer(t, map[string]int{
		"/auth_configs":            http.StatusBadRequest,
		"/connected_accounts/link": http.StatusUnprocessableEntity,
		"/tools/execute/":          http.StatusNotFound,
	})

	report := client.CheckPermissions(context.Background())
	if !report.OK {
		t.Fatalf("expected ok, got %+v", report)
	}
	if report.InvalidKey {
		t.Error("expected invalid_key=false")
	}
	if len(report.Checks) != len(composioProbes)+1 {
		t.Errorf("expected %d checks, got %d", len(composioProbes)+1, len(report.Checks))
	}
	for _, c := range report.Checks {
		if !c.OK {
			t.Errorf("check %q failed unexpectedly: %+v", c.ID, c)
		}
	}
}

func TestCheckPermissions_InvalidKeyShortCircuits(t *testing.T) {
	client, calls := probeServer(t, map[string]int{"/toolkits": http.StatusUnauthorized})

	report := client.CheckPermissions(context.Background())
	if report.OK || !report.InvalidKey {
		t.Fatalf("expected invalid key report, got %+v", report)
	}
	if len(report.Checks) != 1 {
		t.Errorf("expected only the toolkits check, got %d", len(report.Checks))
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("expected exactly 1 upstream call, got %d", got)
	}
}

func TestCheckPermissions_ToolkitsForbiddenIsNotInvalidKey(t *testing.T) {
	// 403 means the key is real but lacks the Toolkits scope — every other area
	// still needs probing.
	client, calls := probeServer(t, map[string]int{"/toolkits": http.StatusForbidden})

	report := client.CheckPermissions(context.Background())
	if report.OK {
		t.Error("expected ok=false")
	}
	if report.InvalidKey {
		t.Error("403 on toolkits must not be reported as an invalid key")
	}
	if chk := checkByID(t, report, "toolkits_read"); chk.OK || chk.Status != http.StatusForbidden {
		t.Errorf("unexpected toolkits check: %+v", chk)
	}
	if got := atomic.LoadInt32(calls); got != int32(len(composioProbes)+1) {
		t.Errorf("expected all probes to run, got %d calls", got)
	}
}

func TestCheckPermissions_MissingWriteScope(t *testing.T) {
	client, _ := probeServer(t, map[string]int{
		"/connected_accounts/link": http.StatusForbidden,
		"/auth_configs":            http.StatusBadRequest,
		"/tools/execute/":          http.StatusNotFound,
	})

	report := client.CheckPermissions(context.Background())
	if report.OK {
		t.Error("expected ok=false")
	}
	failed := checkByID(t, report, "connected_accounts_write")
	if failed.OK {
		t.Error("expected connected_accounts_write to fail")
	}
	if !strings.Contains(failed.Detail, "Some_Slug") {
		t.Errorf("expected Composio slug in detail, got %q", failed.Detail)
	}
	for _, id := range []string{"toolkits_read", "tools_read", "connected_accounts_read", "auth_configs_write", "tool_execution"} {
		if chk := checkByID(t, report, id); !chk.OK {
			t.Errorf("check %q should have passed: %+v", id, chk)
		}
	}
}

func TestCheckPermissions_TransportError(t *testing.T) {
	old := ComposioBaseURL
	// Port 1 on loopback: nothing listens there, so the dial fails fast.
	ComposioBaseURL = "http://127.0.0.1:1"
	t.Cleanup(func() { ComposioBaseURL = old })

	report := NewComposioClient("test-key").CheckPermissions(context.Background())
	if report.OK {
		t.Error("expected ok=false")
	}
	if report.InvalidKey {
		t.Error("a transport failure is not an invalid key")
	}
	chk := checkByID(t, report, "toolkits_read")
	if chk.Status != 0 || chk.Detail == "" {
		t.Errorf("expected status 0 with a detail, got %+v", chk)
	}
}

func TestWriteProbePassed(t *testing.T) {
	for status, want := range map[int]bool{
		http.StatusBadRequest:          true,
		http.StatusNotFound:            true,
		http.StatusUnprocessableEntity: true,
		http.StatusOK:                  true,
		http.StatusUnauthorized:        false,
		http.StatusForbidden:           false,
	} {
		if got := writeProbePassed(status); got != want {
			t.Errorf("writeProbePassed(%d) = %v, want %v", status, got, want)
		}
	}
}
