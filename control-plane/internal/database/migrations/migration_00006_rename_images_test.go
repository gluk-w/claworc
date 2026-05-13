package migrations

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
)

func TestRenameImages(t *testing.T) {
	t.Parallel()

	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := gdb.AutoMigrate(&models.Instance{}, &models.Setting{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	seedInstances := []models.Instance{
		{Name: "bot-a", DisplayName: "A", Status: "running",
			ContainerImage: "glukw/claworc-agent:latest",
			BrowserImage:   "glukw/claworc-browser-chromium:v1"},
		{Name: "bot-b", DisplayName: "B", Status: "running",
			ContainerImage: "docker.io/glukw/claworc-agent:dev",
			BrowserImage:   "docker.io/glukw/claworc-browser-brave:dev"},
		{Name: "bot-c", DisplayName: "C", Status: "running",
			ContainerImage: "claworc/openclaw:latest", // already migrated
			BrowserImage:   "claworc/chromium-browser:latest"},
		{Name: "bot-d", DisplayName: "D", Status: "running",
			ContainerImage: "glukw/openclaw-vnc-chromium:latest", // legacy combined — not touched
			BrowserImage:   ""},
	}
	for i := range seedInstances {
		if err := gdb.Create(&seedInstances[i]).Error; err != nil {
			t.Fatalf("seed instance %d: %v", i, err)
		}
	}

	settings := []models.Setting{
		{Key: "default_agent_image", Value: "glukw/claworc-agent:latest"},
		{Key: "default_browser_image", Value: "glukw/claworc-browser-chromium:latest"},
		{Key: "unrelated_setting", Value: "glukw/claworc-agent:latest"}, // wrong key, must not change
	}
	for i := range settings {
		if err := gdb.Create(&settings[i]).Error; err != nil {
			t.Fatalf("seed setting %d: %v", i, err)
		}
	}

	if err := renameImages(gdb); err != nil {
		t.Fatalf("renameImages: %v", err)
	}

	wantInstances := map[string]struct{ container, browser string }{
		"bot-a": {"claworc/openclaw:latest", "claworc/chromium-browser:v1"},
		"bot-b": {"docker.io/claworc/openclaw:dev", "docker.io/claworc/brave-browser:dev"},
		"bot-c": {"claworc/openclaw:latest", "claworc/chromium-browser:latest"},
		"bot-d": {"glukw/openclaw-vnc-chromium:latest", ""},
	}
	var got []models.Instance
	if err := gdb.Find(&got).Error; err != nil {
		t.Fatalf("load instances: %v", err)
	}
	for _, inst := range got {
		w, ok := wantInstances[inst.Name]
		if !ok {
			t.Errorf("unexpected instance %q", inst.Name)
			continue
		}
		if inst.ContainerImage != w.container {
			t.Errorf("%s container_image = %q, want %q", inst.Name, inst.ContainerImage, w.container)
		}
		if inst.BrowserImage != w.browser {
			t.Errorf("%s browser_image = %q, want %q", inst.Name, inst.BrowserImage, w.browser)
		}
	}

	wantSettings := map[string]string{
		"default_agent_image":   "claworc/openclaw:latest",
		"default_browser_image": "claworc/chromium-browser:latest",
		"unrelated_setting":     "glukw/claworc-agent:latest",
	}
	var setRows []models.Setting
	if err := gdb.Find(&setRows).Error; err != nil {
		t.Fatalf("load settings: %v", err)
	}
	for _, s := range setRows {
		if w, ok := wantSettings[s.Key]; ok && s.Value != w {
			t.Errorf("setting %s = %q, want %q", s.Key, s.Value, w)
		}
	}

	// Idempotent: a second pass should not change anything.
	if err := renameImages(gdb); err != nil {
		t.Fatalf("renameImages second pass: %v", err)
	}
	var got2 []models.Instance
	if err := gdb.Find(&got2).Error; err != nil {
		t.Fatalf("reload instances: %v", err)
	}
	for _, inst := range got2 {
		w := wantInstances[inst.Name]
		if inst.ContainerImage != w.container || inst.BrowserImage != w.browser {
			t.Errorf("%s changed on second pass: container=%q browser=%q",
				inst.Name, inst.ContainerImage, inst.BrowserImage)
		}
	}
}
