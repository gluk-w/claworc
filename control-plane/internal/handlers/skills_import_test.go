package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// setupSkillsTestDB spins up an in-memory DB with the Skill table migrated and
// points config.Cfg.DataPath at a temp dir for filesystem writes.
func setupSkillsTestDB(t *testing.T) string {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&database.Skill{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	database.DB = db
	t.Cleanup(func() { database.DB = nil })

	dir := t.TempDir()
	prev := config.Cfg.DataPath
	config.Cfg.DataPath = dir
	t.Cleanup(func() { config.Cfg.DataPath = prev })
	return dir
}

func TestSaveSkillToLibrary(t *testing.T) {
	dir := setupSkillsTestDB(t)

	fm := &skillFrontmatter{
		Name:            "demo",
		Description:     "A demo skill",
		RequiredEnvVars: []string{"API_KEY"},
	}
	files := map[string][]byte{
		"SKILL.md":  []byte("---\nname: demo\ndescription: A demo skill\n---\nbody\n"),
		"lib/x.txt": []byte("hello"),
	}

	skill, err := saveSkillToLibrary("demo", fm, files)
	if err != nil {
		t.Fatalf("saveSkillToLibrary: %v", err)
	}
	if skill.Slug != "demo" || skill.Name != "demo" || skill.Summary != "A demo skill" {
		t.Errorf("unexpected skill record: %+v", skill)
	}

	// Files written to disk under the slug directory.
	got, err := os.ReadFile(filepath.Join(dir, "skills", "demo", "lib", "x.txt"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("file content = %q, want %q", got, "hello")
	}

	// DB record persisted.
	var count int64
	database.DB.Model(&database.Skill{}).Where("slug = ?", "demo").Count(&count)
	if count != 1 {
		t.Errorf("skill count = %d, want 1", count)
	}
}

func TestSaveSkillToLibrary_RejectsUnsafeSlug(t *testing.T) {
	setupSkillsTestDB(t)

	fm := &skillFrontmatter{Name: "x", Description: "x"}
	files := map[string][]byte{"SKILL.md": []byte("body")}

	for _, slug := range []string{"../evil", "..", ".", "a/b", `a\b`, ""} {
		if _, err := saveSkillToLibrary(slug, fm, files); err == nil {
			t.Errorf("expected error for unsafe slug %q, got nil", slug)
		}
	}
}

func TestSaveSkillToLibrary_RejectsTraversalFileName(t *testing.T) {
	dir := setupSkillsTestDB(t)

	fm := &skillFrontmatter{Name: "demo", Description: "demo"}
	files := map[string][]byte{
		"SKILL.md":           []byte("body"),
		"../../../etc/pwned": []byte("nope"),
	}

	if _, err := saveSkillToLibrary("demo", fm, files); err == nil {
		t.Fatal("expected error for traversal file name, got nil")
	}

	// Nothing must be written outside the skills directory.
	if _, err := os.Stat(filepath.Join(dir, "etc", "pwned")); err == nil {
		t.Error("traversal file escaped outside the skills dir")
	}
}

func TestNextAvailableSkillSlug(t *testing.T) {
	setupSkillsTestDB(t)

	// No collisions yet → first candidate is base-1.
	if got := nextAvailableSkillSlug("demo"); got != "demo-1" {
		t.Errorf("nextAvailableSkillSlug = %q, want %q", got, "demo-1")
	}

	// Seed base and base-1; next free is base-2.
	for _, slug := range []string{"demo", "demo-1"} {
		if err := database.DB.Create(&database.Skill{Slug: slug, Name: slug}).Error; err != nil {
			t.Fatalf("seed %q: %v", slug, err)
		}
	}
	if got := nextAvailableSkillSlug("demo"); got != "demo-2" {
		t.Errorf("nextAvailableSkillSlug = %q, want %q", got, "demo-2")
	}
}
