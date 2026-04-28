package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/middleware"
	"github.com/go-chi/chi/v5"
)

func TestIsValidMountPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path string
		want bool
	}{
		{"/data/shared", true},
		{"/mnt/project", true},
		{"/home/claworc", false},              // reserved
		{"/home/claworc/data", false},         // reserved prefix
		{"/home/linuxbrew", false},            // reserved
		{"/home/linuxbrew/.linuxbrew", false}, // reserved prefix
		{"/dev/shm", false},                   // reserved
		{"relative/path", false},              // not absolute
		{"", false},                           // empty
	}
	for _, tt := range tests {
		got := isValidMountPath(tt.path)
		if got != tt.want {
			t.Errorf("isValidMountPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMergeUintSets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		a    []uint
		b    []uint
		want []uint
	}{
		{[]uint{1, 2}, []uint{3}, []uint{1, 2, 3}},
		{[]uint{1, 2}, []uint{2, 3}, []uint{1, 2, 3}},
		{nil, []uint{1}, []uint{1}},
		{nil, nil, []uint{}},
		{[]uint{}, []uint{}, []uint{}},
	}
	for _, tt := range tests {
		got := mergeUintSets(tt.a, tt.b)
		sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
		sort.Slice(tt.want, func(i, j int) bool { return tt.want[i] < tt.want[j] })
		if len(got) != len(tt.want) {
			t.Errorf("mergeUintSets(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("mergeUintSets(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
				break
			}
		}
	}
}

func TestSymmetricDiffUint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a    []uint
		b    []uint
		want []uint
	}{
		{"removed only", []uint{1, 2}, []uint{1}, []uint{2}},
		{"added only", []uint{1}, []uint{1, 2}, []uint{2}},
		{"added and removed", []uint{1, 2}, []uint{2, 3}, []uint{1, 3}},
		{"identical", []uint{1, 2}, []uint{1, 2}, []uint{}},
		{"both empty", nil, nil, []uint{}},
		{"all removed", []uint{1, 2}, nil, []uint{1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := symmetricDiffUint(tt.a, tt.b)
			sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
			sort.Slice(tt.want, func(i, j int) bool { return tt.want[i] < tt.want[j] })
			if len(got) != len(tt.want) {
				t.Fatalf("symmetricDiffUint(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("symmetricDiffUint(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
				}
			}
		})
	}
}

// setupSharedFolderTestDB sets up an in-memory DB with the SharedFolder schema.
// Reuses the package-level setupTestDB helper, then adds the SharedFolder table.
func setupSharedFolderTestDB(t *testing.T) {
	t.Helper()
	setupTestDB(t)
	if err := database.DB.AutoMigrate(&database.SharedFolder{}); err != nil {
		t.Fatalf("migrate SharedFolder: %v", err)
	}
}

func TestMountPathTaken(t *testing.T) {
	setupSharedFolderTestDB(t)

	existing := &database.SharedFolder{
		Name:      "first",
		MountPath: "/shared/data",
		OwnerID:   1,
	}
	if err := database.CreateSharedFolder(existing); err != nil {
		t.Fatalf("seed folder: %v", err)
	}

	cases := []struct {
		name      string
		path      string
		excludeID uint
		want      bool
	}{
		{"unique path returns false", "/shared/other", 0, false},
		{"existing path with no exclude returns true", "/shared/data", 0, true},
		{"existing path excluding self returns false", "/shared/data", existing.ID, false},
		{"existing path excluding different id returns true", "/shared/data", existing.ID + 99, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := mountPathTaken(tc.path, tc.excludeID)
			if err != nil {
				t.Fatalf("mountPathTaken: %v", err)
			}
			if got != tc.want {
				t.Errorf("mountPathTaken(%q, %d) = %v, want %v", tc.path, tc.excludeID, got, tc.want)
			}
		})
	}
}

// authedRequest builds an *http.Request with a fake user attached, mimicking
// what middleware.AuthRequired would do at runtime.
func authedRequest(method, target string, body []byte, user *database.User) *http.Request {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rdr)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	r = r.WithContext(middleware.WithUser(r.Context(), user))
	return r
}

func adminUser(t *testing.T) *database.User {
	t.Helper()
	u := &database.User{Username: "admin-test", PasswordHash: "x", Role: "admin"}
	if err := database.DB.Create(u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func regularUser(t *testing.T, name string) *database.User {
	t.Helper()
	u := &database.User{Username: name, PasswordHash: "x", Role: "user"}
	if err := database.DB.Create(u).Error; err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	return u
}

// folderUpdateReq builds a PUT request for /api/v1/shared-folders/{id} with
// chi URL param wired in.
func folderUpdateReq(id uint, body []byte, user *database.User) *http.Request {
	r := authedRequest(http.MethodPut, fmt.Sprintf("/api/v1/shared-folders/%d", id), body, user)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", id))
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func folderDeleteReq(id uint, user *database.User) *http.Request {
	r := authedRequest(http.MethodDelete, fmt.Sprintf("/api/v1/shared-folders/%d", id), nil, user)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", id))
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func folderGetReq(id uint, user *database.User) *http.Request {
	r := authedRequest(http.MethodGet, fmt.Sprintf("/api/v1/shared-folders/%d", id), nil, user)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", fmt.Sprintf("%d", id))
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestCreateSharedFolder_DuplicateMountPath(t *testing.T) {
	setupSharedFolderTestDB(t)
	user := adminUser(t)

	body, _ := json.Marshal(map[string]string{
		"name":       "first",
		"mount_path": "/shared/dup",
	})

	// First create — should succeed.
	w := httptest.NewRecorder()
	CreateSharedFolder(w, authedRequest(http.MethodPost, "/api/v1/shared-folders", body, user))
	if w.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Second create with same mount_path — should be rejected.
	body2, _ := json.Marshal(map[string]string{
		"name":       "second",
		"mount_path": "/shared/dup",
	})
	w = httptest.NewRecorder()
	CreateSharedFolder(w, authedRequest(http.MethodPost, "/api/v1/shared-folders", body2, user))
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate create: expected 409, got %d (body: %s)", w.Code, w.Body.String())
	}

	var count int64
	database.DB.Model(&database.SharedFolder{}).Count(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 shared folder after duplicate attempt, got %d", count)
	}
}

func TestUpdateSharedFolder_DuplicateMountPath(t *testing.T) {
	setupSharedFolderTestDB(t)
	user := adminUser(t)

	a := &database.SharedFolder{Name: "a", MountPath: "/shared/a", OwnerID: user.ID}
	b := &database.SharedFolder{Name: "b", MountPath: "/shared/b", OwnerID: user.ID}
	if err := database.CreateSharedFolder(a); err != nil {
		t.Fatalf("seed a: %v", err)
	}
	if err := database.CreateSharedFolder(b); err != nil {
		t.Fatalf("seed b: %v", err)
	}

	updateRequest := func(id uint, body []byte) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := authedRequest(http.MethodPut, fmt.Sprintf("/api/v1/shared-folders/%d", id), body, user)
		// Inject the chi URL param so chi.URLParam(r, "id") works without a router.
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", fmt.Sprintf("%d", id))
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
		UpdateSharedFolder(w, r)
		return w
	}

	// Updating b's mount_path to a's path — should be rejected.
	conflict, _ := json.Marshal(map[string]string{"mount_path": "/shared/a"})
	w := updateRequest(b.ID, conflict)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate update: expected 409, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Updating b to its own current mount_path — should succeed (excludeID branch).
	same, _ := json.Marshal(map[string]string{"mount_path": "/shared/b"})
	w = updateRequest(b.ID, same)
	if w.Code != http.StatusOK {
		t.Fatalf("self-path update: expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	// b's path should still be /shared/b.
	var reloaded database.SharedFolder
	if err := database.DB.First(&reloaded, b.ID).Error; err != nil {
		t.Fatalf("reload b: %v", err)
	}
	if reloaded.MountPath != "/shared/b" {
		t.Errorf("b.mount_path = %q, want /shared/b", reloaded.MountPath)
	}
}

// --- Validation ---

func TestCreateSharedFolder_Validation(t *testing.T) {
	setupSharedFolderTestDB(t)
	user := adminUser(t)

	cases := []struct {
		name     string
		body     map[string]string
		wantCode int
	}{
		{"missing name", map[string]string{"mount_path": "/shared/x"}, http.StatusBadRequest},
		{"empty name", map[string]string{"name": "", "mount_path": "/shared/x"}, http.StatusBadRequest},
		{"reserved path", map[string]string{"name": "x", "mount_path": "/home/claworc/data"}, http.StatusBadRequest},
		{"relative path", map[string]string{"name": "x", "mount_path": "relative/path"}, http.StatusBadRequest},
		{"empty path", map[string]string{"name": "x", "mount_path": ""}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			w := httptest.NewRecorder()
			CreateSharedFolder(w, authedRequest(http.MethodPost, "/api/v1/shared-folders", body, user))
			if w.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d (body: %s)", tc.wantCode, w.Code, w.Body.String())
			}
		})
	}
}

func TestCreateSharedFolder_Unauthenticated(t *testing.T) {
	setupSharedFolderTestDB(t)
	body, _ := json.Marshal(map[string]string{"name": "x", "mount_path": "/shared/x"})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/shared-folders", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	CreateSharedFolder(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestUpdateSharedFolder_NoFields(t *testing.T) {
	setupSharedFolderTestDB(t)
	user := adminUser(t)
	sf := &database.SharedFolder{Name: "a", MountPath: "/shared/a", OwnerID: user.ID}
	if err := database.CreateSharedFolder(sf); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body, _ := json.Marshal(map[string]string{}) // nothing to update
	w := httptest.NewRecorder()
	UpdateSharedFolder(w, folderUpdateReq(sf.ID, body, user))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestUpdateSharedFolder_NotFound(t *testing.T) {
	setupSharedFolderTestDB(t)
	user := adminUser(t)
	body, _ := json.Marshal(map[string]string{"name": "z"})
	w := httptest.NewRecorder()
	UpdateSharedFolder(w, folderUpdateReq(9999, body, user))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestUpdateSharedFolder_InvalidID(t *testing.T) {
	setupSharedFolderTestDB(t)
	user := adminUser(t)
	body, _ := json.Marshal(map[string]string{"name": "z"})
	r := authedRequest(http.MethodPut, "/api/v1/shared-folders/abc", body, user)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "abc")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()
	UpdateSharedFolder(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body: %s)", w.Code, w.Body.String())
	}
}

// --- Authorization & ownership ---

func TestGetSharedFolder_NonAdminCannotReadOthers(t *testing.T) {
	setupSharedFolderTestDB(t)
	owner := regularUser(t, "owner")
	other := regularUser(t, "other")
	sf := &database.SharedFolder{Name: "private", MountPath: "/shared/p", OwnerID: owner.ID}
	if err := database.CreateSharedFolder(sf); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Owner can read.
	w := httptest.NewRecorder()
	GetSharedFolder(w, folderGetReq(sf.ID, owner))
	if w.Code != http.StatusOK {
		t.Fatalf("owner read: expected 200, got %d", w.Code)
	}

	// Other user is forbidden.
	w = httptest.NewRecorder()
	GetSharedFolder(w, folderGetReq(sf.ID, other))
	if w.Code != http.StatusForbidden {
		t.Fatalf("other read: expected 403, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Admin can read.
	admin := adminUser(t)
	w = httptest.NewRecorder()
	GetSharedFolder(w, folderGetReq(sf.ID, admin))
	if w.Code != http.StatusOK {
		t.Fatalf("admin read: expected 200, got %d", w.Code)
	}
}

func TestUpdateSharedFolder_NonAdminCannotModifyOthers(t *testing.T) {
	setupSharedFolderTestDB(t)
	owner := regularUser(t, "owner")
	other := regularUser(t, "other")
	sf := &database.SharedFolder{Name: "private", MountPath: "/shared/p", OwnerID: owner.ID}
	if err := database.CreateSharedFolder(sf); err != nil {
		t.Fatalf("seed: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"name": "renamed"})
	w := httptest.NewRecorder()
	UpdateSharedFolder(w, folderUpdateReq(sf.ID, body, other))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestDeleteSharedFolder_NonAdminCannotDeleteOthers(t *testing.T) {
	setupSharedFolderTestDB(t)
	owner := regularUser(t, "owner")
	other := regularUser(t, "other")
	sf := &database.SharedFolder{Name: "private", MountPath: "/shared/p", OwnerID: owner.ID}
	if err := database.CreateSharedFolder(sf); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := httptest.NewRecorder()
	DeleteSharedFolder(w, folderDeleteReq(sf.ID, other))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Folder still exists.
	if _, err := database.GetSharedFolder(sf.ID); err != nil {
		t.Fatalf("folder should still exist: %v", err)
	}
}

func TestUpdateSharedFolder_NonAdminCannotMapInaccessibleInstance(t *testing.T) {
	setupSharedFolderTestDB(t)
	if err := database.DB.AutoMigrate(&database.Instance{}, &database.UserInstance{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	owner := regularUser(t, "owner")

	// Create an instance the user is NOT assigned to.
	inst := database.Instance{Name: "bot-x", DisplayName: "x", Status: "running"}
	if err := database.DB.Create(&inst).Error; err != nil {
		t.Fatalf("create inst: %v", err)
	}

	sf := &database.SharedFolder{Name: "owned", MountPath: "/shared/o", OwnerID: owner.ID}
	if err := database.CreateSharedFolder(sf); err != nil {
		t.Fatalf("seed sf: %v", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"instance_ids": []uint{inst.ID},
	})
	w := httptest.NewRecorder()
	UpdateSharedFolder(w, folderUpdateReq(sf.ID, body, owner))
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d (body: %s)", w.Code, w.Body.String())
	}
}

// --- Listing semantics ---

func TestListSharedFolders_AdminSeesAll_UserSeesOwn(t *testing.T) {
	setupSharedFolderTestDB(t)
	owner := regularUser(t, "owner")
	other := regularUser(t, "other")
	admin := adminUser(t)

	mine := &database.SharedFolder{Name: "mine", MountPath: "/shared/mine", OwnerID: owner.ID}
	theirs := &database.SharedFolder{Name: "theirs", MountPath: "/shared/theirs", OwnerID: other.ID}
	if err := database.CreateSharedFolder(mine); err != nil {
		t.Fatalf("seed mine: %v", err)
	}
	if err := database.CreateSharedFolder(theirs); err != nil {
		t.Fatalf("seed theirs: %v", err)
	}

	listAs := func(u *database.User) []map[string]any {
		w := httptest.NewRecorder()
		ListSharedFolders(w, authedRequest(http.MethodGet, "/api/v1/shared-folders", nil, u))
		if w.Code != http.StatusOK {
			t.Fatalf("list (%s): expected 200, got %d", u.Username, w.Code)
		}
		var out []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		return out
	}

	if got := listAs(owner); len(got) != 1 || got[0]["name"] != "mine" {
		t.Errorf("owner list: expected only 'mine', got %v", got)
	}
	if got := listAs(other); len(got) != 1 || got[0]["name"] != "theirs" {
		t.Errorf("other list: expected only 'theirs', got %v", got)
	}
	if got := listAs(admin); len(got) != 2 {
		t.Errorf("admin list: expected 2 folders, got %d", len(got))
	}
}

func TestListSharedFolders_RoundTripsInstanceIDs(t *testing.T) {
	setupSharedFolderTestDB(t)
	user := adminUser(t)
	sf := &database.SharedFolder{
		Name:        "x",
		MountPath:   "/shared/x",
		OwnerID:     user.ID,
		InstanceIDs: database.EncodeSharedFolderInstanceIDs([]uint{4, 7, 9}),
	}
	if err := database.CreateSharedFolder(sf); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := httptest.NewRecorder()
	ListSharedFolders(w, authedRequest(http.MethodGet, "/api/v1/shared-folders", nil, user))
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var out []struct {
		InstanceIDs []uint `json:"instance_ids"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 folder, got %d", len(out))
	}
	want := []uint{4, 7, 9}
	if len(out[0].InstanceIDs) != len(want) {
		t.Fatalf("instance_ids = %v, want %v", out[0].InstanceIDs, want)
	}
	for i, v := range want {
		if out[0].InstanceIDs[i] != v {
			t.Fatalf("instance_ids = %v, want %v", out[0].InstanceIDs, want)
		}
	}
}

// --- Restart-targeting logic ---

func TestComputeFolderUpdateRestartTargets(t *testing.T) {
	t.Parallel()
	type tgt = folderRestartTarget
	cases := []struct {
		name              string
		oldIDs, newIDs    []uint
		mountPathChanged  bool
		membershipChanged bool
		want              []tgt
	}{
		{
			name:   "no change → no targets",
			oldIDs: []uint{1, 2}, newIDs: []uint{1, 2},
			mountPathChanged: false, membershipChanged: false,
			want: nil,
		},
		{
			name:   "membership add",
			oldIDs: []uint{1}, newIDs: []uint{1, 2},
			mountPathChanged: false, membershipChanged: true,
			want: []tgt{{2, "Adding shared folder"}},
		},
		{
			name:   "membership remove",
			oldIDs: []uint{1, 2}, newIDs: []uint{1},
			mountPathChanged: false, membershipChanged: true,
			want: []tgt{{2, "Deleting shared folder"}},
		},
		{
			name:   "membership add and remove",
			oldIDs: []uint{1, 2}, newIDs: []uint{2, 3},
			mountPathChanged: false, membershipChanged: true,
			want: []tgt{{1, "Deleting shared folder"}, {3, "Adding shared folder"}},
		},
		{
			name:   "membership unchanged but kept set untouched",
			oldIDs: []uint{1, 2, 3}, newIDs: []uint{1, 2, 3},
			mountPathChanged: false, membershipChanged: true,
			want: []tgt{},
		},
		{
			name:   "mount path change touches union",
			oldIDs: []uint{1, 2}, newIDs: []uint{2, 3},
			mountPathChanged: true, membershipChanged: true,
			want: []tgt{{1, "Deleting shared folder"}, {2, "Adding shared folder"}, {3, "Adding shared folder"}},
		},
		{
			name:   "mount path change with stable membership restarts everyone",
			oldIDs: []uint{1, 2}, newIDs: []uint{1, 2},
			mountPathChanged: true, membershipChanged: false,
			want: []tgt{{1, "Adding shared folder"}, {2, "Adding shared folder"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := computeFolderUpdateRestartTargets(tc.oldIDs, tc.newIDs, tc.mountPathChanged, tc.membershipChanged)
			sort.Slice(got, func(i, j int) bool { return got[i].InstanceID < got[j].InstanceID })
			sort.Slice(tc.want, func(i, j int) bool { return tc.want[i].InstanceID < tc.want[j].InstanceID })
			if len(got) != len(tc.want) {
				t.Fatalf("got %d targets %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("target[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
