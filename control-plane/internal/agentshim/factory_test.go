package agentshim_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/agentshim"
	_ "github.com/gluk-w/claworc/control-plane/internal/agentshim/openclawnative"
	_ "github.com/gluk-w/claworc/control-plane/internal/agentshim/shimexec"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	gossh "golang.org/x/crypto/ssh"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	dsn := fmt.Sprintf("file:agentshim_%s_%p?mode=memory&cache=shared", t.Name(), t)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&database.Instance{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	database.DB = db
}

func TestFactory_ForInstance(t *testing.T) {
	setupTestDB(t)
	inst := database.Instance{Name: "bot-shim-test", DisplayName: "Shim Test", Status: "running"}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create instance: %v", err)
	}

	tunnelCalls := 0
	f := &agentshim.Factory{
		TunnelPort: func(instanceID uint, service string) (int, error) {
			tunnelCalls++
			if instanceID != inst.ID {
				t.Errorf("tunnel port asked for instance %d, want %d", instanceID, inst.ID)
			}
			if service != "gateway" {
				t.Errorf("tunnel port asked for service %q, want gateway", service)
			}
			return 0, fmt.Errorf("no tunnel in test")
		},
		SSHClient: func(ctx context.Context, i database.Instance) (*gossh.Client, error) {
			return nil, fmt.Errorf("no ssh in test")
		},
	}

	client, err := f.ForInstance(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("ForInstance: %v", err)
	}
	if client.Type() != "openclaw" {
		t.Fatalf("client type = %q, want openclaw", client.Type())
	}

	// Deps are resolved lazily and routed to the factory's seams.
	if err := client.Health(context.Background()); err == nil {
		t.Fatal("Health should fail without a tunnel")
	}
	if tunnelCalls != 1 {
		t.Fatalf("tunnel resolver called %d times, want 1", tunnelCalls)
	}

	caps, err := client.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if !caps.Chat {
		t.Fatal("openclaw client must report chat capability")
	}
}

func TestFactory_Dispatch(t *testing.T) {
	setupTestDB(t)

	newFactory := func(probe func(context.Context, agentshim.InstanceDeps) (bool, error)) *agentshim.Factory {
		return &agentshim.Factory{
			SSHClient: func(ctx context.Context, i database.Instance) (*gossh.Client, error) {
				return nil, fmt.Errorf("no ssh in test")
			},
			ProbeShim: probe,
		}
	}

	t.Run("openclaw without shim falls back to native", func(t *testing.T) {
		inst := database.Instance{Name: "bot-native", DisplayName: "native", AgentType: "openclaw"}
		if err := database.DB.Create(&inst).Error; err != nil {
			t.Fatal(err)
		}
		f := newFactory(func(context.Context, agentshim.InstanceDeps) (bool, error) { return false, nil })
		client, err := f.ForInstance(context.Background(), inst.ID)
		if err != nil {
			t.Fatal(err)
		}
		if client.Type() != "openclaw" {
			t.Fatalf("client type = %q, want openclaw", client.Type())
		}
	})

	t.Run("openclaw with shim prefers shimexec", func(t *testing.T) {
		inst := database.Instance{Name: "bot-shimmy", DisplayName: "shimmy", AgentType: "openclaw"}
		if err := database.DB.Create(&inst).Error; err != nil {
			t.Fatal(err)
		}
		f := newFactory(func(context.Context, agentshim.InstanceDeps) (bool, error) { return true, nil })
		client, err := f.ForInstance(context.Background(), inst.ID)
		if err != nil {
			t.Fatal(err)
		}
		if client.Type() != agentshim.ShimAdapterType {
			t.Fatalf("client type = %q, want %q", client.Type(), agentshim.ShimAdapterType)
		}
	})

	t.Run("non-openclaw types always use shimexec without probing", func(t *testing.T) {
		inst := database.Instance{Name: "bot-hermes", DisplayName: "hermes", AgentType: "hermes"}
		if err := database.DB.Create(&inst).Error; err != nil {
			t.Fatal(err)
		}
		f := newFactory(func(context.Context, agentshim.InstanceDeps) (bool, error) {
			t.Fatal("probe must not run for non-openclaw types")
			return false, nil
		})
		client, err := f.ForInstance(context.Background(), inst.ID)
		if err != nil {
			t.Fatal(err)
		}
		if client.Type() != agentshim.ShimAdapterType {
			t.Fatalf("client type = %q, want %q", client.Type(), agentshim.ShimAdapterType)
		}
	})

	t.Run("probe result is cached until invalidated", func(t *testing.T) {
		inst := database.Instance{Name: "bot-cache", DisplayName: "cache", AgentType: "openclaw"}
		if err := database.DB.Create(&inst).Error; err != nil {
			t.Fatal(err)
		}
		probes := 0
		f := newFactory(func(context.Context, agentshim.InstanceDeps) (bool, error) {
			probes++
			return true, nil
		})
		for i := 0; i < 3; i++ {
			if _, err := f.ForInstance(context.Background(), inst.ID); err != nil {
				t.Fatal(err)
			}
		}
		if probes != 1 {
			t.Fatalf("probe ran %d times, want 1 (cached)", probes)
		}
		f.InvalidateShimProbe(inst.ID)
		if _, err := f.ForInstance(context.Background(), inst.ID); err != nil {
			t.Fatal(err)
		}
		if probes != 2 {
			t.Fatalf("probe ran %d times after invalidation, want 2", probes)
		}
	})

	t.Run("probe errors fail open to native", func(t *testing.T) {
		inst := database.Instance{Name: "bot-probe-err", DisplayName: "probe-err", AgentType: "openclaw"}
		if err := database.DB.Create(&inst).Error; err != nil {
			t.Fatal(err)
		}
		probes := 0
		f := newFactory(func(context.Context, agentshim.InstanceDeps) (bool, error) {
			probes++
			return false, fmt.Errorf("ssh down")
		})
		client, err := f.ForInstance(context.Background(), inst.ID)
		if err != nil {
			t.Fatal(err)
		}
		if client.Type() != "openclaw" {
			t.Fatalf("client type = %q, want openclaw fallback", client.Type())
		}
		// Errors are not cached — the next call probes again.
		if _, err := f.ForInstance(context.Background(), inst.ID); err != nil {
			t.Fatal(err)
		}
		if probes != 2 {
			t.Fatalf("probe ran %d times, want 2 (errors uncached)", probes)
		}
	})
}

func TestFactory_ForInstance_NotFound(t *testing.T) {
	setupTestDB(t)
	if _, err := (&agentshim.Factory{}).ForInstance(context.Background(), 9999); err == nil {
		t.Fatal("expected error for missing instance")
	}
}

func TestDefaultFactory_NeverNil(t *testing.T) {
	if agentshim.DefaultFactory() == nil {
		t.Fatal("DefaultFactory returned nil")
	}
}
