package database

import (
	"testing"
)

func setupPasskeyDB(t *testing.T) {
	t.Helper()
	setupTestDB(t)
	if err := DB.AutoMigrate(&BrowserPasskey{}); err != nil {
		t.Fatalf("migrate passkeys: %v", err)
	}
}

func TestUpsertBrowserPasskey_InsertThenUpdateNeverRegressesSignCount(t *testing.T) {
	setupPasskeyDB(t)
	p := &BrowserPasskey{InstanceID: 1, Authenticator: "platform", CredentialID: "c1", RpID: "a.com", PrivateKey: "enc", SignCount: 5}
	if err := UpsertBrowserPasskey(p); err != nil {
		t.Fatal(err)
	}
	// Stale write with a lower count must not move it backwards.
	if err := UpsertBrowserPasskey(&BrowserPasskey{InstanceID: 1, Authenticator: "platform", CredentialID: "c1", RpID: "a.com", PrivateKey: "enc2", SignCount: 2}); err != nil {
		t.Fatal(err)
	}
	rows, _ := ListBrowserPasskeys(1)
	if len(rows) != 1 || rows[0].SignCount != 5 || rows[0].PrivateKey != "enc2" {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if err := RecordBrowserPasskeyUse(1, "c1", 3); err != nil {
		t.Fatal(err)
	}
	if err := RecordBrowserPasskeyUse(1, "c1", 9); err != nil {
		t.Fatal(err)
	}
	rows, _ = ListBrowserPasskeys(1)
	if rows[0].SignCount != 9 || rows[0].LastUsedAt == nil {
		t.Fatalf("sign count/last used not advanced: %+v", rows[0])
	}
	if err := RecordBrowserPasskeyUse(1, "missing", 1); err == nil {
		t.Fatal("expected not-found for unknown credential")
	}
}

func TestCloneAndDeleteBrowserPasskeys(t *testing.T) {
	setupPasskeyDB(t)
	for _, c := range []string{"a", "b"} {
		if err := UpsertBrowserPasskey(&BrowserPasskey{InstanceID: 1, Authenticator: "platform", CredentialID: c, PrivateKey: "k"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := CloneBrowserPasskeys(1, 2); err != nil {
		t.Fatal(err)
	}
	dst, _ := ListBrowserPasskeys(2)
	if len(dst) != 2 || dst[0].InstanceID != 2 {
		t.Fatalf("clone failed: %+v", dst)
	}
	// Cloning again is a no-op thanks to the unique index.
	if err := CloneBrowserPasskeys(1, 2); err != nil {
		t.Fatal(err)
	}
	if dst, _ = ListBrowserPasskeys(2); len(dst) != 2 {
		t.Fatalf("re-clone duplicated rows: %d", len(dst))
	}
	if err := DeleteBrowserPasskey(2, dst[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := DeleteBrowserPasskeysForInstance(1); err != nil {
		t.Fatal(err)
	}
	src, _ := ListBrowserPasskeys(1)
	dst, _ = ListBrowserPasskeys(2)
	if len(src) != 0 || len(dst) != 1 {
		t.Fatalf("delete failed: src=%d dst=%d", len(src), len(dst))
	}
}
