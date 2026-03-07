package llmgateway

import (
	"strings"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
)

// --- 1. generateGatewayKey ---

func TestGenerateGatewayKey(t *testing.T) {
	k := generateGatewayKey()
	if !strings.HasPrefix(k, "claworc-vk-") {
		t.Errorf("expected claworc-vk- prefix, got %q", k)
	}
	// 24 random bytes → 48 hex chars + "claworc-vk-" (11) = 59
	if len(k) != 59 {
		t.Errorf("expected length 59, got %d", len(k))
	}

	k2 := generateGatewayKey()
	if k == k2 {
		t.Error("two calls returned the same key")
	}
}

// --- 2. EnsureKeysForInstance: creates missing keys ---

func TestEnsureKeysForInstance_CreatesMissingKeys(t *testing.T) {
	setupDB(t)
	p1 := mustProvider(t, "prov1", "openai-completions", "http://a")
	p2 := mustProvider(t, "prov2", "openai-completions", "http://b")

	if err := EnsureKeysForInstance(1, []uint{p1.ID, p2.ID}); err != nil {
		t.Fatalf("EnsureKeysForInstance: %v", err)
	}

	var count int64
	database.DB.Model(&database.LLMGatewayKey{}).Where("instance_id = ?", 1).Count(&count)
	if count != 2 {
		t.Errorf("expected 2 keys, got %d", count)
	}
}

// --- 3. EnsureKeysForInstance: skips existing keys ---

func TestEnsureKeysForInstance_SkipsExistingKeys(t *testing.T) {
	setupDB(t)
	p := mustProvider(t, "prov", "openai-completions", "http://a")
	mustGatewayKey(t, 1, p.ID)

	if err := EnsureKeysForInstance(1, []uint{p.ID}); err != nil {
		t.Fatalf("EnsureKeysForInstance: %v", err)
	}

	var count int64
	database.DB.Model(&database.LLMGatewayKey{}).Where("instance_id = ?", 1).Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 key (no duplicate), got %d", count)
	}
}

// --- 4. EnsureKeysForInstance: removes disabled providers ---

func TestEnsureKeysForInstance_RemovesDisabledProviders(t *testing.T) {
	setupDB(t)
	pA := mustProvider(t, "provA", "openai-completions", "http://a")
	pB := mustProvider(t, "provB", "openai-completions", "http://b")
	mustGatewayKey(t, 1, pA.ID)
	mustGatewayKey(t, 1, pB.ID)

	// Only keep A
	if err := EnsureKeysForInstance(1, []uint{pA.ID}); err != nil {
		t.Fatalf("EnsureKeysForInstance: %v", err)
	}

	var keys []database.LLMGatewayKey
	database.DB.Where("instance_id = ?", 1).Find(&keys)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key remaining, got %d", len(keys))
	}
	if keys[0].ProviderID != pA.ID {
		t.Errorf("expected provider A's key to remain, got providerID=%d", keys[0].ProviderID)
	}
}

// --- 5. EnsureKeysForInstance: empty list deletes all ---

func TestEnsureKeysForInstance_EmptyList(t *testing.T) {
	setupDB(t)
	p := mustProvider(t, "prov", "openai-completions", "http://a")
	mustGatewayKey(t, 1, p.ID)

	if err := EnsureKeysForInstance(1, []uint{}); err != nil {
		t.Fatalf("EnsureKeysForInstance: %v", err)
	}

	var count int64
	database.DB.Model(&database.LLMGatewayKey{}).Where("instance_id = ?", 1).Count(&count)
	if count != 0 {
		t.Errorf("expected 0 keys after empty list, got %d", count)
	}
}

// --- 6. GetInstanceGatewayKeys ---

func TestGetInstanceGatewayKeys(t *testing.T) {
	setupDB(t)
	p1 := mustProvider(t, "prov1", "openai-completions", "http://a")
	p2 := mustProvider(t, "prov2", "openai-completions", "http://b")
	p3 := mustProvider(t, "prov3", "openai-completions", "http://c")

	tok1 := mustGatewayKey(t, 1, p1.ID)
	tok2 := mustGatewayKey(t, 1, p2.ID)
	mustGatewayKey(t, 2, p3.ID) // different instance — must not appear

	keys := GetInstanceGatewayKeys(1)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys for instance 1, got %d", len(keys))
	}
	if keys[p1.ID] != tok1 {
		t.Errorf("provider1 key: got %q, want %q", keys[p1.ID], tok1)
	}
	if keys[p2.ID] != tok2 {
		t.Errorf("provider2 key: got %q, want %q", keys[p2.ID], tok2)
	}
	if _, ok := keys[p3.ID]; ok {
		t.Error("instance 2's key should not appear in instance 1's result")
	}
}
