package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d-jiao/codex-sync/internal/config"
)

// TestCreateBackupSetsRestrictivePermissions verifies that the backup directory
// and the files copied into it are user-only readable/writable. ~/.claude can
// contain API keys, prompts, and personal context, so backups must not be
// world-readable either.
func TestCreateBackupSetsRestrictivePermissions(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", "")

	// Populate the Codex base dir with a file inside a syncable subdirectory so that
	// createBackup also creates a nested directory we can stat.
	claudeDir := config.BaseDir()
	skillsDir := filepath.Join(claudeDir, "skills")
	if err := os.MkdirAll(skillsDir, 0700); err != nil {
		t.Fatalf("Failed to create skills dir: %v", err)
	}
	helperPath := filepath.Join(skillsDir, "helper.json")
	if err := os.WriteFile(helperPath, []byte(`{"name":"helper"}`), 0600); err != nil {
		t.Fatalf("Failed to create helper.json: %v", err)
	}

	backupDir, err := createBackup(config.SyncPaths)
	if err != nil {
		t.Fatalf("createBackup failed: %v", err)
	}

	// Backup root must be 0700.
	bi, err := os.Stat(backupDir)
	if err != nil {
		t.Fatalf("Stat backupDir failed: %v", err)
	}
	if got := bi.Mode().Perm(); got != 0700 {
		t.Errorf("Expected backup root mode 0700, got %o", got)
	}

	// Nested directory created during backup must be 0700.
	backupSkills := filepath.Join(backupDir, "skills")
	di, err := os.Stat(backupSkills)
	if err != nil {
		t.Fatalf("Stat backup skills dir failed: %v", err)
	}
	if got := di.Mode().Perm(); got != 0700 {
		t.Errorf("Expected backup nested dir mode 0700, got %o", got)
	}

	// Backed-up file must be 0600.
	backupFile := filepath.Join(backupDir, "skills", "helper.json")
	fi, err := os.Stat(backupFile)
	if err != nil {
		t.Fatalf("Stat backup file failed: %v", err)
	}
	if got := fi.Mode().Perm(); got != 0600 {
		t.Errorf("Expected backup file mode 0600, got %o", got)
	}
}
