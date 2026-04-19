package handlers

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupHandlersTestDB(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	if err := db.AutoMigrate(&database.Setting{}, &database.Instance{}); err != nil {
		t.Fatalf("auto-migrate: %v", err)
	}
	database.DB = db
	t.Cleanup(func() { database.DB = nil })
}

func TestValidateEnvVarName_Valid(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"FOO",
		"FOO_BAR",
		"_UNDERSCORE_LEAD",
		"A1",
		"CLAWORC_FOO",      // non-reserved CLAWORC_* is allowed
		"OPENCLAW_CUSTOM",  // non-reserved OPENCLAW_* is allowed
		"OPENCLAW_API_URL", // admin overrides that are not on the reserved list
	} {
		if err := ValidateEnvVarName(name); err != nil {
			t.Errorf("ValidateEnvVarName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateEnvVarName_InvalidFormat(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"lower_case",
		"1LEADING_DIGIT",
		"HAS SPACE",
		"has-dash",
		"",
	} {
		if err := ValidateEnvVarName(name); err == nil {
			t.Errorf("ValidateEnvVarName(%q) = nil, want error", name)
		}
	}
}

func TestValidateEnvVarName_Reserved(t *testing.T) {
	t.Parallel()
	for _, name := range ReservedEnvVarNames {
		err := ValidateEnvVarName(name)
		if err == nil {
			t.Errorf("ValidateEnvVarName(%q) = nil, want reserved error", name)
			continue
		}
		if !strings.Contains(err.Error(), "reserved") {
			t.Errorf("ValidateEnvVarName(%q) error = %v, want reserved mention", name, err)
		}
	}
}

func TestUpsertEncryptedEnvVarsJSON_RoundTrip(t *testing.T) {
	setupHandlersTestDB(t)

	// Initial: create two keys
	updated, err := UpsertEncryptedEnvVarsJSON("{}", map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}, nil)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	surfaced := EnvVarsForResponse(updated)
	if len(surfaced) != 2 {
		t.Fatalf("response map has %d keys, want 2 (got %v)", len(surfaced), surfaced)
	}
	if surfaced["FOO"] != "bar" {
		t.Errorf("FOO = %q, want bar", surfaced["FOO"])
	}
	if surfaced["BAZ"] != "qux" {
		t.Errorf("BAZ = %q, want qux", surfaced["BAZ"])
	}

	// Second: update FOO, delete BAZ, add NEW
	updated, err = UpsertEncryptedEnvVarsJSON(updated, map[string]string{
		"FOO": "brand-new-value",
		"NEW": "other-value",
	}, []string{"BAZ"})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	plain := decryptEnvVars(decodeEncryptedEnvVarsJSON(updated))
	if plain["FOO"] != "brand-new-value" {
		t.Errorf("FOO = %q, want brand-new-value", plain["FOO"])
	}
	if plain["NEW"] != "other-value" {
		t.Errorf("NEW = %q, want other-value", plain["NEW"])
	}
	if _, exists := plain["BAZ"]; exists {
		t.Errorf("BAZ should have been deleted, got %q", plain["BAZ"])
	}
}

func TestUpsertEncryptedEnvVarsJSON_RejectsReserved(t *testing.T) {
	setupHandlersTestDB(t)

	_, err := UpsertEncryptedEnvVarsJSON("{}", map[string]string{
		"OPENCLAW_GATEWAY_TOKEN": "whatever",
	}, nil)
	if err == nil {
		t.Fatal("expected error for reserved name on set, got nil")
	}

	_, err = UpsertEncryptedEnvVarsJSON("{}", nil, []string{"CLAWORC_INSTANCE_ID"})
	if err == nil {
		t.Fatal("expected error for reserved name on unset, got nil")
	}
}

func TestUpsertEncryptedEnvVarsJSON_RejectsInvalidFormat(t *testing.T) {
	setupHandlersTestDB(t)

	_, err := UpsertEncryptedEnvVarsJSON("{}", map[string]string{
		"lower_case": "v",
	}, nil)
	if err == nil {
		t.Fatal("expected error for invalid name format, got nil")
	}
}

func TestMergeUserEnvVars_Precedence(t *testing.T) {
	t.Parallel()

	// Simulate the merge order used by the instance create/restart paths:
	// global -> instance -> system (system applied last by the caller).
	target := map[string]string{}
	MergeUserEnvVars(target,
		map[string]string{"FOO": "global", "ONLY_GLOBAL": "global"},
		map[string]string{"FOO": "instance", "ONLY_INST": "i"},
	)

	// Reserved system vars applied last by caller — we simulate that here.
	target["OPENCLAW_GATEWAY_TOKEN"] = "sys"
	target["CLAWORC_INSTANCE_ID"] = "42"

	want := map[string]string{
		"FOO":                    "instance",
		"ONLY_GLOBAL":            "global",
		"ONLY_INST":              "i",
		"OPENCLAW_GATEWAY_TOKEN": "sys",
		"CLAWORC_INSTANCE_ID":    "42",
	}
	if !reflect.DeepEqual(target, want) {
		t.Errorf("merged env = %v, want %v", target, want)
	}
}

func TestLoadGlobalEnvVarKeys_Sorted(t *testing.T) {
	setupHandlersTestDB(t)

	encoded, err := UpsertEncryptedEnvVarsJSON("{}", map[string]string{
		"ZETA":  "1",
		"ALPHA": "2",
		"MIKE":  "3",
	}, nil)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := database.SetSetting("default_env_vars", encoded); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	keys := LoadGlobalEnvVarKeys()
	want := []string{"ALPHA", "MIKE", "ZETA"}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("keys not sorted: %v", keys)
	}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
}

func TestLoadInstanceEnvVars_Decrypts(t *testing.T) {
	setupHandlersTestDB(t)

	encoded, err := UpsertEncryptedEnvVarsJSON("{}", map[string]string{
		"SECRET_TOKEN": "ssh-ed25519 whatever",
	}, nil)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	inst := database.Instance{EnvVars: encoded}
	plain := LoadInstanceEnvVars(inst)
	if plain["SECRET_TOKEN"] != "ssh-ed25519 whatever" {
		t.Errorf("decrypted value = %q, want %q", plain["SECRET_TOKEN"], "ssh-ed25519 whatever")
	}
}
