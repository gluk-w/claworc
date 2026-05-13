// migrationcheck applies all registered migrations against a fresh
// SQLite database and verifies that the resulting schema matches the
// current GORM model definitions in internal/database/models.go. It
// exits non-zero with a diff when a model change has shipped without a
// corresponding migration.
//
// Usage:
//
//	go run ./cmd/migrationcheck          # exit 0 on success, 1 on drift
//	go run ./cmd/migrationcheck -dump    # print expected vs applied schema as JSON
//
// CI runs this on every PR. Developers run it locally to confirm a new
// migration closes a drift introduced by editing models.go.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
)

func main() {
	dump := flag.Bool("dump", false, "print schema (tables + columns) as JSON and exit")
	flag.Parse()

	tmpDir, err := os.MkdirTemp("", "migrationcheck-")
	if err != nil {
		fatal("mktemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Force fresh SQLite so we measure the migration output, not whatever
	// the developer happens to have in their dev data dir.
	config.Cfg.DataPath = tmpDir
	config.Cfg.Database = ""

	if err := database.Init(); err != nil {
		fatal("database.Init (this often means migrations themselves failed): %v", err)
	}
	defer database.Close()

	if *dump {
		dumpSchema()
		return
	}

	missing := checkDrift()
	if len(missing) == 0 {
		fmt.Println("migrationcheck: OK — schema matches models")
		return
	}

	fmt.Fprintln(os.Stderr, "migrationcheck: drift detected — models reference tables/columns not produced by migrations:")
	for _, m := range missing {
		fmt.Fprintf(os.Stderr, "  - %s\n", m)
	}
	fmt.Fprintln(os.Stderr, "\nRun `make migration` from control-plane/ to generate the missing migration.")
	os.Exit(1)
}

// allMigratedModels enumerates every model AutoMigrate is called against
// in autoMigrateMain. Kept in lockstep with that list by convention; the
// drift check itself fails loudly if they diverge.
func allMigratedModels() []interface{} {
	return []interface{}{
		&database.Instance{},
		&database.Setting{},
		&database.User{},
		&database.UserInstance{},
		&database.WebAuthnCredential{},
		&database.LLMProvider{},
		&database.LLMGatewayKey{},
		&database.Skill{},
		&database.Backup{},
		&database.BackupSchedule{},
		&database.SharedFolder{},
		&database.KanbanBoard{},
		&database.KanbanTask{},
		&database.KanbanComment{},
		&database.KanbanArtifact{},
		&database.InstanceSoul{},
		&database.BrowserSession{},
		&database.Team{},
		&database.TeamMember{},
		&database.TeamProvider{},
	}
}

func checkDrift() []string {
	migrator := database.DB.Migrator()
	var missing []string
	for _, m := range allMigratedModels() {
		name := typeName(m)
		if !migrator.HasTable(m) {
			missing = append(missing, fmt.Sprintf("table for model %s does not exist", name))
			continue
		}
		for _, field := range structFields(m) {
			if !migrator.HasColumn(m, field) {
				missing = append(missing, fmt.Sprintf("column %s.%s does not exist", name, field))
			}
		}
	}
	sort.Strings(missing)
	return missing
}

// structFields returns the Go field names (not column names) of all
// exported, non-embedded scalar/string/time fields on the model. The
// GORM Migrator accepts Go field names and resolves them to DB columns
// using the same NamingStrategy that drove migration. We skip fields
// tagged `gorm:"-"` and anonymous/embedded fields, which mirrors what
// AutoMigrate would consider.
func structFields(model interface{}) []string {
	t := reflect.TypeOf(model)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || f.Anonymous {
			continue
		}
		tag := f.Tag.Get("gorm")
		if tag == "-" || hasGormFlag(tag, "-") {
			continue
		}
		// Skip pure relation fields (slices/structs without a gorm column
		// declaration) — they don't produce columns. We keep time.Time,
		// pointers to scalars, and basic types.
		if isRelationField(f.Type) && !hasGormColumn(tag) {
			continue
		}
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

func hasGormFlag(tag, flag string) bool {
	for _, part := range splitTag(tag) {
		if part == flag {
			return true
		}
	}
	return false
}

func hasGormColumn(tag string) bool {
	for _, part := range splitTag(tag) {
		if len(part) > len("column:") && part[:len("column:")] == "column:" {
			return true
		}
	}
	return false
}

func splitTag(tag string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(tag); i++ {
		if tag[i] == ';' {
			parts = append(parts, tag[start:i])
			start = i + 1
		}
	}
	parts = append(parts, tag[start:])
	return parts
}

func isRelationField(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return t.Elem().Kind() == reflect.Struct && t.Elem().PkgPath() != "time"
	case reflect.Struct:
		// time.Time is a struct but it's a scalar column.
		return t.PkgPath() != "time"
	}
	return false
}

func typeName(m interface{}) string {
	t := reflect.TypeOf(m)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Name()
}

type schemaDump struct {
	Tables map[string][]string `json:"tables"`
}

func dumpSchema() {
	migrator := database.DB.Migrator()
	out := schemaDump{Tables: map[string][]string{}}
	tables, err := migrator.GetTables()
	if err != nil {
		fatal("list tables: %v", err)
	}
	for _, t := range tables {
		types, err := migrator.ColumnTypes(t)
		if err != nil {
			fatal("column types for %s: %v", t, err)
		}
		cols := make([]string, 0, len(types))
		for _, c := range types {
			cols = append(cols, c.Name())
		}
		sort.Strings(cols)
		out.Tables[t] = cols
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal("encode: %v", err)
	}
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}
