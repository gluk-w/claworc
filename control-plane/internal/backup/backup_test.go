package backup

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gluk-w/claworc/control-plane/internal/config"
	"github.com/gluk-w/claworc/control-plane/internal/database"
	"github.com/gluk-w/claworc/control-plane/internal/orchestrator"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// --- mock orchestrator ---

type mockOrch struct {
	streamFn func(ctx context.Context, name string, cmd []string, stdout io.Writer) (string, int, error)
}

func (m *mockOrch) Initialize(_ context.Context) error                                  { return nil }
func (m *mockOrch) IsAvailable(_ context.Context) bool                                  { return true }
func (m *mockOrch) BackendName() string                                                 { return "mock" }
func (m *mockOrch) CreateInstance(_ context.Context, _ orchestrator.CreateParams) error  { return nil }
func (m *mockOrch) DeleteInstance(_ context.Context, _ string) error                    { return nil }
func (m *mockOrch) StartInstance(_ context.Context, _ string) error                     { return nil }
func (m *mockOrch) StopInstance(_ context.Context, _ string) error                      { return nil }
func (m *mockOrch) RestartInstance(_ context.Context, _ string, _ orchestrator.CreateParams) error {
	return nil
}
func (m *mockOrch) GetInstanceStatus(_ context.Context, _ string) (string, error)       { return "running", nil }
func (m *mockOrch) GetInstanceImageInfo(_ context.Context, _ string) (string, error)    { return "", nil }
func (m *mockOrch) UpdateInstanceConfig(_ context.Context, _, _ string) error           { return nil }
func (m *mockOrch) CloneVolumes(_ context.Context, _, _ string) error                   { return nil }
func (m *mockOrch) ConfigureSSHAccess(_ context.Context, _ uint, _ string) error        { return nil }
func (m *mockOrch) GetSSHAddress(_ context.Context, _ uint) (string, int, error)        { return "", 0, nil }
func (m *mockOrch) UpdateResources(_ context.Context, _ string, _ orchestrator.UpdateResourcesParams) error {
	return nil
}
func (m *mockOrch) GetContainerStats(_ context.Context, _ string) (*orchestrator.ContainerStats, error) {
	return nil, nil
}
func (m *mockOrch) UpdateImage(_ context.Context, _ string, _ orchestrator.CreateParams) error {
	return nil
}
func (m *mockOrch) ExecInInstance(_ context.Context, _ string, _ []string) (string, string, int, error) {
	return "", "", 0, nil
}
func (m *mockOrch) StreamExecInInstance(ctx context.Context, name string, cmd []string, stdout io.Writer) (string, int, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, name, cmd, stdout)
	}
	return "", 0, nil
}
func (m *mockOrch) DeleteSharedVolume(_ context.Context, _ uint) error { return nil }

// --- test helpers ---

func setupTestDB(t *testing.T) {
	t.Helper()
	var err error
	database.DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	database.DB.AutoMigrate(&database.Instance{}, &database.Setting{}, &database.Backup{}, &database.BackupSchedule{})
}

func setupTestDataPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	config.Cfg.DataPath = dir
	return dir
}

// --- ResolvePaths ---

func TestResolvePaths_Empty(t *testing.T) {
	result := ResolvePaths(nil)
	if len(result) != 1 || result[0] != "/home/claworc" {
		t.Errorf("expected [/home/claworc], got %v", result)
	}

	result = ResolvePaths([]string{})
	if len(result) != 1 || result[0] != "/home/claworc" {
		t.Errorf("expected [/home/claworc], got %v", result)
	}
}

func TestResolvePaths_AllEmpty(t *testing.T) {
	result := ResolvePaths([]string{"", "  ", ""})
	if len(result) != 1 || result[0] != "/home/claworc" {
		t.Errorf("expected [/home/claworc] for all-empty input, got %v", result)
	}
}

func TestResolvePaths_HOME(t *testing.T) {
	result := ResolvePaths([]string{"HOME"})
	if len(result) != 1 || result[0] != "/home/claworc" {
		t.Errorf("expected [/home/claworc], got %v", result)
	}
}

func TestResolvePaths_Homebrew(t *testing.T) {
	result := ResolvePaths([]string{"Homebrew"})
	if len(result) != 1 || result[0] != "/home/linuxbrew/.linuxbrew" {
		t.Errorf("expected [/home/linuxbrew/.linuxbrew], got %v", result)
	}
}

func TestResolvePaths_RootSlash(t *testing.T) {
	result := ResolvePaths([]string{"/"})
	if len(result) != 1 || result[0] != "/" {
		t.Errorf("expected [/], got %v", result)
	}
}

func TestResolvePaths_LiteralPath(t *testing.T) {
	result := ResolvePaths([]string{"/etc/nginx"})
	if len(result) != 1 || result[0] != "/etc/nginx" {
		t.Errorf("expected [/etc/nginx], got %v", result)
	}
}

func TestResolvePaths_Mixed(t *testing.T) {
	result := ResolvePaths([]string{"HOME", "Homebrew", "/etc/nginx", ""})
	if len(result) != 3 {
		t.Fatalf("expected 3 paths, got %d: %v", len(result), result)
	}
	if result[0] != "/home/claworc" {
		t.Errorf("expected /home/claworc, got %s", result[0])
	}
	if result[1] != "/home/linuxbrew/.linuxbrew" {
		t.Errorf("expected /home/linuxbrew/.linuxbrew, got %s", result[1])
	}
	if result[2] != "/etc/nginx" {
		t.Errorf("expected /etc/nginx, got %s", result[2])
	}
}

func TestResolvePaths_TrimWhitespace(t *testing.T) {
	result := ResolvePaths([]string{"  HOME  "})
	if len(result) != 1 || result[0] != "/home/claworc" {
		t.Errorf("expected [/home/claworc], got %v", result)
	}
}

// --- buildTarCommand ---

func TestBuildTarCommand_SinglePath(t *testing.T) {
	cmd := buildTarCommand([]string{"/home/claworc"})
	if len(cmd) != 3 || cmd[0] != "sh" || cmd[1] != "-c" {
		t.Fatalf("expected [sh -c ...], got %v", cmd)
	}
	shellCmd := cmd[2]
	if !strings.Contains(shellCmd, "/home/claworc") {
		t.Errorf("command should contain path, got: %s", shellCmd)
	}
	if !strings.Contains(shellCmd, "--exclude=/proc") {
		t.Errorf("command should contain exclusions, got: %s", shellCmd)
	}
}

func TestBuildTarCommand_MultiplePaths(t *testing.T) {
	cmd := buildTarCommand([]string{"/home/claworc", "/etc/nginx"})
	shellCmd := cmd[2]
	if !strings.Contains(shellCmd, "/home/claworc") {
		t.Errorf("command should contain /home/claworc, got: %s", shellCmd)
	}
	if !strings.Contains(shellCmd, "/etc/nginx") {
		t.Errorf("command should contain /etc/nginx, got: %s", shellCmd)
	}
}

func TestBuildTarCommand_AllExclusions(t *testing.T) {
	cmd := buildTarCommand([]string{"/"})
	shellCmd := cmd[2]
	for _, excl := range defaultExclusions {
		if !strings.Contains(shellCmd, "--exclude="+excl) {
			t.Errorf("missing exclusion %s in: %s", excl, shellCmd)
		}
	}
}

// --- CreateFullBackup ---

func TestCreateFullBackup_Success(t *testing.T) {
	setupTestDB(t)
	setupTestDataPath(t)

	orch := &mockOrch{
		streamFn: func(_ context.Context, _ string, _ []string, stdout io.Writer) (string, int, error) {
			// Write some fake tar data
			stdout.Write([]byte("fake tar content"))
			return "", 0, nil
		},
	}

	inst := database.Instance{Name: "test-inst", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	backupID, err := CreateFullBackup(context.Background(), orch, inst.Name, inst.ID, "test note", []string{"HOME"})
	if err != nil {
		t.Fatalf("CreateFullBackup failed: %v", err)
	}
	if backupID == 0 {
		t.Fatal("expected non-zero backup ID")
	}

	// Wait for async goroutine to complete
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, err := database.GetBackup(backupID)
		if err != nil {
			t.Fatalf("get backup: %v", err)
		}
		if b.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backup did not complete within 5 seconds")
		}
		time.Sleep(50 * time.Millisecond)
	}

	b, _ := database.GetBackup(backupID)
	if b.Status != "completed" {
		t.Errorf("expected status completed, got %s (error: %s)", b.Status, b.ErrorMessage)
	}
	if b.SizeBytes == 0 {
		t.Error("expected non-zero size")
	}
	if b.CompletedAt == nil {
		t.Error("expected completed_at to be set")
	}
	if b.Note != "test note" {
		t.Errorf("expected note 'test note', got %q", b.Note)
	}

	// Verify paths stored as JSON
	var storedPaths []string
	json.Unmarshal([]byte(b.Paths), &storedPaths)
	if len(storedPaths) != 1 || storedPaths[0] != "HOME" {
		t.Errorf("expected paths [HOME], got %v", storedPaths)
	}

	// Verify file exists
	absPath := BackupDir() + "/" + b.FilePath
	if _, err := os.Stat(absPath); err != nil {
		t.Errorf("backup file should exist: %v", err)
	}

	// Verify filename contains instance name
	if !strings.Contains(b.FilePath, "test-inst") {
		t.Errorf("filename should contain instance name, got: %s", b.FilePath)
	}
}

func TestCreateFullBackup_StreamError(t *testing.T) {
	setupTestDB(t)
	setupTestDataPath(t)

	orch := &mockOrch{
		streamFn: func(_ context.Context, _ string, _ []string, _ io.Writer) (string, int, error) {
			return "some error", 0, io.ErrClosedPipe
		},
	}

	inst := database.Instance{Name: "test-err", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	backupID, err := CreateFullBackup(context.Background(), orch, inst.Name, inst.ID, "", nil)
	if err != nil {
		t.Fatalf("CreateFullBackup should not fail synchronously: %v", err)
	}

	// Wait for async goroutine
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := database.GetBackup(backupID)
		if b.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backup did not finish within 5 seconds")
		}
		time.Sleep(50 * time.Millisecond)
	}

	b, _ := database.GetBackup(backupID)
	if b.Status != "failed" {
		t.Errorf("expected status failed, got %s", b.Status)
	}
	if b.ErrorMessage == "" {
		t.Error("expected error message to be set")
	}
}

func TestCreateFullBackup_TarExitCode2(t *testing.T) {
	setupTestDB(t)
	setupTestDataPath(t)

	orch := &mockOrch{
		streamFn: func(_ context.Context, _ string, _ []string, _ io.Writer) (string, int, error) {
			return "tar: something bad", 2, nil
		},
	}

	inst := database.Instance{Name: "test-tar2", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	backupID, _ := CreateFullBackup(context.Background(), orch, inst.Name, inst.ID, "", nil)

	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := database.GetBackup(backupID)
		if b.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for backup to finish")
		}
		time.Sleep(50 * time.Millisecond)
	}

	b, _ := database.GetBackup(backupID)
	if b.Status != "failed" {
		t.Errorf("expected failed for exit code 2, got %s", b.Status)
	}
}

func TestCreateFullBackup_TarExitCode1_Accepted(t *testing.T) {
	setupTestDB(t)
	setupTestDataPath(t)

	orch := &mockOrch{
		streamFn: func(_ context.Context, _ string, _ []string, stdout io.Writer) (string, int, error) {
			stdout.Write([]byte("some data"))
			return "file changed as we read it", 1, nil
		},
	}

	inst := database.Instance{Name: "test-tar1", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	backupID, _ := CreateFullBackup(context.Background(), orch, inst.Name, inst.ID, "", nil)

	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := database.GetBackup(backupID)
		if b.Status != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timeout")
		}
		time.Sleep(50 * time.Millisecond)
	}

	b, _ := database.GetBackup(backupID)
	if b.Status != "completed" {
		t.Errorf("exit code 1 should be accepted, got status %s (err: %s)", b.Status, b.ErrorMessage)
	}
}

func TestCreateFullBackup_DefaultPaths(t *testing.T) {
	setupTestDB(t)
	setupTestDataPath(t)

	var capturedCmd []string
	orch := &mockOrch{
		streamFn: func(_ context.Context, _ string, cmd []string, stdout io.Writer) (string, int, error) {
			capturedCmd = cmd
			stdout.Write([]byte("data"))
			return "", 0, nil
		},
	}

	inst := database.Instance{Name: "test-default", DisplayName: "Test", Status: "running"}
	database.DB.Create(&inst)

	CreateFullBackup(context.Background(), orch, inst.Name, inst.ID, "", nil)

	// Wait for goroutine to start and capture the command
	deadline := time.Now().Add(5 * time.Second)
	for capturedCmd == nil {
		if time.Now().After(deadline) {
			t.Fatal("timeout waiting for command capture")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Default paths should resolve to /home/claworc
	shellCmd := capturedCmd[2]
	if !strings.Contains(shellCmd, "/home/claworc") {
		t.Errorf("default path should be /home/claworc, got: %s", shellCmd)
	}
}
