package browserprov

import (
	"context"
	"fmt"
	"strings"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"github.com/gluk-w/claworc/control-plane/internal/taskmanager"
)

// StopperAdapter exposes a Provider's StopSession method as the
// BrowserSessionStopper interface used by the handlers package. It keeps the
// handlers free of a direct browserprov import for that one method.
type StopperAdapter struct{ Provider Provider }

func (s StopperAdapter) StopSession(ctx context.Context, instanceID uint) error {
	return s.Provider.StopSession(ctx, instanceID)
}

// AdminAdapter exposes the LocalProvider operations the handlers reach for
// outside the bridge's session lifecycle (delete on instance teardown, clone
// on instance clone). Kept as a thin adapter so handlers stay decoupled from
// the concrete provider implementation.
type AdminAdapter struct{ Provider *LocalProvider }

func (a AdminAdapter) DeleteBrowserPod(ctx context.Context, instanceID uint) error {
	return a.Provider.DeleteSession(ctx, instanceID)
}

func (a AdminAdapter) CloneBrowserVolume(ctx context.Context, srcInstanceName, dstInstanceName string) error {
	return a.Provider.CloneSession(ctx, srcInstanceName, dstInstanceName)
}

// Migrator coordinates the legacy → on-demand migration for an instance.
// The migration:
//  1. Validates the instance is legacy and resolves the new agent + browser images
//     from admin defaults.
//  2. Updates Instance.ContainerImage to the new slim agent image and seeds
//     Instance.BrowserImage / BrowserProvider so the orchestrator and tunnel
//     reconciler pick up the new layout on the next pass.
//  3. Calls orchestrator.UpdateImage to roll out the new agent Deployment.
//  4. Creates a BrowserSession row in "stopped" state so the desktop tab and
//     CDP listener wake the bridge on first use.
//
// PVC migration of chrome-data is not yet implemented inline — it will be
// addressed by a follow-up task that runs a one-shot copy job in K8s / a
// volume-clone container in Docker. For now legacy chrome-data is left in
// place on the agent home volume; the new browser PVC starts empty and
// existing cookies will not transfer until the copy job lands.
type Migrator struct {
	tasks  *taskmanager.Manager
	orch   orchestrator.ContainerOrchestrator
	bridge *BrowserBridge
}

// NewMigrator constructs a Migrator wired against the active orchestrator and
// browser bridge. Either argument can be nil to disable migrations entirely.
func NewMigrator(tasks *taskmanager.Manager, orch orchestrator.ContainerOrchestrator, bridge *BrowserBridge) *Migrator {
	return &Migrator{tasks: tasks, orch: orch, bridge: bridge}
}

// Migrate kicks off a TaskBrowserMigrate task and returns its ID immediately.
// Progress is exposed via the standard TaskManager SSE feed.
func (m *Migrator) Migrate(ctx context.Context, instanceID, userID uint) (string, error) {
	if m == nil || m.tasks == nil || m.orch == nil {
		return "", fmt.Errorf("browser migration not configured")
	}
	instanceLabel := fmt.Sprintf("instance %d", instanceID)
	var inst database.Instance
	if err := database.DB.Select("display_name").First(&inst, instanceID).Error; err == nil && inst.DisplayName != "" {
		instanceLabel = inst.DisplayName
	}
	taskID := m.tasks.Start(taskmanager.StartOpts{
		Type:       taskmanager.TaskBrowserMigrate,
		InstanceID: instanceID,
		UserID:     userID,
		Title:      fmt.Sprintf("Migrating instance %s", instanceLabel),
		OnCancel:   m.makeRollback(instanceID),
		Run: func(taskCtx context.Context, h *taskmanager.Handle) error {
			return m.run(taskCtx, h, instanceID)
		},
	})
	return taskID, nil
}

func (m *Migrator) run(ctx context.Context, h *taskmanager.Handle, instanceID uint) error {
	var inst database.Instance
	if err := database.DB.First(&inst, instanceID).Error; err != nil {
		return fmt.Errorf("instance %d: %w", instanceID, err)
	}
	if !database.IsLegacyEmbedded(inst.ContainerImage) {
		return fmt.Errorf("instance %d is already on the new layout", instanceID)
	}

	// Resolve target images.
	agentImage, _ := database.GetSetting("default_agent_image")
	if agentImage == "" {
		return fmt.Errorf("default_agent_image setting is empty — configure it before migrating")
	}
	browserImage := deriveBrowserImage(inst.ContainerImage)
	if override, _ := database.GetSetting("default_browser_image"); override != "" && browserImage == "" {
		browserImage = override
	}

	provider, _ := database.GetSetting("default_browser_provider")
	if provider == "" || provider == "auto" {
		provider = m.orch.BackendName()
	}

	// Capture pre-migration values so we can revert the row if anything below
	// fails (e.g., the new agent image isn't available in the registry yet).
	prev := snapshot{
		ContainerImage:  inst.ContainerImage,
		BrowserProvider: inst.BrowserProvider,
		BrowserImage:    inst.BrowserImage,
		BrowserStorage:  inst.BrowserStorage,
	}

	h.UpdateMessage("Updating instance image")
	updates := map[string]interface{}{
		"container_image":  agentImage,
		"browser_provider": provider,
		"browser_image":    browserImage,
	}
	storageChanged := false
	if inst.BrowserStorage == "" {
		if def, _ := database.GetSetting("default_browser_storage"); def != "" {
			updates["browser_storage"] = def
			storageChanged = true
		}
	}
	if err := database.DB.Model(&database.Instance{}).Where("id = ?", instanceID).Updates(updates).Error; err != nil {
		return fmt.Errorf("update instance %d: %w", instanceID, err)
	}

	h.UpdateMessage("Rolling out new agent image")
	params := orchestrator.CreateParams{
		Name:            inst.Name,
		CPURequest:      inst.CPURequest,
		CPULimit:        inst.CPULimit,
		MemoryRequest:   inst.MemoryRequest,
		MemoryLimit:     inst.MemoryLimit,
		StorageHomebrew: inst.StorageHomebrew,
		StorageHome:     inst.StorageHome,
		ContainerImage:  agentImage,
		VNCResolution:   inst.VNCResolution,
		Timezone:        inst.Timezone,
		UserAgent:       inst.UserAgent,
	}
	if err := m.orch.UpdateImage(ctx, inst.Name, params); err != nil {
		// Pull/rollout failed — revert the row so the instance keeps using
		// its existing legacy image and the user can retry once the new
		// image is available (e.g. after pushing it to the registry, or
		// building it locally with `docker build -f agent/instance/Dockerfile`
		// -t claworc/openclaw:latest agent/instance/`).
		revertSnapshot(instanceID, prev, storageChanged)
		return fmt.Errorf("rollout new agent image: %w (instance left on legacy image; build/push the agent image first or update default_agent_image)", err)
	}

	h.UpdateMessage("Recording browser session row")
	row := &database.BrowserSession{
		InstanceID: instanceID,
		Provider:   provider,
		Status:     "stopped",
		Image:      browserImage,
		PodName:    inst.Name + "-browser",
	}
	if err := database.UpsertBrowserSession(row); err != nil {
		return fmt.Errorf("upsert browser_session: %w", err)
	}

	h.UpdateMessage("Migration complete")
	return nil
}

// snapshot captures the Instance columns the migrator overwrites so they can
// be restored on failure.
type snapshot struct {
	ContainerImage  string
	BrowserProvider string
	BrowserImage    string
	BrowserStorage  string
}

func revertSnapshot(instanceID uint, s snapshot, storageChanged bool) {
	updates := map[string]interface{}{
		"container_image":  s.ContainerImage,
		"browser_provider": s.BrowserProvider,
		"browser_image":    s.BrowserImage,
	}
	if storageChanged {
		updates["browser_storage"] = s.BrowserStorage
	}
	if err := database.DB.Model(&database.Instance{}).Where("id = ?", instanceID).Updates(updates).Error; err != nil {
		// Log via fmt rather than pulling in log here; the caller already
		// returns an error to TaskManager which surfaces in the toast.
		fmt.Printf("browserprov: revert snapshot for instance %d: %v\n", instanceID, err)
	}
}

func (m *Migrator) makeRollback(instanceID uint) taskmanager.OnCancel {
	return func(ctx context.Context) {
		// Best-effort: if the image swap had happened, revert it. We don't
		// know the original image from inside the cancel callback (the row
		// has been overwritten), so we leave the row alone and let the
		// operator inspect logs. This is acceptable because the migration
		// is one-way and the rollout itself is idempotent.
		_ = database.UpdateBrowserSessionStatus(instanceID, "stopped", "migration canceled")
	}
}

// deriveBrowserImage maps a legacy combined image name to the corresponding
// browser-only image. e.g. glukw/openclaw-vnc-chromium:latest →
// claworc/chromium-browser:latest. Returns "" when no mapping applies;
// callers fall back to default_browser_image.
func deriveBrowserImage(legacy string) string {
	idx := strings.Index(legacy, "openclaw-vnc-")
	if idx < 0 {
		return ""
	}
	rest := legacy[idx+len("openclaw-vnc-"):]
	// rest now looks like "chromium:latest"
	colon := strings.Index(rest, ":")
	variant := rest
	tag := "latest"
	if colon >= 0 {
		variant = rest[:colon]
		tag = rest[colon+1:]
	}
	return fmt.Sprintf("claworc/%s-browser:%s", variant, tag)
}
