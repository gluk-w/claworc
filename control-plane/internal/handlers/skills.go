package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Clawhub proxy (well-known discovery + search cache)
// ---------------------------------------------------------------------------

const clawhubWellKnownURL = "https://clawhub.ai/.well-known/clawhub.json"

type clawhubCacheEntry struct {
	body      []byte
	expiresAt time.Time
}

var (
	clawhubMu          sync.RWMutex
	clawhubAPIBase     string
	clawhubAPIBaseExp  time.Time
	clawhubSearchCache = map[string]*clawhubCacheEntry{}
	clawhubHTTPClient  = &http.Client{Timeout: 10 * time.Second}
)

func getClawhubAPIBase(ctx context.Context) (string, error) {
	clawhubMu.RLock()
	base := clawhubAPIBase
	exp := clawhubAPIBaseExp
	clawhubMu.RUnlock()

	if base != "" && time.Now().Before(exp) {
		return base, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clawhubWellKnownURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := clawhubHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch clawhub well-known: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var wk struct {
		APIBase string `json:"apiBase"`
	}
	if err := json.Unmarshal(body, &wk); err != nil {
		return "", fmt.Errorf("parse clawhub well-known: %w", err)
	}
	if wk.APIBase == "" {
		return "", fmt.Errorf("clawhub well-known: empty apiBase")
	}

	clawhubMu.Lock()
	clawhubAPIBase = wk.APIBase
	clawhubAPIBaseExp = time.Now().Add(time.Hour)
	clawhubMu.Unlock()
	return wk.APIBase, nil
}

// ClawhubSearch proxies search queries to the Clawhub public registry.
func ClawhubSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit := r.URL.Query().Get("limit")
	if limit == "" {
		limit = "20"
	}

	cacheKey := "search:" + q + ":" + limit

	clawhubMu.RLock()
	entry := clawhubSearchCache[cacheKey]
	clawhubMu.RUnlock()

	if entry != nil && time.Now().Before(entry.expiresAt) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(entry.body)
		return
	}

	apiBase, err := getClawhubAPIBase(r.Context())
	if err != nil {
		log.Printf("clawhub search: %v", err)
		http.Error(w, `{"error":"clawhub unavailable"}`, http.StatusBadGateway)
		return
	}

	url := apiBase + "/api/v1/search?q=" + q + "&limit=" + limit
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	resp, err := clawhubHTTPClient.Do(req)
	if err != nil {
		log.Printf("clawhub search fetch: %v", err)
		http.Error(w, `{"error":"clawhub unavailable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, `{"error":"read error"}`, http.StatusBadGateway)
		return
	}

	if resp.StatusCode == http.StatusOK {
		newEntry := &clawhubCacheEntry{body: body, expiresAt: time.Now().Add(60 * time.Second)}
		clawhubMu.Lock()
		clawhubSearchCache[cacheKey] = newEntry
		clawhubMu.Unlock()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// ---------------------------------------------------------------------------
// SKILL.md frontmatter parsing
// ---------------------------------------------------------------------------

type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func parseSkillFrontmatter(content []byte) (*skillFrontmatter, error) {
	s := string(content)
	if !strings.HasPrefix(s, "---") {
		return nil, fmt.Errorf("SKILL.md missing frontmatter opening ---")
	}
	rest := s[3:]
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return nil, fmt.Errorf("SKILL.md missing frontmatter closing ---")
	}
	yamlBlock := rest[:end]
	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return nil, fmt.Errorf("parse frontmatter YAML: %w", err)
	}
	if fm.Name == "" {
		return nil, fmt.Errorf("SKILL.md frontmatter missing name")
	}
	if fm.Description == "" {
		return nil, fmt.Errorf("SKILL.md frontmatter missing description")
	}
	return &fm, nil
}

// ---------------------------------------------------------------------------
// List skills
// ---------------------------------------------------------------------------

func ListSkills(w http.ResponseWriter, r *http.Request) {
	var skills []database.Skill
	if err := database.DB.Order("created_at desc").Find(&skills).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list skills")
		return
	}
	writeJSON(w, http.StatusOK, skills)
}

// ---------------------------------------------------------------------------
// Upload skill (zip)
// ---------------------------------------------------------------------------

func UploadSkill(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "File too large or invalid form")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Missing file field")
		return
	}
	defer file.Close()

	zipData, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to read file")
		return
	}

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid zip file")
		return
	}

	prefix := detectZipPrefix(zr.File)
	files := map[string][]byte{}
	var skillMDContent []byte

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		if prefix != "" {
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" {
			continue
		}
		if strings.Contains(name, "..") {
			writeError(w, http.StatusBadRequest, "Invalid path in zip: "+name)
			return
		}
		rc, err := f.Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, "Failed to read zip entry")
			return
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			writeError(w, http.StatusBadRequest, "Failed to read zip entry content")
			return
		}
		files[name] = data
		if name == "SKILL.md" {
			skillMDContent = data
		}
	}

	if skillMDContent == nil {
		writeError(w, http.StatusBadRequest, "Zip does not contain SKILL.md")
		return
	}

	fm, err := parseSkillFrontmatter(skillMDContent)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid SKILL.md: "+err.Error())
		return
	}

	slug := fm.Name

	overwrite := r.URL.Query().Get("overwrite") == "true"

	var existing database.Skill
	if err := database.DB.Where("slug = ?", slug).First(&existing).Error; err == nil {
		if !overwrite {
			writeError(w, http.StatusConflict, "Skill '"+slug+"' already exists")
			return
		}
		// Remove existing files and DB record before re-creating
		_ = os.RemoveAll(filepath.Join(config.Cfg.DataPath, "skills", slug))
		database.DB.Delete(&existing)
	}

	destDir := filepath.Join(config.Cfg.DataPath, "skills", slug)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create skill directory")
		return
	}

	for name, data := range files {
		destPath := filepath.Join(destDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to create directory")
			return
		}
		if err := os.WriteFile(destPath, data, 0644); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to write file")
			return
		}
	}

	skill := database.Skill{
		Slug:    slug,
		Name:    fm.Name,
		Summary: fm.Description,
	}
	if err := database.DB.Create(&skill).Error; err != nil {
		os.RemoveAll(destDir)
		writeError(w, http.StatusInternalServerError, "Failed to save skill")
		return
	}

	writeJSON(w, http.StatusCreated, skill)
}

// detectZipPrefix returns a common top-level directory prefix if all files share one.
func detectZipPrefix(files []*zip.File) string {
	for _, f := range files {
		if f.FileInfo().IsDir() {
			continue
		}
		parts := strings.SplitN(f.Name, "/", 2)
		if len(parts) != 2 {
			return ""
		}
		prefix := parts[0] + "/"
		for _, f2 := range files {
			if !f2.FileInfo().IsDir() && !strings.HasPrefix(f2.Name, prefix) {
				return ""
			}
		}
		return prefix
	}
	return ""
}

// ---------------------------------------------------------------------------
// Delete skill
// ---------------------------------------------------------------------------

func DeleteSkill(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	var skill database.Skill
	if err := database.DB.Where("slug = ?", slug).First(&skill).Error; err != nil {
		writeError(w, http.StatusNotFound, "Skill not found")
		return
	}

	// Sanitize slug to prevent path traversal
	cleanSlug := filepath.Base(slug)
	destDir := filepath.Join(config.Cfg.DataPath, "skills", cleanSlug)
	if err := os.RemoveAll(destDir); err != nil {
		log.Printf("delete skill dir: %v", err)
	}

	if err := database.DB.Delete(&skill).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete skill")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Deploy skill
// ---------------------------------------------------------------------------

type deploySkillRequest struct {
	InstanceIDs []uint `json:"instance_ids"`
	Source      string `json:"source"`
	Version     string `json:"version,omitempty"`
}

type deploySkillResult struct {
	InstanceID uint   `json:"instance_id"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

func DeploySkill(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	var req deploySkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(req.InstanceIDs) == 0 {
		writeError(w, http.StatusBadRequest, "No instance IDs specified")
		return
	}
	if req.Source == "" {
		req.Source = "library"
	}

	fileMap, err := buildSkillFileMap(r.Context(), slug, req.Source, req.Version)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to load skill: "+err.Error())
		return
	}

	results := make([]deploySkillResult, len(req.InstanceIDs))
	var wg sync.WaitGroup
	for i, instID := range req.InstanceIDs {
		wg.Add(1)
		go func(idx int, instanceID uint) {
			defer wg.Done()
			results[idx] = deployToInstance(instanceID, slug, fileMap)
		}(i, instID)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]interface{}{"results": results})
}

func buildSkillFileMap(ctx context.Context, slug, source, version string) (map[string][]byte, error) {
	if source == "library" {
		dir := filepath.Join(config.Cfg.DataPath, "skills", slug)
		fileMap := map[string][]byte{}
		err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			fileMap[filepath.ToSlash(rel)] = data
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("read skill files: %w", err)
		}
		return fileMap, nil
	}

	// clawhub source: download zip
	apiBase, err := getClawhubAPIBase(ctx)
	if err != nil {
		return nil, fmt.Errorf("clawhub unavailable: %w", err)
	}

	url := apiBase + "/api/v1/download?slug=" + slug
	if version != "" {
		url += "&version=" + version
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := clawhubHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch skill from clawhub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("clawhub download returned %d", resp.StatusCode)
	}

	zipData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("invalid zip from clawhub: %w", err)
	}

	prefix := detectZipPrefix(zr.File)
	fileMap := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		if prefix != "" {
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" || strings.Contains(name, "..") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		fileMap[name] = data
	}
	return fileMap, nil
}

func deployToInstance(instanceID uint, slug string, fileMap map[string][]byte) deploySkillResult {
	result := deploySkillResult{InstanceID: instanceID}

	client, ok := SSHMgr.GetConnection(instanceID)
	if !ok {
		result.Status = "error"
		result.Error = "SSH not connected"
		return result
	}

	// Use path (not filepath) for remote Unix paths
	remoteBase := "/home/claworc/.openclaw/skills/" + slug

	if err := sshproxy.CreateDirectory(client, remoteBase); err != nil {
		result.Status = "error"
		result.Error = "Failed to create skill directory: " + err.Error()
		return result
	}

	for name, data := range fileMap {
		remotePath := path.Join(remoteBase, name)
		parentDir := path.Dir(remotePath)
		if parentDir != remoteBase {
			if err := sshproxy.CreateDirectory(client, parentDir); err != nil {
				result.Status = "error"
				result.Error = "Failed to create directory " + parentDir + ": " + err.Error()
				return result
			}
		}
		if err := sshproxy.WriteFile(client, remotePath, data); err != nil {
			result.Status = "error"
			result.Error = "Failed to write " + name + ": " + err.Error()
			return result
		}
	}

	result.Status = "ok"
	return result
}
