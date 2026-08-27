package migrations

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/gluk-w/claworc/control-plane/internal/database/models"
)

func openMem(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return gdb
}

// TestRenameLLMProxyKeysUp_UpgradePath simulates an existing DB: the legacy
// llm_gateway_keys table holds data, AutoMigrateAll has already created the new
// llm_proxy_keys table, and the migration must carry rows across (mapping
// gateway_key → virtual_key) and drop the old table.
func TestRenameLLMProxyKeysUp_UpgradePath(t *testing.T) {
	t.Parallel()
	gdb := openMem(t)

	// Legacy table + rows (pre-rename schema).
	if err := gdb.AutoMigrate(&legacyLLMGatewayKey{}); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	seed := []legacyLLMGatewayKey{
		{InstanceID: 1, ProviderID: 10, GatewayKey: "claworc-vk-aaa"},
		{InstanceID: 1, ProviderID: 11, GatewayKey: "claworc-vk-bbb"},
		{InstanceID: 2, ProviderID: 10, GatewayKey: "claworc-vk-ccc"},
	}
	for i := range seed {
		if err := gdb.Create(&seed[i]).Error; err != nil {
			t.Fatalf("seed legacy row %d: %v", i, err)
		}
	}
	// AutoMigrateAll already created the new (empty) table on boot.
	if err := gdb.AutoMigrate(&models.LLMProxyKey{}); err != nil {
		t.Fatalf("automigrate new table: %v", err)
	}

	if err := renameLLMProxyKeysUp(gdb.Migrator(), gdb); err != nil {
		t.Fatalf("up: %v", err)
	}

	if gdb.Migrator().HasTable(llmGatewayKeysTable) {
		t.Errorf("%s should have been dropped", llmGatewayKeysTable)
	}
	var rows []models.LLMProxyKey
	if err := gdb.Order("virtual_key").Find(&rows).Error; err != nil {
		t.Fatalf("load proxy keys: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d proxy keys, want 3", len(rows))
	}
	want := map[string]struct{ inst, prov uint }{
		"claworc-vk-aaa": {1, 10},
		"claworc-vk-bbb": {1, 11},
		"claworc-vk-ccc": {2, 10},
	}
	for _, r := range rows {
		w, ok := want[r.VirtualKey]
		if !ok {
			t.Errorf("unexpected virtual_key %q", r.VirtualKey)
			continue
		}
		if r.InstanceID != w.inst || r.ProviderID != w.prov {
			t.Errorf("%s: got inst=%d prov=%d, want inst=%d prov=%d",
				r.VirtualKey, r.InstanceID, r.ProviderID, w.inst, w.prov)
		}
	}

	// Idempotent: a second Up (old table already gone) is a no-op.
	if err := renameLLMProxyKeysUp(gdb.Migrator(), gdb); err != nil {
		t.Fatalf("up second pass: %v", err)
	}
	var count int64
	gdb.Model(&models.LLMProxyKey{}).Count(&count)
	if count != 3 {
		t.Errorf("after second pass got %d rows, want 3", count)
	}
}

// TestRenameLLMProxyKeysUp_FreshInstall: no legacy table exists, so the
// migration is a no-op and the AutoMigrated new table is left intact.
func TestRenameLLMProxyKeysUp_FreshInstall(t *testing.T) {
	t.Parallel()
	gdb := openMem(t)
	if err := gdb.AutoMigrate(&models.LLMProxyKey{}); err != nil {
		t.Fatalf("automigrate new table: %v", err)
	}

	if err := renameLLMProxyKeysUp(gdb.Migrator(), gdb); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !gdb.Migrator().HasTable(llmProxyKeysTable) {
		t.Errorf("%s should still exist on fresh install", llmProxyKeysTable)
	}
}

// TestRenameLLMProxyKeysDown reverses the rename, recreating the legacy table.
func TestRenameLLMProxyKeysDown(t *testing.T) {
	t.Parallel()
	gdb := openMem(t)
	if err := gdb.AutoMigrate(&models.LLMProxyKey{}); err != nil {
		t.Fatalf("automigrate new table: %v", err)
	}
	if err := gdb.Create(&models.LLMProxyKey{InstanceID: 5, ProviderID: 7, VirtualKey: "claworc-vk-zzz"}).Error; err != nil {
		t.Fatalf("seed proxy key: %v", err)
	}

	if err := renameLLMProxyKeysDown(gdb.Migrator(), gdb); err != nil {
		t.Fatalf("down: %v", err)
	}
	if gdb.Migrator().HasTable(llmProxyKeysTable) {
		t.Errorf("%s should have been dropped on down", llmProxyKeysTable)
	}
	var rows []legacyLLMGatewayKey
	if err := gdb.Find(&rows).Error; err != nil {
		t.Fatalf("load legacy rows: %v", err)
	}
	if len(rows) != 1 || rows[0].GatewayKey != "claworc-vk-zzz" || rows[0].InstanceID != 5 || rows[0].ProviderID != 7 {
		t.Errorf("unexpected legacy rows after down: %+v", rows)
	}
}
