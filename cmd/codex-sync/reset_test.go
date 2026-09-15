package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d-jiao/codex-sync/internal/config"
)

// seedConfigDir creates ~/.codex-sync with the tool's own files and one
// trashed rollout, returning the config dir and the trashed file's path.
func seedConfigDir(t *testing.T, home string) (configDir, trashed string) {
	t.Helper()
	configDir = filepath.Join(home, ".codex-sync")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	for _, f := range []string{"config.yaml", "age-key.txt", "state.json"} {
		if err := os.WriteFile(filepath.Join(configDir, f), []byte("test"), 0600); err != nil {
			t.Fatalf("Failed to create %s: %v", f, err)
		}
	}
	trashed = filepath.Join(configDir, "trash", "20260101-030000", "sessions", "2026", "01", "01", "rollout-x.jsonl")
	if err := os.MkdirAll(filepath.Dir(trashed), 0700); err != nil {
		t.Fatalf("Failed to create trash dir: %v", err)
	}
	if err := os.WriteFile(trashed, []byte(`{"type":"session_meta"}`+"\n"), 0600); err != nil {
		t.Fatalf("Failed to create trashed file: %v", err)
	}
	return configDir, trashed
}

func TestResetClearsConfigDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	configDir, _ := seedConfigDir(t, tmpDir)

	removed, err := resetLocalFiles(configDir)
	if err != nil {
		t.Fatalf("resetLocalFiles: %v", err)
	}
	if len(removed) != 3 {
		t.Errorf("removed %v, want the three files", removed)
	}
	for _, f := range []string{"config.yaml", "age-key.txt", "state.json"} {
		if _, err := os.Stat(filepath.Join(configDir, f)); !os.IsNotExist(err) {
			t.Errorf("%s should not exist after reset", f)
		}
	}
	// Missing files are not an error: a second reset is a no-op.
	if removed, err := resetLocalFiles(configDir); err != nil || len(removed) != 0 {
		t.Errorf("second reset: removed %v, err %v", removed, err)
	}
}

func TestResetKeepsTrash(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	configDir, trashed := seedConfigDir(t, tmpDir)

	if _, err := resetLocalFiles(configDir); err != nil {
		t.Fatalf("resetLocalFiles: %v", err)
	}
	if _, err := os.Stat(trashed); err != nil {
		t.Errorf("reset must not touch %s: %v", trashed, err)
	}
	if _, err := os.Stat(configDir); err != nil {
		t.Errorf("config dir should survive so the trash stays reachable: %v", err)
	}
}

func TestResetClearsStateFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create config dir and state file
	configDir := filepath.Join(tmpDir, ".codex-sync")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	statePath := filepath.Join(configDir, "state.json")
	stateContent := `{"files": {}, "device_id": "test"}`
	if err := os.WriteFile(statePath, []byte(stateContent), 0600); err != nil {
		t.Fatalf("Failed to create state file: %v", err)
	}

	// Verify state exists
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Fatal("State file should exist before reset")
	}

	// Remove state file (simulating reset --local)
	if err := os.Remove(statePath); err != nil {
		t.Fatalf("Failed to remove state file: %v", err)
	}

	// Verify state is gone
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Error("State file should not exist after reset --local")
	}
}

func TestResetPreservesBaseDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("CODEX_HOME", "")

	configDir, _ := seedConfigDir(t, tmpDir)
	baseDir := filepath.Join(tmpDir, config.DefaultBaseDirName)
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("Failed to create base dir: %v", err)
	}

	// Create a session file in base dir
	sessionFile := filepath.Join(baseDir, "session.jsonl")
	if err := os.WriteFile(sessionFile, []byte("session data"), 0644); err != nil {
		t.Fatalf("Failed to create session file: %v", err)
	}

	// Reset touches only the config dir's own files, never the Codex home
	if _, err := resetLocalFiles(configDir); err != nil {
		t.Fatalf("resetLocalFiles: %v", err)
	}

	// Verify base dir is still there
	if _, err := os.Stat(baseDir); os.IsNotExist(err) {
		t.Errorf("Base directory (%s) should be preserved after reset", config.DefaultBaseDirName)
	}

	// Verify session file is still there
	if _, err := os.Stat(sessionFile); os.IsNotExist(err) {
		t.Error("Session file should be preserved after reset")
	}
}
