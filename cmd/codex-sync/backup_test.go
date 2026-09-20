package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d-jiao/codex-sync/internal/config"
)

// TestCreateBackupSetsRestrictivePermissions verifies that the backup directory
// and the files copied into it are user-only readable/writable. ~/.codex can
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

	backupDir, err := createBackup(config.SyncPaths, nil)
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

// TestCreateBackupHonorsExcludes covers the first-pull backup of a Codex home
// whose sync_paths include the base directory itself. Identity files and
// databases must not be copied: they are never synced, they are large and
// machine-local, and auth.json is a credential.
func TestCreateBackupHonorsExcludes(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CODEX_HOME", "")

	baseDir := config.BaseDir()
	if err := os.MkdirAll(filepath.Join(baseDir, "skills"), 0700); err != nil {
		t.Fatalf("Failed to create skills dir: %v", err)
	}
	for name, content := range map[string]string{
		"auth.json":          `{"token":"secret"}`,
		"state.sqlite":       "binary",
		"scratch.tmp":        "junk",
		"skills/helper.json": `{"name":"helper"}`,
		"AGENTS.md":          "# agents",
	} {
		if err := os.WriteFile(filepath.Join(baseDir, name), []byte(content), 0600); err != nil {
			t.Fatalf("Failed to write %s: %v", name, err)
		}
	}

	cfg := &config.Config{Exclude: []string{"*.tmp"}}
	backupDir, err := createBackup([]string{"."}, cfg.IsExcluded)
	if err != nil {
		t.Fatalf("createBackup failed: %v", err)
	}

	for _, rel := range []string{"skills/helper.json", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(backupDir, rel)); err != nil {
			t.Errorf("expected %s in the backup: %v", rel, err)
		}
	}
	for _, rel := range []string{"auth.json", "state.sqlite", "scratch.tmp"} {
		if _, err := os.Stat(filepath.Join(backupDir, rel)); !os.IsNotExist(err) {
			t.Errorf("%s must not be backed up (err = %v)", rel, err)
		}
	}
}
