//go:build composio_live

// composio_live_test.go pins the parts of Composio's REST contract that Claworc
// depends on and that fail *silently* when they drift: request paths and query
// string parameters.
//
// Composio ignores unknown query params instead of rejecting them, so a renamed
// filter does not surface as an error — it comes back as a successful response
// carrying the wrong data. That is exactly how `toolkit_slugs` (plural) shipped:
// every generated skill listed another toolkit's tools and nothing failed. These
// tests therefore assert on the *effect* of each parameter, not just on a 2xx.
//
// Run nightly by .github/workflows/composio-contract.yml against the real API:
//
//	COMPOSIO_API_KEY=... go test -tags composio_live -count=1 -v \
//	  ./internal/internalproxy/ -run TestComposioContract
//
// Nothing here mutates state at Composio: reads are plain GETs, and the write
// paths are probed with bodies that cannot produce a valid create.

package internalproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"
)

// contractToolkit is the toolkit the tool-filter assertions are made against. It
// needs to be Composio-managed, always present, and have enough tools that
// "important" is a strict subset and a 1-item page is not the last page.
const contractToolkit = "gmail"

// contractUserID is a Composio user_id that must never have a connected
// account, so the tool-execution probe cannot actually run anything.
const contractUserID = "claworc-ci-contract-probe"

func liveClient(t *testing.T) *ComposioClient {
	t.Helper()
	key := os.Getenv("COMPOSIO_API_KEY")
	if key == "" {
		t.Skip("COMPOSIO_API_KEY not set; skipping live Composio contract test")
	}
	return NewComposioClient(key)
}

func liveContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// rawTools issues GET /tools with exactly the given query and returns the parsed
// items plus the next cursor. It deliberately bypasses ListToolkitTools, whose
// client-side toolkit filter would mask a broken server-side filter.
func rawTools(t *testing.T, c *ComposioClient, q url.Values) ([]json.RawMessage, string) {
	t.Helper()
	status, body, err := c.do(liveContext(t), http.MethodGet, "/tools?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("GET /tools?%s: status %d: %v", q.Encode(), status, err)
	}
	items, next := parseToolsPage(body)
	return items, next
}

// itemToolkitSlug reads the owning toolkit slug off a raw tool item.
func itemToolkitSlug(t *testing.T, raw json.RawMessage) (toolSlug, toolkitSlug string) {
	t.Helper()
	var item struct {
		Slug    string `json:"slug"`
		Toolkit struct {
			Slug string `json:"slug"`
		} `json:"toolkit"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("decode tool item: %v", err)
	}
	return item.Slug, item.Toolkit.Slug
}

// TestComposioContract_ToolkitCatalog covers GET /toolkits — the wizard's
// catalog — and the managed_by filter.
func TestComposioContract_ToolkitCatalog(t *testing.T) {
	c := liveClient(t)
	toolkits, err := c.ListOAuthToolkits(liveContext(t))
	if err != nil {
		t.Fatalf("ListOAuthToolkits: %v", err)
	}
	if len(toolkits) == 0 {
		t.Fatal("GET /toolkits?managed_by=composio returned no toolkits — path, filter, or response envelope changed")
	}
	var found bool
	for _, tk := range toolkits {
		if tk.Slug == contractToolkit {
			found = true
		}
		if tk.Slug == "" || tk.Name == "" {
			t.Errorf("toolkit missing slug/name: %+v", tk)
		}
	}
	if !found {
		t.Errorf("%q not in the managed_by=composio catalog (%d toolkits) — the filter or the slug changed",
			contractToolkit, len(toolkits))
	}
}

// TestComposioContract_ToolkitDetail covers GET /toolkits/{slug}, the source of
// the generated skill's name and description.
func TestComposioContract_ToolkitDetail(t *testing.T) {
	c := liveClient(t)
	detail, err := c.GetToolkit(liveContext(t), contractToolkit)
	if err != nil {
		t.Fatalf("GetToolkit(%q): %v", contractToolkit, err)
	}
	if detail.Name == "" {
		t.Error("toolkit name empty — it moved out of both the top level and meta")
	}
	if detail.Description == "" {
		t.Error("toolkit description empty — it moved out of both the top level and meta")
	}
}

// TestComposioContract_ToolkitSlugFilter is the regression guard for the bug
// this suite exists for: GET /tools?toolkit_slug=<slug> must actually restrict
// the result to that toolkit. A renamed or dropped parameter comes back 200 with
// the entire catalog, which previously ended up verbatim in every skill.
func TestComposioContract_ToolkitSlugFilter(t *testing.T) {
	c := liveClient(t)

	q := url.Values{"toolkit_slug": {contractToolkit}, "limit": {"50"}}
	items, _ := rawTools(t, c, q)
	if len(items) == 0 {
		t.Fatalf("GET /tools?toolkit_slug=%s returned nothing — the parameter or the path changed", contractToolkit)
	}
	for _, raw := range items {
		toolSlug, tkSlug := itemToolkitSlug(t, raw)
		if tkSlug != contractToolkit {
			t.Fatalf("filter ignored: %s belongs to toolkit %q, want %q — "+
				"Composio accepted the request but did not filter it (see ListToolkitTools)",
				toolSlug, tkSlug, contractToolkit)
		}
	}

	// The unfiltered catalog must be visibly larger, otherwise the assertion
	// above would pass even if the filter stopped working.
	all, _ := rawTools(t, c, url.Values{"limit": {"50"}})
	var foreign int
	for _, raw := range all {
		if _, tkSlug := itemToolkitSlug(t, raw); tkSlug != contractToolkit {
			foreign++
		}
	}
	if foreign == 0 {
		t.Errorf("unfiltered GET /tools returned only %q tools; the filter assertion above is no longer meaningful",
			contractToolkit)
	}
}

// TestComposioContract_ImportantFilter covers ?important=true, which decides
// which tools the generated skill documents in full.
func TestComposioContract_ImportantFilter(t *testing.T) {
	c := liveClient(t)

	base := url.Values{"toolkit_slug": {contractToolkit}, "limit": {"100"}}
	all, _ := rawTools(t, c, base)

	important := url.Values{}
	for k, v := range base {
		important[k] = v
	}
	important.Set("important", "true")
	imp, _ := rawTools(t, c, important)

	if len(imp) == 0 {
		t.Fatalf("?important=true returned no tools for %q — the parameter or its values changed", contractToolkit)
	}
	if len(imp) >= len(all) {
		t.Errorf("?important=true returned %d of %d tools — it no longer narrows the result, so "+
			"every tool would be rendered in full in the skill", len(imp), len(all))
	}
}

// TestComposioContract_Pagination covers the cursor contract. If next_cursor is
// renamed, listToolsRaw silently stops after the first page and the skill
// documents only part of the toolkit.
func TestComposioContract_Pagination(t *testing.T) {
	c := liveClient(t)

	q := url.Values{"toolkit_slug": {contractToolkit}, "limit": {"1"}}
	first, next := rawTools(t, c, q)
	if len(first) != 1 {
		t.Fatalf("limit=1 returned %d items — the limit parameter changed", len(first))
	}
	if next == "" {
		t.Fatalf("next_cursor empty on a limit=1 page of %q — the cursor field was renamed or pagination changed",
			contractToolkit)
	}

	q.Set("cursor", next)
	second, _ := rawTools(t, c, q)
	if len(second) != 1 {
		t.Fatalf("cursor page returned %d items", len(second))
	}
	firstSlug, _ := itemToolkitSlug(t, first[0])
	secondSlug, _ := itemToolkitSlug(t, second[0])
	if firstSlug == secondSlug {
		t.Errorf("cursor %q returned the same tool %q — the cursor parameter is being ignored",
			next, firstSlug)
	}
}

// TestComposioContract_ToolItemShape covers the tool fields the skill renderer
// reads: slug, description, and the JSON-Schema input/output parameter objects.
func TestComposioContract_ToolItemShape(t *testing.T) {
	c := liveClient(t)

	tools, err := c.ListToolkitTools(liveContext(t), contractToolkit, true)
	if err != nil {
		t.Fatalf("ListToolkitTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatalf("no important tools parsed for %q", contractToolkit)
	}

	var withInput, withOutput, withDesc int
	for _, tool := range tools {
		if tool.Slug == "" {
			t.Error("tool with empty slug survived parsing")
		}
		if tool.Description != "" {
			withDesc++
		}
		if len(tool.InputParams) > 0 {
			withInput++
		}
		if len(tool.OutputParams) > 0 {
			withOutput++
		}
	}
	if withDesc == 0 {
		t.Error("no tool carried a description — the field was renamed")
	}
	if withInput == 0 {
		t.Error("no tool carried input parameters — input_parameters was renamed or is no longer a JSON Schema")
	}
	if withOutput == 0 {
		t.Error("no tool carried output parameters — output_parameters was renamed or is no longer a JSON Schema")
	}
}

// TestComposioContract_WritePaths checks that the endpoints behind the connect
// flow and tool execution still exist. Each is called with a body that cannot
// succeed, so nothing is created; a 404 means the path itself moved.
//
// 401/403 is a key-scope problem rather than contract drift, but it breaks the
// integration just as thoroughly, so it fails here too.
func TestComposioContract_WritePaths(t *testing.T) {
	c := liveClient(t)

	cases := []struct {
		name string
		path string
		body any
	}{
		{
			name: "auth config create",
			path: "/auth_configs",
			body: map[string]any{},
		},
		{
			name: "connected account link",
			path: "/connected_accounts/link",
			body: map[string]any{},
		},
		{
			// A real tool slug, but a user_id with no connected account, so the
			// request is rejected before anything runs.
			name: "tool execute",
			path: "/tools/execute/" + anyToolSlug(t, c),
			body: map[string]any{"arguments": map[string]any{}, "user_id": contractUserID},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body, _ := c.do(liveContext(t), http.MethodPost, tc.path, tc.body)
			switch {
			case status == http.StatusNotFound:
				t.Errorf("POST %s → 404: the endpoint moved. Body: %s", tc.path, truncate(string(body), 300))
			case status == http.StatusUnauthorized || status == http.StatusForbidden:
				t.Errorf("POST %s → %d: the API key lacks this permission area (see docs/connections.md)",
					tc.path, status)
			case status == 0:
				t.Errorf("POST %s: transport failure", tc.path)
			}
		})
	}
}

// TestComposioContract_ReadPaths checks the remaining GET endpoints Claworc
// calls that no other test in this file exercises.
func TestComposioContract_ReadPaths(t *testing.T) {
	c := liveClient(t)

	for _, path := range []string{"/connected_accounts?limit=1", "/tools?limit=1"} {
		status, body, err := c.do(liveContext(t), http.MethodGet, path, nil)
		if status < 200 || status >= 300 {
			t.Errorf("GET %s → %d (%v). Body: %s", path, status, err, truncate(string(body), 300))
		}
	}
}

// anyToolSlug returns a real tool slug from the contract toolkit,
// for probing the execute path. It fails the test rather than returning "" so a
// broken listing cannot silently turn the probe into a no-op.
func anyToolSlug(t *testing.T, c *ComposioClient) string {
	t.Helper()
	items, _ := rawTools(t, c, url.Values{"toolkit_slug": {contractToolkit}, "limit": {"1"}})
	if len(items) == 0 {
		t.Fatalf("cannot resolve a tool slug for %q", contractToolkit)
	}
	slug, _ := itemToolkitSlug(t, items[0])
	if slug == "" {
		t.Fatal("tool item has no slug")
	}
	return slug
}

// TestComposioContract_Summary prints the observed shape so a failing nightly
// run shows what Composio is returning now, not just which assertion tripped.
func TestComposioContract_Summary(t *testing.T) {
	c := liveClient(t)

	toolkits, err := c.ListOAuthToolkits(liveContext(t))
	if err != nil {
		t.Fatalf("ListOAuthToolkits: %v", err)
	}
	all, _ := rawTools(t, c, url.Values{"toolkit_slug": {contractToolkit}, "limit": {"100"}})
	imp, _ := rawTools(t, c, url.Values{"toolkit_slug": {contractToolkit}, "important": {"true"}, "limit": {"100"}})

	t.Log("Composio contract snapshot:")
	t.Log("  base URL:            " + ComposioBaseURL)
	t.Log("  managed toolkits:    " + strconv.Itoa(len(toolkits)))
	t.Log(fmt.Sprintf("  %s tools:          %d (important: %d)", contractToolkit, len(all), len(imp)))
}
