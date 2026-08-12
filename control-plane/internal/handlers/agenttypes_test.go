package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/sshproxy"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupAgentTypesTestDB opens an in-memory DB with every table the create
// path touches (teams, providers, gateway keys) plus the seed settings the
// image-resolution logic reads.
func setupAgentTypesTestDB(t *testing.T) {
	t.Helper()
	dsn := fmt.Sprintf("file:agenttypes_%s_%p?mode=memory&cache=shared", t.Name(), t)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(
		&database.Instance{}, &database.Setting{}, &database.User{},
		&database.UserInstance{}, &database.Team{}, &database.TeamMember{},
		&database.LLMProvider{}, &database.LLMGatewayKey{},
	); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	// Note: database.DB is intentionally left set after the test — the async
	// provisioning goroutine spawned by CreateInstance reads the global and
	// would panic on nil during cleanup.
	database.DB = db

	database.SetSetting("default_agent_image", "claworc/openclaw:latest")
	database.SetSetting("default_agent_images",
		`{"hermes":"claworc/hermes:latest","nanoclaw":"claworc/nanoclaw:latest","custom":""}`)
	database.SetSetting("default_models", `[]`)

	if err := db.Create(&database.Team{Name: "Default Team"}).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}
}

func TestListAgentTypes(t *testing.T) {
	setupAgentTypesTestDB(t)

	w := httptest.NewRecorder()
	ListAgentTypes(w, httptest.NewRequest("GET", "/api/v1/agent-types", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var got []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d agent types, want 4", len(got))
	}
	byType := map[string]map[string]interface{}{}
	for _, e := range got {
		byType[e["type"].(string)] = e
	}
	if img := byType["openclaw"]["default_image"]; img != "claworc/openclaw:latest" {
		t.Errorf("openclaw default_image = %v", img)
	}
	if img := byType["hermes"]["default_image"]; img != "claworc/hermes:latest" {
		t.Errorf("hermes default_image = %v", img)
	}
	if hc := byType["openclaw"]["has_control_ui"]; hc != true {
		t.Errorf("openclaw has_control_ui = %v, want true", hc)
	}
	if hc := byType["hermes"]["has_control_ui"]; hc != false {
		t.Errorf("hermes has_control_ui = %v, want false", hc)
	}
}

func TestCreateInstance_RejectsUnknownAgentType(t *testing.T) {
	setupAgentTypesTestDB(t)

	body := bytes.NewBufferString(`{"display_name":"Bad Type","team_id":1,"agent_type":"skynet"}`)
	req := httptest.NewRequest("POST", "/api/v1/instances", body)
	w := httptest.NewRecorder()

	CreateInstance(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unknown agent type") {
		t.Errorf("body %q should mention unknown agent type", w.Body.String())
	}
}

func TestCreateInstance_StoresAgentTypeAndDefaultImage(t *testing.T) {
	setupAgentTypesTestDB(t)

	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)
	// A live (but unconnected) SSH manager so the async provisioning
	// goroutine blocks harmlessly instead of panicking on a nil manager.
	// Deliberately NOT reset to nil on exit (package convention — the
	// goroutine may still reference it).
	SSHMgr = sshproxy.NewSSHManager(nil, "")

	body := bytes.NewBufferString(`{"display_name":"Hermes One","team_id":1,"agent_type":"hermes"}`)
	req := httptest.NewRequest("POST", "/api/v1/instances", body)
	w := httptest.NewRecorder()

	CreateInstance(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["agent_type"] != "hermes" {
		t.Errorf("response agent_type = %v, want hermes", resp["agent_type"])
	}
	if resp["agent_display_name"] != "Hermes" {
		t.Errorf("response agent_display_name = %v, want Hermes", resp["agent_display_name"])
	}
	if resp["has_control_ui"] != false {
		t.Errorf("response has_control_ui = %v, want false", resp["has_control_ui"])
	}

	var inst database.Instance
	if err := database.DB.Where("name = ?", "bot-hermes-one").First(&inst).Error; err != nil {
		t.Fatalf("load created instance: %v", err)
	}
	if inst.AgentType != "hermes" {
		t.Errorf("stored AgentType = %q, want hermes", inst.AgentType)
	}
	if inst.ContainerImage != "claworc/hermes:latest" {
		t.Errorf("stored ContainerImage = %q, want the hermes default", inst.ContainerImage)
	}
}

func TestCreateInstance_DefaultsToOpenClaw(t *testing.T) {
	setupAgentTypesTestDB(t)

	mock := &mockOrchestrator{}
	orchestrator.Set(mock)
	defer orchestrator.Set(nil)
	SSHMgr = sshproxy.NewSSHManager(nil, "")

	body := bytes.NewBufferString(`{"display_name":"Plain","team_id":1}`)
	req := httptest.NewRequest("POST", "/api/v1/instances", body)
	w := httptest.NewRecorder()

	CreateInstance(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", w.Code, w.Body.String())
	}
	var inst database.Instance
	if err := database.DB.Where("name = ?", "bot-plain").First(&inst).Error; err != nil {
		t.Fatalf("load created instance: %v", err)
	}
	if inst.AgentType != agentshim.TypeOpenClaw {
		t.Errorf("stored AgentType = %q, want openclaw", inst.AgentType)
	}
	if inst.ContainerImage != "claworc/openclaw:latest" {
		t.Errorf("stored ContainerImage = %q, want the openclaw default", inst.ContainerImage)
	}
}

func TestApplyReservedAgentEnv_OpenClaw(t *testing.T) {
	setupAgentTypesTestDB(t)
	database.SetSetting("default_models", `["anthropic/claude-sonnet-4-5"]`)

	prevPort := config.Cfg.LLMGatewayPort
	config.Cfg.LLMGatewayPort = 40001
	defer func() { config.Cfg.LLMGatewayPort = prevPort }()

	inst := database.Instance{Name: "bot-env", DisplayName: "Env", AgentType: "openclaw"}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	envVars := map[string]string{}
	applyReservedAgentEnv(envVars, inst, "tok-123")

	if envVars["CLAWORC_INSTANCE_ID"] != fmt.Sprintf("%d", inst.ID) {
		t.Errorf("CLAWORC_INSTANCE_ID = %q", envVars["CLAWORC_INSTANCE_ID"])
	}
	if envVars["CLAWORC_AGENT_TOKEN"] != "tok-123" {
		t.Errorf("CLAWORC_AGENT_TOKEN = %q, want tok-123", envVars["CLAWORC_AGENT_TOKEN"])
	}
	if envVars["CLAWORC_LLM_PROXY_URL"] != "http://127.0.0.1:40001" {
		t.Errorf("CLAWORC_LLM_PROXY_URL = %q", envVars["CLAWORC_LLM_PROXY_URL"])
	}
	var routing agentshim.LLMRouting
	if err := json.Unmarshal([]byte(envVars["CLAWORC_INITIAL_LLM_CONFIG"]), &routing); err != nil {
		t.Fatalf("CLAWORC_INITIAL_LLM_CONFIG is not valid LLMRouting JSON: %v", err)
	}
	if routing.DefaultModel != "anthropic/claude-sonnet-4-5" {
		t.Errorf("routing default model = %q", routing.DefaultModel)
	}
	if routing.ProxyURL != "http://127.0.0.1:40001" {
		t.Errorf("routing proxy url = %q", routing.ProxyURL)
	}

	// OpenClaw keeps its legacy variables.
	if envVars["OPENCLAW_GATEWAY_TOKEN"] != "tok-123" {
		t.Errorf("OPENCLAW_GATEWAY_TOKEN = %q, want tok-123", envVars["OPENCLAW_GATEWAY_TOKEN"])
	}
	if envVars["OPENCLAW_INITIAL_MODELS"] == "" {
		t.Error("OPENCLAW_INITIAL_MODELS missing for openclaw type")
	}
}

func TestApplyReservedAgentEnv_NonOpenClawSkipsLegacyVars(t *testing.T) {
	setupAgentTypesTestDB(t)
	database.SetSetting("default_models", `["anthropic/claude-sonnet-4-5"]`)

	prevPort := config.Cfg.LLMGatewayPort
	config.Cfg.LLMGatewayPort = 40001
	defer func() { config.Cfg.LLMGatewayPort = prevPort }()

	inst := database.Instance{Name: "bot-hermes-env", DisplayName: "HermesEnv", AgentType: "hermes"}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	envVars := map[string]string{}
	applyReservedAgentEnv(envVars, inst, "tok-456")

	for _, want := range []string{"CLAWORC_INSTANCE_ID", "CLAWORC_AGENT_TOKEN", "CLAWORC_LLM_PROXY_URL", "CLAWORC_INITIAL_LLM_CONFIG"} {
		if envVars[want] == "" {
			t.Errorf("%s missing for hermes type", want)
		}
	}
	for _, banned := range []string{"OPENCLAW_GATEWAY_TOKEN", "OPENCLAW_INITIAL_MODELS", "OPENCLAW_INITIAL_PROVIDERS"} {
		if _, ok := envVars[banned]; ok {
			t.Errorf("%s must not be injected for non-openclaw types", banned)
		}
	}
}

func TestControlProxy_NoControlUIType(t *testing.T) {
	setupAgentTypesTestDB(t)

	inst := database.Instance{Name: "bot-no-ui", DisplayName: "No UI", Status: "running", AgentType: "hermes"}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}
	user := createTestUser(t, "admin")

	req := buildRequest(t, "GET", fmt.Sprintf("/openclaw/%d/", inst.ID), user,
		map[string]string{"id": fmt.Sprintf("%d", inst.ID), "*": ""})
	w := httptest.NewRecorder()

	ControlProxy(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "does not provide a web control UI") {
		t.Errorf("body %q should explain the missing control UI", w.Body.String())
	}
}

func TestReservedEnvVarNames_IncludeShimContractVars(t *testing.T) {
	t.Parallel()
	want := []string{
		"OPENCLAW_GATEWAY_TOKEN", "CLAWORC_INSTANCE_ID",
		"OPENCLAW_INITIAL_MODELS", "OPENCLAW_INITIAL_PROVIDERS",
		"CLAWORC_AGENT_TOKEN", "CLAWORC_INITIAL_LLM_CONFIG", "CLAWORC_LLM_PROXY_URL",
	}
	have := map[string]bool{}
	for _, n := range ReservedEnvVarNames {
		have[n] = true
	}
	for _, n := range want {
		if !have[n] {
			t.Errorf("ReservedEnvVarNames missing %s", n)
		}
	}
}
