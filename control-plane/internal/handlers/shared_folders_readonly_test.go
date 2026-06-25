package handlers

import (
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestSharedFolderReadWritePersists guards against the GORM default-tag gotcha:
// a read-write (ReadOnly=false) host folder must persist as false, not be
// silently flipped to read-only by a column default.
func TestSharedFolderReadWritePersists(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&database.SharedFolder{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	database.DB = db
	t.Cleanup(func() { database.DB = nil })

	rw := &database.SharedFolder{
		Name:      "rw",
		MountPath: "/shared/rw",
		HostPath:  "/tmp/rw",
		ReadOnly:  false,
		OwnerID:   1,
	}
	if err := database.CreateSharedFolder(rw); err != nil {
		t.Fatalf("create rw folder: %v", err)
	}
	ro := &database.SharedFolder{
		Name:      "ro",
		MountPath: "/shared/ro",
		HostPath:  "/tmp/ro",
		ReadOnly:  true,
		OwnerID:   1,
	}
	if err := database.CreateSharedFolder(ro); err != nil {
		t.Fatalf("create ro folder: %v", err)
	}

	gotRW, err := database.GetSharedFolder(rw.ID)
	if err != nil {
		t.Fatalf("get rw folder: %v", err)
	}
	if gotRW.ReadOnly {
		t.Errorf("read-write folder persisted as read-only")
	}

	gotRO, err := database.GetSharedFolder(ro.ID)
	if err != nil {
		t.Fatalf("get ro folder: %v", err)
	}
	if !gotRO.ReadOnly {
		t.Errorf("read-only folder persisted as read-write")
	}
}
