//go:build docker_integration

package handlers_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// buildSkillZip returns a zip archive containing the supplied files.
func buildSkillZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip.Create %q: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// uploadSkillZip POSTs a zip to /api/v1/skills?overwrite=true and returns the
// created skill's slug.
func uploadSkillZip(t *testing.T, zipBytes []byte) string {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "skill.zip")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(zipBytes); err != nil {
		t.Fatalf("write part: %v", err)
	}
	mw.Close()

	req, _ := http.NewRequest(http.MethodPost, sessionURL+"/api/v1/skills?overwrite=true", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload skill: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload skill: expected 201, got %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if out.Slug == "" {
		t.Fatal("upload returned empty slug")
	}
	return out.Slug
}

func putSkillFile(t *testing.T, slug, path, content string) (int, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"content": content})
	req, _ := http.NewRequest(http.MethodPut, fmt.Sprintf("%s/api/v1/skills/%s/files/%s", sessionURL, slug, path), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func getSkillFile(t *testing.T, slug, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/skills/%s/files/%s", sessionURL, slug, path))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// TestIntegration_SkillEditor exercises the list/get/put skill file endpoints.
func TestIntegration_SkillEditor(t *testing.T) {
	uniqueName := fmt.Sprintf("editor-test-%d", time.Now().UnixNano())
	skillMD := []byte("---\nname: " + uniqueName + "\ndescription: Original description\n---\nbody\n")
	notes := []byte("hello world\n")
	binary := append([]byte{0x89, 'P', 'N', 'G', 0, 0x1a, 0x0a}, bytes.Repeat([]byte{0x00, 0xff}, 100)...)

	zipBytes := buildSkillZip(t, map[string][]byte{
		"SKILL.md": skillMD,
		"notes.md": notes,
		"icon.png": binary,
	})
	slug := uploadSkillZip(t, zipBytes)
	t.Logf("uploaded skill slug=%s", slug)

	defer func() {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/skills/%s", sessionURL, slug), nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()

	// Step 1: list files
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/skills/%s/files", sessionURL, slug))
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("list files: %d: %s", resp.StatusCode, raw)
	}
	var entries []struct {
		Path   string `json:"path"`
		Size   int64  `json:"size"`
		Binary bool   `json:"binary"`
	}
	json.NewDecoder(resp.Body).Decode(&entries)
	resp.Body.Close()

	got := map[string]bool{}
	for _, e := range entries {
		got[e.Path] = e.Binary
	}
	for _, want := range []string{"SKILL.md", "notes.md", "icon.png"} {
		if _, ok := got[want]; !ok {
			t.Errorf("file list missing %q (got %v)", want, got)
		}
	}
	if !got["icon.png"] {
		t.Errorf("icon.png should have binary=true")
	}
	if got["SKILL.md"] || got["notes.md"] {
		t.Errorf("text files should have binary=false")
	}

	// Step 2: read notes.md
	code, raw := getSkillFile(t, slug, "notes.md")
	if code != 200 {
		t.Fatalf("GET notes.md: %d: %s", code, raw)
	}
	var notesResp struct {
		Content string `json:"content"`
		Binary  bool   `json:"binary"`
	}
	json.Unmarshal(raw, &notesResp)
	if notesResp.Binary {
		t.Error("notes.md returned binary=true")
	}
	if notesResp.Content != string(notes) {
		t.Errorf("notes.md content = %q, want %q", notesResp.Content, string(notes))
	}

	// Step 3: read icon.png — binary, empty content
	code, raw = getSkillFile(t, slug, "icon.png")
	if code != 200 {
		t.Fatalf("GET icon.png: %d: %s", code, raw)
	}
	var iconResp struct {
		Content string `json:"content"`
		Binary  bool   `json:"binary"`
	}
	json.Unmarshal(raw, &iconResp)
	if !iconResp.Binary {
		t.Error("icon.png returned binary=false")
	}
	if iconResp.Content != "" {
		t.Error("icon.png content should be empty when binary")
	}

	// Step 4: path traversal — multiple shapes (servers may normalise URLs).
	for _, badPath := range []string{"..%2F..%2Fetc%2Fpasswd"} {
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/skills/%s/files/%s", sessionURL, slug, badPath))
		if err != nil {
			t.Fatalf("traversal probe: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Errorf("traversal %q: status %d, want 400 or 404", badPath, resp.StatusCode)
		}
	}

	// Step 5: update notes.md
	code, msg := putSkillFile(t, slug, "notes.md", "updated content")
	if code != http.StatusNoContent {
		t.Fatalf("PUT notes.md: %d: %s", code, msg)
	}
	code, raw = getSkillFile(t, slug, "notes.md")
	json.Unmarshal(raw, &notesResp)
	if notesResp.Content != "updated content" {
		t.Errorf("after PUT, notes.md = %q, want %q", notesResp.Content, "updated content")
	}

	// Step 6: PUT to a binary file is rejected
	code, msg = putSkillFile(t, slug, "icon.png", "should fail")
	if code != http.StatusBadRequest {
		t.Errorf("PUT icon.png: status %d, want 400 (msg=%s)", code, msg)
	}

	// Step 7: SKILL.md frontmatter update propagates to DB summary
	newSkillMD := "---\nname: " + uniqueName + "\ndescription: Updated description\n---\nbody\n"
	code, msg = putSkillFile(t, slug, "SKILL.md", newSkillMD)
	if code != http.StatusNoContent {
		t.Fatalf("PUT SKILL.md (valid): %d: %s", code, msg)
	}
	resp, err = http.Get(sessionURL + "/api/v1/skills")
	if err != nil {
		t.Fatalf("GET /skills: %v", err)
	}
	var skills []struct {
		Slug    string `json:"slug"`
		Summary string `json:"summary"`
	}
	json.NewDecoder(resp.Body).Decode(&skills)
	resp.Body.Close()
	foundSummary := ""
	for _, s := range skills {
		if s.Slug == slug {
			foundSummary = s.Summary
			break
		}
	}
	if foundSummary != "Updated description" {
		t.Errorf("after SKILL.md edit, summary = %q, want %q", foundSummary, "Updated description")
	}

	// Step 8: invalid SKILL.md frontmatter rejected, on-disk content unchanged
	code, msg = putSkillFile(t, slug, "SKILL.md", "no frontmatter here")
	if code != http.StatusBadRequest {
		t.Errorf("PUT SKILL.md (invalid): status %d, want 400 (msg=%s)", code, msg)
	}
	code, raw = getSkillFile(t, slug, "SKILL.md")
	var skillMDResp struct {
		Content string `json:"content"`
	}
	json.Unmarshal(raw, &skillMDResp)
	if !strings.Contains(skillMDResp.Content, "Updated description") {
		t.Errorf("SKILL.md should be unchanged after rejected PUT, got %q", skillMDResp.Content)
	}
}
