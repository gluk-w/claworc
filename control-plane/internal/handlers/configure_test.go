package handlers

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	"github.com/gluk-w/claworc/control-plane/internal/agentshim/openclawnative"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
)

// mockInstance records ExecOpenclaw calls and returns queued results.
type mockInstance struct {
	mu      sync.Mutex
	calls   [][]string
	results []callResult
}

type callResult struct {
	stdout, stderr string
	code           int
	err            error
}

func (m *mockInstance) ExecOpenclaw(_ context.Context, args ...string) (string, string, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, args)
	if len(m.results) == 0 {
		return "", "", 0, nil
	}
	r := m.results[0]
	if len(m.results) > 1 {
		m.results = m.results[1:]
	}
	return r.stdout, r.stderr, r.code, r.err
}

// mockOps implements orchestrator.ContainerOrchestrator for tests.
type mockOps struct{}

func (mockOps) Initialize(_ context.Context) error                                  { return nil }
func (mockOps) IsAvailable(_ context.Context) bool                                  { return true }
func (mockOps) BackendName() string                                                 { return "mock" }
func (mockOps) CreateInstance(_ context.Context, _ orchestrator.CreateParams) error { return nil }
func (mockOps) DeleteInstance(_ context.Context, _ string) error                    { return nil }
func (mockOps) StartInstance(_ context.Context, _ string) error                     { return nil }
func (mockOps) StopInstance(_ context.Context, _ string) error                      { return nil }
func (mockOps) RestartInstance(_ context.Context, _ string, _ orchestrator.CreateParams) error {
	return nil
}
func (mockOps) GetInstanceStatus(_ context.Context, _ string) (string, error)    { return "running", nil }
func (mockOps) GetInstanceImageInfo(_ context.Context, _ string) (string, error) { return "", nil }
func (mockOps) CloneVolumes(_ context.Context, _, _ string) error                { return nil }
func (mockOps) ConfigureSSHAccess(_ context.Context, _ uint, _ string) error     { return nil }
func (mockOps) GetSSHAddress(_ context.Context, _ uint) (string, int, error)     { return "", 0, nil }
func (mockOps) UpdateResources(_ context.Context, _ string, _ orchestrator.UpdateResourcesParams) error {
	return nil
}
func (mockOps) GetContainerStats(_ context.Context, _ string) (*orchestrator.ContainerStats, error) {
	return nil, nil
}
func (mockOps) UpdateImage(_ context.Context, _ string, _ orchestrator.CreateParams) error {
	return nil
}
func (mockOps) ExecInInstance(_ context.Context, _ string, _ []string) (string, string, int, error) {
	return "", "", 0, nil
}
func (mockOps) StreamExecInInstance(_ context.Context, _ string, _ []string, _ io.Writer) (string, int, error) {
	return "", 0, nil
}
func (mockOps) UpdatePlacementConfig(_ context.Context, _ string, _ orchestrator.UpdatePlacementParams) error {
	return nil
}
func (mockOps) DeleteSharedVolume(_ context.Context, _ uint) error         { return nil }
func (mockOps) CloneVolume(_ context.Context, _, _ string) error           { return nil }
func (mockOps) VolumeNameFor(name, suffix string) string                   { return name + "-" + suffix }
func (mockOps) Apply(_ context.Context, _ orchestrator.WorkloadSpec) error { return nil }
func (mockOps) DeleteWorkload(_ context.Context, _ orchestrator.WorkloadSpec) error {
	return nil
}
func (mockOps) EnsureSSHAccess(_ context.Context, _, _ string) error { return nil }
func (mockOps) WorkloadSSHAddress(_ context.Context, _ string) (string, int, error) {
	return "", 0, nil
}

// configureViaNative runs ConfigureInstance with the agent client seam pointed
// at the legacy native OpenClaw adapter over inst, so the tests below exercise
// routing construction end to end through the openclaw CLI calls it produces.
func configureViaNative(t *testing.T, inst *mockInstance, models []string, providers map[string]LLMProxyProvider, port int) {
	t.Helper()
	orig := agentClientFor
	agentClientFor = func(context.Context, uint) (agentshim.Client, error) {
		return openclawnative.NewWithExec(inst), nil
	}
	defer func() { agentClientFor = orig }()
	ConfigureInstance(context.Background(), mockOps{}, 1, "test", models, providers, port)
}

// fakeLLMClient records the routing handed to ConfigureLLM.
type fakeLLMClient struct {
	agentshim.Client
	routing *agentshim.LLMRouting
	err     error
}

func (f *fakeLLMClient) ConfigureLLM(_ context.Context, r agentshim.LLMRouting) error {
	f.routing = &r
	return f.err
}

// TestConfigureInstance_UsesFactoryClient: ConfigureInstance must hand the
// routing to whatever Client the factory resolves for the instance (the shim
// adapter for Hermes/NanoClaw/custom/shim OpenClaw), never a hardcoded
// OpenClaw adapter.
func TestConfigureInstance_UsesFactoryClient(t *testing.T) {
	fake := &fakeLLMClient{}
	var gotID uint
	orig := agentClientFor
	agentClientFor = func(_ context.Context, id uint) (agentshim.Client, error) {
		gotID = id
		return fake, nil
	}
	defer func() { agentClientFor = orig }()

	providers := map[string]LLMProxyProvider{
		"anthropic": {Key: "claworc-vk-1", APIType: "anthropic-messages",
			Models: []database.ProviderModel{{ID: "claude-sonnet-4-5"}}},
	}
	ConfigureInstance(context.Background(), mockOps{}, 42, "test",
		[]string{"anthropic/claude-sonnet-4-5"}, providers, 40001)

	if gotID != 42 {
		t.Errorf("factory resolved instance %d, want 42", gotID)
	}
	if fake.routing == nil {
		t.Fatal("ConfigureLLM not called")
	}
	if fake.routing.DefaultModel != "anthropic/claude-sonnet-4-5" {
		t.Errorf("default model = %q", fake.routing.DefaultModel)
	}
	if len(fake.routing.Providers) != 1 || fake.routing.Providers[0].APIKey != "claworc-vk-1" {
		t.Errorf("providers = %+v", fake.routing.Providers)
	}
}

// TestConfigureInstance_FactoryError: a factory failure is logged, not fatal.
func TestConfigureInstance_FactoryError(t *testing.T) {
	orig := agentClientFor
	agentClientFor = func(context.Context, uint) (agentshim.Client, error) {
		return nil, errors.New("no such instance")
	}
	defer func() { agentClientFor = orig }()
	ConfigureInstance(context.Background(), mockOps{}, 7, "test", []string{"m"}, nil, 0)
}

func TestConfigureInstance_NoOp(t *testing.T) {
	inst := &mockInstance{}
	// Empty models and providers → early return, no calls
	configureViaNative(t, inst, nil, nil, 0)
	if len(inst.calls) != 0 {
		t.Errorf("expected 0 calls, got %d", len(inst.calls))
	}
}

func TestConfigureInstance_ModelSet(t *testing.T) {
	inst := &mockInstance{}
	configureViaNative(t, inst,
		[]string{"claude-3-5-sonnet"}, nil, 0)

	if len(inst.calls) < 4 {
		t.Fatalf("expected at least 4 calls (model set + allowlist unset + allowlist set + gateway stop), got %d", len(inst.calls))
	}
	// First call: config set agents.defaults.model
	call0 := inst.calls[0]
	if call0[0] != "config" || call0[1] != "set" || call0[2] != "agents.defaults.model" {
		t.Errorf("unexpected first call: %v", call0)
	}
	// Second call: config unset agents.defaults.models (clear before re-setting)
	call1 := inst.calls[1]
	if call1[0] != "config" || call1[1] != "unset" || call1[2] != "agents.defaults.models" {
		t.Errorf("unexpected second call: %v", call1)
	}
	// Third call: config set agents.defaults.models (allowlist)
	call2 := inst.calls[2]
	if call2[0] != "config" || call2[1] != "set" || call2[2] != "agents.defaults.models" {
		t.Errorf("unexpected third call: %v", call2)
	}
	if !strings.Contains(call2[3], "claude-3-5-sonnet") {
		t.Errorf("models allowlist should contain claude-3-5-sonnet, got: %s", call2[3])
	}
	// Last call must be gateway stop
	last := inst.calls[len(inst.calls)-1]
	if last[0] != "gateway" || last[1] != "stop" {
		t.Errorf("expected last call to be gateway stop, got %v", last)
	}
}

func TestConfigureInstance_GatewayStop(t *testing.T) {
	inst := &mockInstance{}
	// Only providers → should set providers then stop gateway
	providers := map[string]LLMProxyProvider{
		"anthropic": {Key: "vk-test", APIType: "openai-completions"},
	}
	configureViaNative(t, inst,
		nil, providers, 40001)

	if len(inst.calls) < 1 {
		t.Fatalf("expected at least 1 call (gateway stop), got %d", len(inst.calls))
	}
	last := inst.calls[len(inst.calls)-1]
	if last[0] != "gateway" || last[1] != "stop" {
		t.Errorf("expected gateway stop, got %v", last)
	}
}

func TestConfigureInstance_ProvidersSet(t *testing.T) {
	inst := &mockInstance{}
	providers := map[string]LLMProxyProvider{
		"anthropic": {Key: "vk-test", APIType: "openai-completions"},
	}
	configureViaNative(t, inst,
		nil, providers, 40001)

	// Should have: providers unset + providers set + gateway stop
	if len(inst.calls) < 3 {
		t.Fatalf("expected at least 3 calls, got %d", len(inst.calls))
	}
	if c := inst.calls[0]; c[0] != "config" || c[1] != "unset" || c[2] != "models.providers" {
		t.Errorf("expected providers unset first, got %v", c)
	}
	if c := inst.calls[1]; c[0] != "config" || c[1] != "set" || c[2] != "models.providers" {
		t.Errorf("expected providers set second, got %v", c)
	}
}

func TestConfigureInstance_NilModelsEmptySlice(t *testing.T) {
	inst := &mockInstance{}
	// Nil models but with gateway providers → skip model set and allowlist, set providers, stop gateway
	providers := map[string]LLMProxyProvider{
		"openai": {Key: "vk-test2", APIType: "openai-completions"},
	}
	configureViaNative(t, inst,
		nil, providers, 40001)

	for _, call := range inst.calls {
		if call[0] == "config" && call[2] == "agents.defaults.model" {
			t.Errorf("model set should not be called when models is nil, got call: %v", call)
		}
		if call[0] == "config" && call[2] == "agents.defaults.models" {
			t.Errorf("models allowlist should not be called when models is nil, got call: %v", call)
		}
	}
}

func TestConfigureInstance_ModelSetFailure(t *testing.T) {
	inst := &mockInstance{
		results: []callResult{
			{err: errors.New("SSH error")},
		},
	}
	// Should log error and return without calling gateway stop
	configureViaNative(t, inst,
		[]string{"model-a"}, nil, 0)

	// Only one call was made (the failed one), gateway stop should not follow
	if len(inst.calls) != 1 {
		t.Errorf("expected 1 call (failed model set), got %d", len(inst.calls))
	}
}

func TestConfigureInstance_ModelSetNonZeroCode(t *testing.T) {
	inst := &mockInstance{
		results: []callResult{
			{code: 1, stderr: "unknown model"},
		},
	}
	providers := map[string]LLMProxyProvider{
		"anthropic": {Key: "vk-test", APIType: "openai-completions"},
	}
	configureViaNative(t, inst,
		[]string{"model-a"}, providers, 40001)

	hasProviders := false
	hasAllowlist := false
	for _, c := range inst.calls {
		if c[0] == "config" && c[1] == "set" && c[2] == "models.providers" {
			hasProviders = true
		}
		if c[0] == "config" && c[1] == "set" && c[2] == "agents.defaults.models" {
			hasAllowlist = true
		}
	}
	if !hasProviders {
		t.Errorf("providers must be set even when model config returns non-zero; calls: %v", inst.calls)
	}
	if !hasAllowlist {
		t.Errorf("models allowlist must be set even when model config returns non-zero; calls: %v", inst.calls)
	}
}

func TestConfigureInstance_CustomProviderAllModels(t *testing.T) {
	// Custom providers (non-empty gp.Models) pass all models through regardless of effective list.
	inst := &mockInstance{}
	providers := map[string]LLMProxyProvider{
		"anthropic": {
			Key:     "vk-test",
			APIType: "anthropic-messages",
			Models: []database.ProviderModel{
				{ID: "anthropic/claude-opus-4-6", Name: "Claude Opus 4.6"},
				{ID: "anthropic/claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
			},
		},
	}
	// Effective list only contains sonnet, but custom providers ignore this — both models should appear.
	configureViaNative(t, inst,
		[]string{"anthropic/anthropic/claude-sonnet-4-6"}, providers, 40001)

	var providersJSON string
	var allowlistJSON string
	for _, c := range inst.calls {
		if c[0] == "config" && c[1] == "set" && c[2] == "models.providers" {
			providersJSON = c[3]
		}
		if c[0] == "config" && c[1] == "set" && c[2] == "agents.defaults.models" {
			allowlistJSON = c[3]
		}
	}
	if providersJSON == "" {
		t.Fatal("models.providers call not found")
	}
	if !strings.Contains(providersJSON, "claude-opus-4-6") {
		t.Errorf("opus should be present (custom provider passes all models); got: %s", providersJSON)
	}
	if !strings.Contains(providersJSON, "claude-sonnet-4-6") {
		t.Errorf("sonnet should be present; got: %s", providersJSON)
	}
	// Models allowlist should only contain the effective model
	if allowlistJSON == "" {
		t.Fatal("agents.defaults.models call not found")
	}
	if !strings.Contains(allowlistJSON, "anthropic/anthropic/claude-sonnet-4-6") {
		t.Errorf("allowlist should contain the effective model; got: %s", allowlistJSON)
	}
}

func TestConfigureInstance_CatalogProviderModelsFiltered(t *testing.T) {
	// Catalog providers (empty gp.Models, CatalogKey set) use getCatalogModels + effectiveSet.
	orig := getCatalogModels
	getCatalogModels = func(catalogKey string) []database.ProviderModel {
		if catalogKey != "anthropic" {
			return nil
		}
		return []database.ProviderModel{
			{ID: "anthropic/claude-opus-4-6", Name: "Claude Opus 4.6"},
			{ID: "anthropic/claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
		}
	}
	defer func() { getCatalogModels = orig }()

	inst := &mockInstance{}
	providers := map[string]LLMProxyProvider{
		"anthropic": {Key: "vk-test", APIType: "anthropic-messages", CatalogKey: "anthropic"},
	}
	configureViaNative(t, inst,
		[]string{"anthropic/anthropic/claude-sonnet-4-6"}, providers, 40001)

	var providersJSON string
	var allowlistJSON string
	for _, c := range inst.calls {
		if c[0] == "config" && c[1] == "set" && c[2] == "models.providers" {
			providersJSON = c[3]
		}
		if c[0] == "config" && c[1] == "set" && c[2] == "agents.defaults.models" {
			allowlistJSON = c[3]
		}
	}
	if providersJSON == "" {
		t.Fatal("models.providers call not found")
	}
	if strings.Contains(providersJSON, "claude-opus-4-6") {
		t.Errorf("opus should be filtered out; got: %s", providersJSON)
	}
	if !strings.Contains(providersJSON, "claude-sonnet-4-6") {
		t.Errorf("sonnet should be present; got: %s", providersJSON)
	}
	// Models allowlist should match effective models
	if allowlistJSON == "" {
		t.Fatal("agents.defaults.models call not found")
	}
	if !strings.Contains(allowlistJSON, "anthropic/anthropic/claude-sonnet-4-6") {
		t.Errorf("allowlist should contain effective model; got: %s", allowlistJSON)
	}
}

func TestConfigureInstance_CatalogProviderWithCachedModelsFiltered(t *testing.T) {
	// Catalog provider with CatalogKey AND non-empty Models (cached) should still filter by effectiveSet.
	orig := getCatalogModels
	getCatalogModels = func(_ string) []database.ProviderModel {
		// Should not be called since Models is already populated.
		t.Error("getCatalogModels should not be called when Models is already cached")
		return nil
	}
	defer func() { getCatalogModels = orig }()

	inst := &mockInstance{}
	providers := map[string]LLMProxyProvider{
		"anthropic": {
			Key:        "vk-test",
			APIType:    "anthropic-messages",
			CatalogKey: "anthropic",
			Models: []database.ProviderModel{
				{ID: "anthropic/claude-opus-4-6", Name: "Claude Opus 4.6"},
				{ID: "anthropic/claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
			},
		},
	}
	// Effective list only contains sonnet.
	configureViaNative(t, inst,
		[]string{"anthropic/anthropic/claude-sonnet-4-6"}, providers, 40001)

	var providersJSON string
	var allowlistJSON string
	for _, c := range inst.calls {
		if c[0] == "config" && c[1] == "set" && c[2] == "models.providers" {
			providersJSON = c[3]
		}
		if c[0] == "config" && c[1] == "set" && c[2] == "agents.defaults.models" {
			allowlistJSON = c[3]
		}
	}
	if providersJSON == "" {
		t.Fatal("models.providers call not found")
	}
	if strings.Contains(providersJSON, "claude-opus-4-6") {
		t.Errorf("opus should be filtered out even with cached models; got: %s", providersJSON)
	}
	if !strings.Contains(providersJSON, "claude-sonnet-4-6") {
		t.Errorf("sonnet should be present; got: %s", providersJSON)
	}
	// Models allowlist should match effective models
	if allowlistJSON == "" {
		t.Fatal("agents.defaults.models call not found")
	}
	if !strings.Contains(allowlistJSON, "anthropic/anthropic/claude-sonnet-4-6") {
		t.Errorf("allowlist should contain effective model; got: %s", allowlistJSON)
	}
}

func TestConfigureInstance_CatalogProviderEmptyWhenNoneSelected(t *testing.T) {
	// Catalog provider with no models selected in effective list → models: []
	orig := getCatalogModels
	getCatalogModels = func(catalogKey string) []database.ProviderModel {
		return []database.ProviderModel{
			{ID: "anthropic/claude-opus-4-6", Name: "Claude Opus 4.6"},
		}
	}
	defer func() { getCatalogModels = orig }()

	inst := &mockInstance{}
	providers := map[string]LLMProxyProvider{
		"anthropic": {Key: "vk-test", APIType: "anthropic-messages", CatalogKey: "anthropic"},
	}
	configureViaNative(t, inst,
		nil, providers, 40001)

	var providersJSON string
	for _, c := range inst.calls {
		if c[0] == "config" && c[1] == "set" && c[2] == "models.providers" {
			providersJSON = c[3]
		}
		if c[0] == "config" && c[1] == "set" && c[2] == "agents.defaults.models" {
			t.Errorf("models allowlist should not be set when models is nil; got call: %v", c)
		}
	}
	if providersJSON == "" {
		t.Fatal("models.providers call not found")
	}
	if strings.Contains(providersJSON, "claude-opus-4-6") {
		t.Errorf("no models should appear when none are selected; got: %s", providersJSON)
	}
	if !strings.Contains(providersJSON, `"models":[]`) {
		t.Errorf("expected empty models array; got: %s", providersJSON)
	}
}
