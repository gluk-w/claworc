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

	"github.com/gluk-w/claworc/control-plane/internal/analytics"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/taskmanager"
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
	Name            string   `yaml:"name"`
	Description     string   `yaml:"description"`
	RequiredEnvVars []string `yaml:"required_env_vars,omitempty"`
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

type skillResponse struct {
	ID              uint     `json:"id"`
	Slug            string   `json:"slug"`
	Name            string   `json:"name"`
	Summary         string   `json:"summary"`
	RequiredEnvVars []string `json:"required_env_vars"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
}

func skillToResponse(s database.Skill) skillResponse {
	return skillResponse{
		ID:              s.ID,
		Slug:            s.Slug,
		Name:            s.Name,
		Summary:         s.Summary,
		RequiredEnvVars: parseRequiredEnvVars(s.RequiredEnvVars),
		CreatedAt:       s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt:       s.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func parseRequiredEnvVars(raw string) []string {
	if raw == "" || raw == "[]" {
		return []string{}
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil || names == nil {
		return []string{}
	}
	return names
}

func encodeRequiredEnvVars(names []string) string {
	if len(names) == 0 {
		return "[]"
	}
	b, err := json.Marshal(names)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func ListSkills(w http.ResponseWriter, r *http.Request) {
	var skills []database.Skill
	if err := database.DB.Order("created_at desc").Find(&skills).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list skills")
		return
	}
	resp := make([]skillResponse, len(skills))
	for i, s := range skills {
		resp[i] = skillToResponse(s)
	}
	writeJSON(w, http.StatusOK, resp)
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
		Slug:            slug,
		Name:            fm.Name,
		Summary:         fm.Description,
		RequiredEnvVars: encodeRequiredEnvVars(fm.RequiredEnvVars),
	}
	if err := database.DB.Create(&skill).Error; err != nil {
		os.RemoveAll(destDir)
		writeError(w, http.StatusInternalServerError, "Failed to save skill")
		return
	}

	var totalSkills int64
	database.DB.Model(&database.Skill{}).Count(&totalSkills)
	analytics.Track(r.Context(), analytics.EventSkillUploaded, map[string]any{
		"total_skills": totalSkills,
	})

	writeJSON(w, http.StatusCreated, skillToResponse(skill))
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
// Skill file editor (list / get / put)
// ---------------------------------------------------------------------------

type skillFileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Binary bool   `json:"binary"`
}

type skillFileContent struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Binary  bool   `json:"binary"`
}

type skillFilePutRequest struct {
	Content string `json:"content"`
}

// isBinaryContent returns true if the first 8KB of data contains a NUL byte.
func isBinaryContent(data []byte) bool {
	limit := len(data)
	if limit > 8192 {
		limit = 8192
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

// resolveSkillFilePath validates the user-supplied relative path and returns
// the absolute on-disk path. It rejects empty paths, "..", and any path that
// would escape the skill directory.
func resolveSkillFilePath(skillSlug, rel string) (string, string, error) {
	if rel == "" {
		return "", "", fmt.Errorf("path required")
	}
	if strings.Contains(rel, "..") {
		return "", "", fmt.Errorf("invalid path")
	}
	cleanRel := filepath.ToSlash(filepath.Clean(rel))
	if strings.HasPrefix(cleanRel, "/") || strings.HasPrefix(cleanRel, "..") {
		return "", "", fmt.Errorf("invalid path")
	}
	root := filepath.Join(config.Cfg.DataPath, "skills", skillSlug)
	abs := filepath.Join(root, filepath.FromSlash(cleanRel))
	relCheck, err := filepath.Rel(root, abs)
	if err != nil {
		return "", "", fmt.Errorf("invalid path")
	}
	if strings.HasPrefix(relCheck, "..") || relCheck == ".." {
		return "", "", fmt.Errorf("invalid path")
	}
	return abs, cleanRel, nil
}

// lookupSkill loads the skill record and returns 404 if not found.
func lookupSkill(w http.ResponseWriter, slug string) (*database.Skill, bool) {
	var skill database.Skill
	if err := database.DB.Where("slug = ?", slug).First(&skill).Error; err != nil {
		writeError(w, http.StatusNotFound, "Skill not found")
		return nil, false
	}
	return &skill, true
}

// ListSkillFiles returns the list of files inside a skill's on-disk directory.
func ListSkillFiles(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	skill, ok := lookupSkill(w, slug)
	if !ok {
		return
	}

	root := filepath.Join(config.Cfg.DataPath, "skills", skill.Slug)
	var entries []skillFileEntry
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		// Sniff first 8KB for binary detection.
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		buf := make([]byte, 8192)
		n, _ := f.Read(buf)
		f.Close()
		entries = append(entries, skillFileEntry{
			Path:   filepath.ToSlash(rel),
			Size:   info.Size(),
			Binary: isBinaryContent(buf[:n]),
		})
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list skill files")
		return
	}
	if entries == nil {
		entries = []skillFileEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}

// GetSkillFile returns the content of a single file inside a skill directory.
func GetSkillFile(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	skill, ok := lookupSkill(w, slug)
	if !ok {
		return
	}

	abs, relPath, err := resolveSkillFilePath(skill.Slug, chi.URLParam(r, "*"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "File not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to read file")
		return
	}

	resp := skillFileContent{Path: relPath, Binary: isBinaryContent(data)}
	if !resp.Binary {
		resp.Content = string(data)
	}
	writeJSON(w, http.StatusOK, resp)
}

// PutSkillFile writes new content to a file inside a skill directory. If the
// file is SKILL.md the frontmatter is re-parsed and the DB record updated.
func PutSkillFile(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	skill, ok := lookupSkill(w, slug)
	if !ok {
		return
	}

	abs, relPath, err := resolveSkillFilePath(skill.Slug, chi.URLParam(r, "*"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Refuse to overwrite an existing binary file via the text editor.
	if existing, err := os.ReadFile(abs); err == nil && isBinaryContent(existing) {
		writeError(w, http.StatusBadRequest, "Binary files cannot be edited as text")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req skillFilePutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	newContent := []byte(req.Content)

	// SKILL.md must remain valid — re-parse before writing.
	var newFrontmatter *skillFrontmatter
	if relPath == "SKILL.md" {
		fm, err := parseSkillFrontmatter(newContent)
		if err != nil {
			writeError(w, http.StatusBadRequest, "Invalid SKILL.md: "+err.Error())
			return
		}
		newFrontmatter = fm
	}

	// Atomic write: tmp file + rename.
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create directory")
		return
	}
	tmp := abs + ".tmp"
	if err := os.WriteFile(tmp, newContent, 0644); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to write file")
		return
	}
	if err := os.Rename(tmp, abs); err != nil {
		os.Remove(tmp)
		writeError(w, http.StatusInternalServerError, "Failed to commit file")
		return
	}

	if newFrontmatter != nil {
		skill.Name = newFrontmatter.Name
		skill.Summary = newFrontmatter.Description
		skill.RequiredEnvVars = encodeRequiredEnvVars(newFrontmatter.RequiredEnvVars)
		if err := database.DB.Save(skill).Error; err != nil {
			log.Printf("update skill record after SKILL.md edit: %v", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
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

	// Sanitize slug to prevent path traversal — use the DB record's slug
	// which was validated on creation, not the URL parameter directly
	destDir := filepath.Join(config.Cfg.DataPath, "skills", skill.Slug)
	if err := os.RemoveAll(destDir); err != nil {
		log.Printf("delete skill dir: %v", err)
	}

	if err := database.DB.Delete(&skill).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete skill")
		return
	}

	var remaining int64
	database.DB.Model(&database.Skill{}).Count(&remaining)
	analytics.Track(r.Context(), analytics.EventSkillDeleted, map[string]any{
		"remaining_skills": remaining,
	})

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
	InstanceID     uint     `json:"instance_id"`
	Status         string   `json:"status"`
	Error          string   `json:"error,omitempty"`
	MissingEnvVars []string `json:"missing_env_vars,omitempty"`
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

	// Per-instance authorization: caller must be admin or manager of every
	// targeted instance's team. The route itself is not admin-gated so that
	// team managers can deploy library skills to their own team's instances.
	for _, instID := range req.InstanceIDs {
		if !middleware.CanMutateInstance(r, instID) {
			writeError(w, http.StatusForbidden, fmt.Sprintf("Not authorized to deploy to instance %d", instID))
			return
		}
	}

	fileMap, err := buildSkillFileMap(r.Context(), slug, req.Source, req.Version)
	if err != nil {
		writeError(w, http.StatusBadRequest, "Failed to load skill: "+err.Error())
		return
	}

	// Determine the env var names this skill declares it needs. We parse the
	// frontmatter from the fileMap so the check works for both library and
	// clawhub sources without a DB lookup.
	var requiredEnvVars []string
	if skillMD, ok := fileMap["SKILL.md"]; ok {
		if fm, err := parseSkillFrontmatter(skillMD); err == nil {
			requiredEnvVars = fm.RequiredEnvVars
		}
	}

	// Globally-defined env var names (shared across all instances) — only the
	// names are needed, not the values, so we skip decryption.
	globalEnvNames := map[string]struct{}{}
	for _, k := range LoadGlobalEnvVarKeys() {
		globalEnvNames[k] = struct{}{}
	}

	// Async: register one task per target instance and return 202 with the
	// task IDs immediately. Per-instance results arrive over the SSE stream
	// driven by the TaskManager; the frontend renders them as toasts and
	// updates its skills page when the matching tasks end.
	taskIDs := make([]string, 0, len(req.InstanceIDs))
	if TaskMgr == nil {
		// Fallback for tests without a wired TaskMgr: keep synchronous
		// behaviour so unit tests don't need a manager.
		results := make([]deploySkillResult, len(req.InstanceIDs))
		var wg sync.WaitGroup
		for i, instID := range req.InstanceIDs {
			wg.Add(1)
			go func(idx int, instanceID uint) {
				defer wg.Done()
				result := deployToInstance(instanceID, slug, fileMap)
				result.MissingEnvVars = computeMissingEnvVars(instanceID, requiredEnvVars, globalEnvNames)
				results[idx] = result
			}(i, instID)
		}
		wg.Wait()
		writeJSON(w, http.StatusOK, map[string]interface{}{"results": results})
		return
	}
	for _, instID := range req.InstanceIDs {
		instanceID := instID
		var displayName, instanceLabel string
		var inst database.Instance
		if err := database.DB.Select("display_name").First(&inst, instanceID).Error; err == nil {
			displayName = fmt.Sprintf("%s — %s", inst.DisplayName, slug)
			instanceLabel = inst.DisplayName
		} else {
			displayName = fmt.Sprintf("instance %d — %s", instanceID, slug)
			instanceLabel = fmt.Sprintf("instance %d", instanceID)
		}
		taskID := TaskMgr.Start(taskmanager.StartOpts{
			Type:         taskmanager.TaskSkillDeploy,
			InstanceID:   instanceID,
			UserID:       callerID(r),
			ResourceID:   slug,
			ResourceName: displayName,
			Title:        fmt.Sprintf("Deploying %s to %s", slug, instanceLabel),
			Run: func(ctx context.Context, h *taskmanager.Handle) error {
				h.UpdateMessage("uploading skill files")
				result := deployToInstance(instanceID, slug, fileMap)
				if result.Status != "ok" {
					if result.Error != "" {
						return fmt.Errorf("%s", result.Error)
					}
					return fmt.Errorf("deploy failed")
				}
				return nil
			},
		})
		taskIDs = append(taskIDs, taskID)
	}
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"task_ids": taskIDs})
}

// computeMissingEnvVars returns the subset of requiredEnvVars that is neither
// defined globally nor per-instance. Missing env vars are a warning, not a
// failure — the deploy still proceeds.
func computeMissingEnvVars(instanceID uint, requiredEnvVars []string, globalEnvNames map[string]struct{}) []string {
	if len(requiredEnvVars) == 0 {
		return nil
	}
	instNames := map[string]struct{}{}
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err == nil {
		for k := range decodeEncryptedEnvVarsJSON(inst.EnvVars) {
			instNames[k] = struct{}{}
		}
	}
	var missing []string
	for _, name := range requiredEnvVars {
		if _, ok := globalEnvNames[name]; ok {
			continue
		}
		if _, ok := instNames[name]; ok {
			continue
		}
		missing = append(missing, name)
	}
	return missing
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
