// Package modwiring contains adapters that satisfy the moderator package's
// ports using concrete claworc internals (database, sshproxy, utils, etc.).
// It exists so the moderator package itself can stay free of claworc imports.
package modwiring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/moderator"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"github.com/gluk-w/claworc/control-plane/internal/utils"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---- Gateway dialer ----------------------------------------------------

// GatewayDialer wires moderator.GatewayDialer to sshproxy.DialGateway. It
// looks up the active gateway tunnel for the instance and decrypts the
// gateway token before dialing.
type GatewayDialer struct {
	DB      *gorm.DB
	Tunnels *sshproxy.TunnelManager
}

func (g *GatewayDialer) Dial(ctx context.Context, instanceID uint, _ string) (moderator.GatewayConn, error) {
	var inst database.Instance
	if err := g.DB.First(&inst, instanceID).Error; err != nil {
		return nil, fmt.Errorf("load instance: %w", err)
	}
	tunnels := g.Tunnels.GetTunnelsForInstance(instanceID)
	port := 0
	for _, t := range tunnels {
		if t.Label == "Gateway" && t.Status == "active" {
			port = t.LocalPort
			break
		}
	}
	if port == 0 {
		return nil, fmt.Errorf("no active gateway tunnel for instance %d", instanceID)
	}
	var token string
	if inst.GatewayToken != "" {
		if tok, err := utils.Decrypt(inst.GatewayToken); err == nil {
			token = tok
		}
	}
	conn, err := sshproxy.DialGateway(ctx, port, token)
	if err != nil {
		return nil, err
	}
	return &wsConn{c: conn}, nil
}

type wsConn struct{ c *websocket.Conn }

func (w *wsConn) Send(ctx context.Context, frame []byte) error {
	return w.c.Write(ctx, websocket.MessageText, frame)
}
func (w *wsConn) Recv(ctx context.Context) ([]byte, error) {
	_, data, err := w.c.Read(ctx)
	return data, err
}
func (w *wsConn) Close() error { return w.c.CloseNow() }

// ---- Workspace FS ------------------------------------------------------

// WorkspaceFS wires moderator.WorkspaceFS to sshproxy.ListDirectory /
// ReadFile, ensuring the SSH connection exists first.
type WorkspaceFS struct {
	DB  *gorm.DB
	SSH *sshproxy.SSHManager
}

func (f *WorkspaceFS) client(ctx context.Context, instanceID uint) (*ssh.Client, error) {
	var inst database.Instance
	if err := f.DB.First(&inst, instanceID).Error; err != nil {
		return nil, err
	}
	orch := orchestrator.Get()
	return f.SSH.EnsureConnectedWithIPCheck(ctx, inst.ID, orch, inst.AllowedSourceIPs)
}

func (f *WorkspaceFS) List(ctx context.Context, instanceID uint, dir string) ([]moderator.FileEntry, error) {
	c, err := f.client(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	entries, err := sshproxy.ListDirectory(c, dir)
	if err != nil {
		return nil, err
	}
	out := make([]moderator.FileEntry, 0, len(entries))
	for _, e := range entries {
		size := int64(0)
		if e.Size != nil {
			fmt.Sscanf(*e.Size, "%d", &size)
		}
		out = append(out, moderator.FileEntry{
			Path:  filepath.Join(dir, e.Name),
			Size:  size,
			IsDir: e.Type == "directory",
		})
	}
	return out, nil
}

func (f *WorkspaceFS) Read(ctx context.Context, instanceID uint, path string) ([]byte, error) {
	c, err := f.client(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	return sshproxy.ReadFile(c, path)
}

func (f *WorkspaceFS) Write(ctx context.Context, instanceID uint, path string, data []byte) error {
	c, err := f.client(ctx, instanceID)
	if err != nil {
		return err
	}
	if err := sshproxy.EnsureParentDir(c, path); err != nil {
		return err
	}
	return sshproxy.WriteFile(c, path, data)
}

func (f *WorkspaceFS) MkdirAll(ctx context.Context, instanceID uint, dir string) error {
	c, err := f.client(ctx, instanceID)
	if err != nil {
		return err
	}
	return sshproxy.CreateDirectory(c, dir)
}

func (f *WorkspaceFS) RemoveAll(ctx context.Context, instanceID uint, path string) error {
	c, err := f.client(ctx, instanceID)
	if err != nil {
		return err
	}
	return sshproxy.DeletePath(c, path)
}

// ---- LLM client --------------------------------------------------------

// LLMClient wires moderator.LLMClient to a direct OpenAI-compatible chat
// completion call against the provider's configured BaseURL + decrypted
// APIKey. We bypass the gateway here because the moderator already runs
// inside the control-plane process.
type LLMClient struct {
	DB         *gorm.DB
	HTTPClient *http.Client
}

func (l *LLMClient) Complete(ctx context.Context, providerKey, model, prompt string) (string, error) {
	var prov database.LLMProvider
	if err := l.DB.Where("key = ? AND instance_id IS NULL", providerKey).First(&prov).Error; err != nil {
		return "", fmt.Errorf("load provider %q: %w", providerKey, err)
	}
	apiKey := ""
	if prov.APIKey != "" {
		if dec, err := utils.Decrypt(prov.APIKey); err == nil {
			apiKey = dec
		}
	}
	base := strings.TrimRight(prov.BaseURL, "/")
	apiType := prov.APIType
	if apiType == "" {
		apiType = "openai-completions"
	}

	var url string
	var bodyBytes []byte
	headers := http.Header{"Content-Type": []string{"application/json"}}

	switch apiType {
	case "anthropic-messages":
		url = base + "/v1/messages"
		bodyBytes, _ = json.Marshal(map[string]any{
			"model":      model,
			"max_tokens": 4096,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		})
		if apiKey != "" {
			headers.Set("x-api-key", apiKey)
		}
		headers.Set("anthropic-version", "2023-06-01")
	default: // openai-compatible (openai-completions, openai-responses, ollama, …)
		// Some providers configure BaseURL with /v1 already; others without.
		// Probe the suffix and build accordingly.
		if strings.HasSuffix(base, "/v1") || strings.Contains(base, "/v1/") {
			url = base + "/chat/completions"
		} else {
			url = base + "/v1/chat/completions"
		}
		bodyBytes, _ = json.Marshal(map[string]any{
			"model": model,
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		})
		if apiKey != "" {
			headers.Set("Authorization", "Bearer "+apiKey)
		}
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header = headers
	hc := l.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("llm http %d: %s", resp.StatusCode, string(rawBody))
	}
	if apiType == "anthropic-messages" {
		var ar struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(rawBody, &ar); err != nil {
			return "", err
		}
		var sb strings.Builder
		for _, c := range ar.Content {
			if c.Type == "text" {
				sb.WriteString(c.Text)
			}
		}
		return sb.String(), nil
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rawBody, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llm empty response")
	}
	return parsed.Choices[0].Message.Content, nil
}

// ---- Store -------------------------------------------------------------

// Store wires moderator.Store to GORM models in the database package.
type Store struct{ DB *gorm.DB }

func (s *Store) GetTask(ctx context.Context, id uint) (moderator.Task, error) {
	var t database.KanbanTask
	if err := s.DB.WithContext(ctx).First(&t, id).Error; err != nil {
		return moderator.Task{}, err
	}
	return moderator.Task{
		ID: t.ID, BoardID: t.BoardID, Title: t.Title, Description: t.Description,
		Status: t.Status, AssignedInstanceID: t.AssignedInstanceID,
		OpenClawSessionID: t.OpenClawSessionID, OpenClawRunID: t.OpenClawRunID,
		EvaluatorProviderKey: t.EvaluatorProviderKey, EvaluatorModel: t.EvaluatorModel,
	}, nil
}

func (s *Store) UpdateTask(ctx context.Context, id uint, fields map[string]any) error {
	return s.DB.WithContext(ctx).Model(&database.KanbanTask{}).Where("id = ?", id).Updates(fields).Error
}

func (s *Store) GetBoard(ctx context.Context, id uint) (moderator.Board, error) {
	var b database.KanbanBoard
	if err := s.DB.WithContext(ctx).First(&b, id).Error; err != nil {
		return moderator.Board{}, err
	}
	var ids []uint
	_ = json.Unmarshal([]byte(b.EligibleInstances), &ids)
	return moderator.Board{
		ID: b.ID, Name: b.Name, Description: b.Description, EligibleInstances: ids,
	}, nil
}

func (s *Store) InsertComment(ctx context.Context, c moderator.Comment) (uint, error) {
	row := database.KanbanComment{
		TaskID: c.TaskID, Kind: c.Kind, Author: c.Author, Body: c.Body,
		OpenClawSessionID: c.OpenClawSessionID,
	}
	if err := s.DB.WithContext(ctx).Create(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

func (s *Store) SetCommentBody(ctx context.Context, id uint, body string) error {
	return s.DB.WithContext(ctx).Model(&database.KanbanComment{}).Where("id = ?", id).Updates(map[string]any{
		"body":       body,
		"updated_at": time.Now().UTC(),
	}).Error
}

func (s *Store) ListComments(ctx context.Context, taskID uint) ([]moderator.Comment, error) {
	var rows []database.KanbanComment
	if err := s.DB.WithContext(ctx).Where("task_id = ?", taskID).Order("created_at ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]moderator.Comment, 0, len(rows))
	for _, r := range rows {
		out = append(out, moderator.Comment{
			ID: r.ID, TaskID: r.TaskID, Kind: r.Kind, Author: r.Author,
			Body: r.Body, OpenClawSessionID: r.OpenClawSessionID,
		})
	}
	return out, nil
}

func (s *Store) InsertArtifact(ctx context.Context, a moderator.Artifact) error {
	return s.DB.WithContext(ctx).Create(&database.KanbanArtifact{
		TaskID: a.TaskID, Path: a.Path, SizeBytes: a.SizeBytes,
		SHA256: a.SHA256, StoragePath: a.StoragePath,
	}).Error
}

func (s *Store) ListTaskArtifacts(ctx context.Context, taskID uint) ([]moderator.Artifact, error) {
	var rows []database.KanbanArtifact
	if err := s.DB.WithContext(ctx).Where("task_id = ?", taskID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]moderator.Artifact, 0, len(rows))
	for _, r := range rows {
		out = append(out, moderator.Artifact{
			ID: r.ID, TaskID: r.TaskID, Path: r.Path,
			SizeBytes: r.SizeBytes, SHA256: r.SHA256, StoragePath: r.StoragePath,
		})
	}
	return out, nil
}

func (s *Store) GetSouls(ctx context.Context, instanceIDs []uint) ([]moderator.Soul, error) {
	var rows []database.InstanceSoul
	if err := s.DB.WithContext(ctx).Where("instance_id IN ?", instanceIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]moderator.Soul, 0, len(rows))
	for _, r := range rows {
		var skills []string
		_ = json.Unmarshal([]byte(r.Skills), &skills)
		out = append(out, moderator.Soul{
			InstanceID: r.InstanceID, Summary: r.Summary, Skills: skills, UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Store) UpsertSoul(ctx context.Context, soul moderator.Soul) error {
	skillsJSON, _ := json.Marshal(soul.Skills)
	row := database.InstanceSoul{
		InstanceID: soul.InstanceID,
		Summary:    soul.Summary,
		Skills:     string(skillsJSON),
		UpdatedAt:  time.Now().UTC(),
	}
	// Driver-portable upsert. GORM emits ON CONFLICT for SQLite/Postgres and
	// ON DUPLICATE KEY UPDATE for MySQL.
	return s.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "instance_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"summary", "skills", "updated_at"}),
	}).Create(&row).Error
}

// ---- Settings ----------------------------------------------------------

// Settings reads moderator tunables from the settings table.
type Settings struct {
	DB         *gorm.DB
	DefaultDir string // fallback artifact storage dir
}

func (s *Settings) get(key, def string) string {
	if v, err := database.GetSetting(key); err == nil && v != "" {
		return v
	}
	return def
}

func (s *Settings) ModeratorProvider() (string, string) {
	return s.get("kanban_moderator_provider_key", ""), s.get("kanban_moderator_model", "")
}
func (s *Settings) SummaryInterval() time.Duration {
	v := s.get("kanban_summary_interval", "10m")
	d, err := time.ParseDuration(v)
	if err != nil {
		return 10 * time.Minute
	}
	return d
}
func (s *Settings) ArtifactMaxBytes() int64 {
	v := s.get("kanban_artifacts_max_bytes", "5242880")
	var n int64
	fmt.Sscanf(v, "%d", &n)
	if n <= 0 {
		return 5 * 1024 * 1024
	}
	return n
}
func (s *Settings) ArtifactStorageDir() string {
	return s.get("kanban_artifacts_dir", s.DefaultDir)
}
func (s *Settings) WorkspaceDir() string {
	return s.get("kanban_workspace_dir", "/home/claworc/.openclaw/workspace")
}
func (s *Settings) TaskOutcomeDir() string {
	return s.get("kanban_task_outcome_dir", "/home/claworc/tasks")
}

// ---- Instance lister ---------------------------------------------------

type InstanceLister struct{ DB *gorm.DB }

func (l *InstanceLister) ListInstanceIDs(ctx context.Context) ([]uint, error) {
	var ids []uint
	if err := l.DB.WithContext(ctx).Model(&database.Instance{}).Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	return ids, nil
}

func (l *InstanceLister) InstanceName(ctx context.Context, id uint) (string, error) {
	var inst database.Instance
	if err := l.DB.WithContext(ctx).Select("display_name", "name").First(&inst, id).Error; err != nil {
		return "", err
	}
	if inst.DisplayName != "" {
		return inst.DisplayName, nil
	}
	return inst.Name, nil
}
