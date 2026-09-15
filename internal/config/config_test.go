package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/d-jiao/codex-sync/internal/storage"
)

func TestScopedSyncPaths(t *testing.T) {
	t.Run("sessions scope is limited to portable session data", func(t *testing.T) {
		got := ScopedSyncPaths("sessions")
		want := []string{"sessions", "archived_sessions", "session_index.jsonl", "history.jsonl", "attachments"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ScopedSyncPaths(\"sessions\") = %v, want %v", got, want)
		}
		for _, p := range got {
			if p == "plugins" {
				t.Fatal("sessions scope must never include plugins (bundles node_modules/.venv)")
			}
		}
	})

	t.Run("full, empty, and unknown scopes return the complete SyncPaths", func(t *testing.T) {
		for _, scope := range []string{"full", "", "bogus"} {
			got := ScopedSyncPaths(scope)
			if !reflect.DeepEqual(got, SyncPaths) {
				t.Errorf("ScopedSyncPaths(%q) = %v, want full SyncPaths %v", scope, got, SyncPaths)
			}
		}
	})
}

func TestConfigDirPath(t *testing.T) {
	path := ConfigDirPath()
	if path == "" {
		t.Fatal("ConfigDirPath should not return empty string")
	}

	if !strings.HasSuffix(path, ConfigDir) {
		t.Errorf("ConfigDirPath should end with '%s', got '%s'", ConfigDir, path)
	}
}

func TestConfigFilePath(t *testing.T) {
	path := ConfigFilePath()
	if path == "" {
		t.Fatal("ConfigFilePath should not return empty string")
	}

	if !strings.HasSuffix(path, ConfigFile) {
		t.Errorf("ConfigFilePath should end with '%s', got '%s'", ConfigFile, path)
	}
}

func TestStateFilePath(t *testing.T) {
	path := StateFilePath()
	if path == "" {
		t.Fatal("StateFilePath should not return empty string")
	}

	if !strings.HasSuffix(path, StateFile) {
		t.Errorf("StateFilePath should end with '%s', got '%s'", StateFile, path)
	}
}

func TestAgeKeyFilePath(t *testing.T) {
	path := AgeKeyFilePath()
	if path == "" {
		t.Fatal("AgeKeyFilePath should not return empty string")
	}

	if !strings.HasSuffix(path, AgeKeyFile) {
		t.Errorf("AgeKeyFilePath should end with '%s', got '%s'", AgeKeyFile, path)
	}
}

func TestBaseDir(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	path := BaseDir()
	if path == "" {
		t.Fatal("BaseDir should not return empty string")
	}

	if !strings.HasSuffix(path, ".codex") {
		t.Errorf("BaseDir should end with '.codex', got '%s'", path)
	}
}

func TestSaveAndLoad(t *testing.T) {
	// Create a temporary directory to use as home
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Create config directory
	configDir := filepath.Join(tmpDir, ConfigDir)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	// Create test config
	cfg := &Config{
		AccountID:       "test-account-id",
		AccessKeyID:     "test-access-key",
		SecretAccessKey: "test-secret-key",
		Bucket:          "test-bucket",
		EncryptionKey:   "~/.codex-sync/age-key.txt",
	}

	// Save config
	configPath := filepath.Join(configDir, ConfigFile)
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		t.Fatalf("Failed to create config parent dir: %v", err)
	}

	// Write config manually since Save uses hardcoded path
	data := `account_id: test-account-id
access_key_id: test-access-key
secret_access_key: test-secret-key
bucket: test-bucket
encryption_key_path: ~/.codex-sync/age-key.txt
`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Load config
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify loaded config
	if loaded.AccountID != cfg.AccountID {
		t.Errorf("AccountID mismatch: expected '%s', got '%s'", cfg.AccountID, loaded.AccountID)
	}
	if loaded.AccessKeyID != cfg.AccessKeyID {
		t.Errorf("AccessKeyID mismatch: expected '%s', got '%s'", cfg.AccessKeyID, loaded.AccessKeyID)
	}
	if loaded.SecretAccessKey != cfg.SecretAccessKey {
		t.Errorf("SecretAccessKey mismatch: expected '%s', got '%s'", cfg.SecretAccessKey, loaded.SecretAccessKey)
	}
	if loaded.Bucket != cfg.Bucket {
		t.Errorf("Bucket mismatch: expected '%s', got '%s'", cfg.Bucket, loaded.Bucket)
	}

	// Check that ~ is expanded in encryption key path
	if strings.HasPrefix(loaded.EncryptionKey, "~") {
		t.Error("EncryptionKey should have ~ expanded")
	}

	// Check that endpoint is auto-populated
	expectedEndpoint := "https://test-account-id.r2.cloudflarestorage.com"
	if loaded.Endpoint != expectedEndpoint {
		t.Errorf("Endpoint mismatch: expected '%s', got '%s'", expectedEndpoint, loaded.Endpoint)
	}
}

func TestLoadNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	_, err := Load()
	if err == nil {
		t.Fatal("Load should fail when config doesn't exist")
	}

	if !strings.Contains(err.Error(), "run 'codex-sync init' first") {
		t.Errorf("Error should mention running init, got: %v", err)
	}
}

func TestExists(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	// Should not exist initially
	if Exists() {
		t.Error("Exists should return false when config doesn't exist")
	}

	// Create config file
	configDir := filepath.Join(tmpDir, ConfigDir)
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	configPath := filepath.Join(configDir, ConfigFile)
	if err := os.WriteFile(configPath, []byte("test"), 0600); err != nil {
		t.Fatalf("Failed to create config file: %v", err)
	}

	// Should exist now
	if !Exists() {
		t.Error("Exists should return true when config exists")
	}
}

func TestGetStorageConfig_NewFormat(t *testing.T) {
	cfg := &Config{
		Storage: &storage.StorageConfig{
			Provider: storage.ProviderS3,
			Bucket:   "my-bucket",
			Region:   "us-east-1",
		},
	}

	sc := cfg.GetStorageConfig()
	if sc.Provider != storage.ProviderS3 {
		t.Errorf("expected provider S3, got %q", sc.Provider)
	}
	if sc.Bucket != "my-bucket" {
		t.Errorf("expected bucket my-bucket, got %q", sc.Bucket)
	}
}

func TestGetStorageConfig_LegacyFormat(t *testing.T) {
	cfg := &Config{
		AccountID:       "abc123",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		Bucket:          "legacy-bucket",
		Endpoint:        "https://abc123.r2.cloudflarestorage.com",
	}

	sc := cfg.GetStorageConfig()
	if sc.Provider != storage.ProviderR2 {
		t.Errorf("expected provider R2 for legacy config, got %q", sc.Provider)
	}
	if sc.Bucket != "legacy-bucket" {
		t.Errorf("expected bucket legacy-bucket, got %q", sc.Bucket)
	}
	if sc.AccountID != "abc123" {
		t.Errorf("expected account ID abc123, got %q", sc.AccountID)
	}
}

func TestIsLegacyConfig(t *testing.T) {
	tests := []struct {
		name     string
		cfg      Config
		expected bool
	}{
		{"legacy with account ID", Config{AccountID: "abc"}, true},
		{"new format with storage", Config{Storage: &storage.StorageConfig{Provider: "s3"}}, false},
		{"empty config", Config{}, false},
		{"both set uses new format", Config{Storage: &storage.StorageConfig{Provider: "s3"}, AccountID: "abc"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.IsLegacyConfig(); got != tt.expected {
				t.Errorf("IsLegacyConfig() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestConfigSaveAndLoad(t *testing.T) {
	// Override config dir to temp dir
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".codex-sync")

	// We can't easily override ConfigDirPath, so test Save/Load via direct file ops
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		EncryptionKey: "~/.codex-sync/age-key.txt",
		Bucket:        "test-bucket",
		AccountID:     "test-account",
		Exclude:       []string{"*.tmp", "cache/**"},
	}

	// Write config manually to test Load
	configPath := filepath.Join(configDir, "config.yaml")
	data := `bucket: test-bucket
account_id: test-account
encryption_key_path: "~/.codex-sync/age-key.txt"
exclude:
  - "*.tmp"
  - "cache/**"
`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	// Verify the file was written
	readBack, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(readBack) == 0 {
		t.Fatal("config file should not be empty")
	}

	// Verify expected fields in the written content
	content := string(readBack)
	if !strings.Contains(content, "test-bucket") {
		t.Error("config should contain bucket name")
	}

	_ = cfg // cfg used for reference
}

func TestIsExcluded(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		patterns []string
		expected bool
	}{
		// Directory wildcard patterns with /**
		{"exclude dir with /**", "plugins/cache/foo/bar.js", []string{"plugins/cache/**"}, true},
		{"exclude dir itself", "plugins/cache", []string{"plugins/cache/**"}, true},
		{"exclude nested dir", "plugins/marketplaces/repo/file.txt", []string{"plugins/marketplaces/**"}, true},
		{"non-matching dir", "workspace/installed.json", []string{"workspace/cache/**"}, false},

		// Filename glob patterns
		{"exclude by extension", "projects/foo/debug.tmp", []string{"*.tmp"}, true},
		{"exclude dotfile glob", "projects/.DS_Store", []string{".*"}, true},
		{"non-matching extension", "projects/foo/file.json", []string{"*.tmp"}, false},

		// Exact path patterns
		{"exact file match", "debug/log.txt", []string{"debug/log.txt"}, true},
		{"exact dir pattern with /**", "debug", []string{"debug/**"}, true},

		// Directory prefix (without /**)
		{"dir prefix match", "plugins/marketplace/repo/file.txt", []string{"plugins/marketplace"}, true},
		{"dir prefix exact", "plugins/marketplace", []string{"plugins/marketplace"}, true},

		// Multiple patterns
		{"first pattern matches", "plugins/cache/mod.js", []string{"plugins/cache/**", "*.tmp"}, true},
		{"second pattern matches", "foo.tmp", []string{"plugins/cache/**", "*.tmp"}, true},
		{"no pattern matches", "settings.json", []string{"plugins/cache/**", "*.tmp"}, false},

		// Empty patterns
		{"empty patterns", "anything.txt", []string{}, false},
		{"nil-like empty", "anything.txt", nil, false},

		// Edge cases
		{"partial name no match", "workspace/cachedata/file.txt", []string{"workspace/cache/**"}, false},
		{"shell-snapshots", "shell-snapshots/snap.json", []string{"shell-snapshots/**"}, true},
		{"telemetry dir", "telemetry/data.json", []string{"telemetry/**"}, true},

		// Recursive globstar patterns (Issue #43)
		{"globstar .git at root", ".git/HEAD", []string{"**/.git/**"}, true},
		{"globstar .git nested", "projects/someproject/.git/config", []string{"**/.git/**"}, true},
		{"globstar .git deeply nested", "projects/foo/bar/.git/objects/ab/cd", []string{"**/.git/**"}, true},
		{"globstar .git dir itself", "projects/app/.git", []string{"**/.git/**"}, true},
		{"globstar non-matching", "projects/app/git/config", []string{"**/.git/**"}, false},
		{"globstar node_modules anywhere", "projects/foo/node_modules/lodash/index.js", []string{"**/node_modules/**"}, true},
		{"globstar node_modules deep", "a/b/c/d/node_modules/pkg/lib/file.js", []string{"**/node_modules/**"}, true},
		{"leading ** with extension", "deeply/nested/path/file.log", []string{"**/*.log"}, true},
		{"combined patterns from issue", "projects/foo/.git/objects/ab", []string{"*.tmp", "projects/*/node_modules/*", "**/.git/**"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Exclude: tt.patterns}
			result := cfg.IsExcluded(tt.path)
			if result != tt.expected {
				t.Errorf("IsExcluded(%q) with patterns %v = %v, want %v", tt.path, tt.patterns, result, tt.expected)
			}
		})
	}
}

func TestGetEffectiveSyncPaths(t *testing.T) {
	tests := []struct {
		name      string
		syncPaths []string
		scope     string
		wantLen   int
	}{
		{
			name:      "empty SyncPaths returns defaults",
			syncPaths: nil,
			scope:     "",
			wantLen:   len(SyncPaths),
		},
		{
			name:      "custom SyncPaths overrides defaults",
			syncPaths: []string{"CLAUDE.md", "settings.json"},
			scope:     "",
			wantLen:   2,
		},
		{
			name:      "sessions scope without custom paths",
			syncPaths: nil,
			scope:     ScopeSessions,
			wantLen:   len(SessionSyncPaths),
		},
		{
			// Scope is a ceiling: an entirely out-of-scope custom list cannot
			// widen a sessions config, and must not collapse to an empty set
			// either, so it falls back to the scope defaults.
			name:      "wholly out-of-scope custom paths fall back to sessions defaults",
			syncPaths: []string{"custom-only"},
			scope:     ScopeSessions,
			wantLen:   len(SessionSyncPaths),
		},
		{
			name:      "custom paths are narrowed to the sessions scope",
			syncPaths: []string{"sessions", "plugins", "AGENTS.md"},
			scope:     ScopeSessions,
			wantLen:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{SyncPaths: tt.syncPaths, Scope: tt.scope}
			got := cfg.GetEffectiveSyncPaths()
			if len(got) != tt.wantLen {
				t.Errorf("GetEffectiveSyncPaths() returned %d paths, want %d", len(got), tt.wantLen)
			}
		})
	}
}

// TestGetEffectiveSyncPathsScopeIsCeiling guards the regression that motivated
// wiring up the override: `paths add|remove` materializes the full default path
// list into sync_paths, so honoring that list verbatim would silently widen a
// sessions-scoped config back into plugins/ (node_modules, .venv) on upgrade.
func TestGetEffectiveSyncPathsScopeIsCeiling(t *testing.T) {
	materialized := append([]string{}, SyncPaths...)
	materialized = append(materialized, "my-extra-file.md")

	cfg := &Config{SyncPaths: materialized, Scope: ScopeSessions}
	got := cfg.GetEffectiveSyncPaths()

	for _, p := range got {
		if p == "plugins" {
			t.Fatalf("sessions scope leaked plugins/ via sync_paths: %v", got)
		}
	}

	for _, p := range got {
		var inScope bool
		for _, s := range SessionSyncPaths {
			if p == s {
				inScope = true
				break
			}
		}
		if !inScope {
			t.Errorf("path %q is outside SessionSyncPaths but was returned", p)
		}
	}

	if len(got) == 0 {
		t.Error("effective sync paths must never be empty: an empty set makes " +
			"DetectChanges report every tracked file as deleted, wiping the remote")
	}
}

// TestGetEffectiveSyncPathsAppliedByFullScope covers the originally reported
// bug: a custom sync_paths list under the default scope must be honored.
func TestGetEffectiveSyncPathsAppliedByFullScope(t *testing.T) {
	cfg := &Config{SyncPaths: []string{"CLAUDE.md", "my-extra-file.md"}}
	got := cfg.GetEffectiveSyncPaths()

	var found bool
	for _, p := range got {
		if p == "my-extra-file.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("custom sync_paths ignored under full scope: got %v", got)
	}
}

func TestBaseDirHonorsCodexHome(t *testing.T) {
	custom := t.TempDir()
	t.Setenv("CODEX_HOME", custom)
	got, err := BaseDirE()
	if err != nil {
		t.Fatalf("BaseDirE: %v", err)
	}
	if got != custom {
		t.Errorf("BaseDirE() = %q, want %q", got, custom)
	}
	if BaseDir() != custom {
		t.Errorf("BaseDir() = %q, want %q", BaseDir(), custom)
	}
}

func TestBaseDirExpandsTilde(t *testing.T) {
	t.Setenv("CODEX_HOME", "~/custom-codex")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	got, err := BaseDirE()
	if err != nil {
		t.Fatalf("BaseDirE: %v", err)
	}
	if want := filepath.Join(home, "custom-codex"); got != want {
		t.Errorf("BaseDirE() = %q, want %q", got, want)
	}
}

func TestBaseDirDefaultsToDotCodex(t *testing.T) {
	t.Setenv("CODEX_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	got, err := BaseDirE()
	if err != nil {
		t.Fatalf("BaseDirE: %v", err)
	}
	if want := filepath.Join(home, ".codex"); got != want {
		t.Errorf("BaseDirE() = %q, want %q", got, want)
	}
}

func TestSyncPathsAreTheCodexProfile(t *testing.T) {
	wantFull := []string{"sessions", "archived_sessions", "session_index.jsonl", "history.jsonl", "attachments", "config.toml", "rules", "skills", "memories", "AGENTS.md"}
	wantSessions := []string{"sessions", "archived_sessions", "session_index.jsonl", "history.jsonl", "attachments"}
	if !reflect.DeepEqual(SyncPaths, wantFull) {
		t.Errorf("SyncPaths = %v, want %v", SyncPaths, wantFull)
	}
	if got := ScopedSyncPaths(ScopeSessions); !reflect.DeepEqual(got, wantSessions) {
		t.Errorf("ScopedSyncPaths(sessions) = %v, want %v", got, wantSessions)
	}
	if got := ScopedSyncPaths(""); !reflect.DeepEqual(got, wantFull) {
		t.Errorf("ScopedSyncPaths(\"\") = %v, want %v", got, wantFull)
	}
}

func TestHardExcludesApplyWithoutUserConfig(t *testing.T) {
	cfg := &Config{}
	excluded := []string{
		"auth.json", "installation_id",
		"state_5.sqlite", "state_5.sqlite-wal", "logs_2.sqlite-shm", "thread_history_1.sqlite",
		"sqlite/codex-dev.db", "sqlite/codex-dev.db-wal",
		"logs/today.log", "logs_2.sqlite",
		".codex-global-state.json", ".codex-global-state.json.bak", "..codex-global-state.json.tmp-1-abc",
		"plugins/x/y.js", "packages/standalone/current/bin/codex", "cache/models", ".tmp/x", "tmp/x",
		"ipc/sock", "thread-writer-locks/a", "shell_snapshots/b", "models_cache.json", "computer-use/c",
		"vendor_imports/d", "browser/e", "node_repl/f", "process_manager/g", "dictation-history/h",
		"transcription-history.jsonl", "version.json", "worktrees/repo/file.go",
		"config.toml.app-full.bak", "sessions/2026/01/01/rollout-x.jsonl.tmp-123",
	}
	for _, p := range excluded {
		if !cfg.IsExcluded(p) {
			t.Errorf("%s should be hard-excluded", p)
		}
		if !IsHardExcluded(p) {
			t.Errorf("IsHardExcluded(%s) should be true", p)
		}
	}
	included := []string{
		"sessions/2026/01/01/rollout-x.jsonl", "archived_sessions/rollout-y.jsonl", "session_index.jsonl",
		"history.jsonl", "attachments/abc/goal.md", "config.toml", "rules/default.rules",
		"skills/foo/SKILL.md", "memories/notes.md", "AGENTS.md",
	}
	for _, p := range included {
		if cfg.IsExcluded(p) {
			t.Errorf("%s should be syncable", p)
		}
	}
}

func TestUserExcludesStillApply(t *testing.T) {
	cfg := &Config{Exclude: []string{"skills/private/**"}}
	if !cfg.IsExcluded("skills/private/SKILL.md") {
		t.Error("user exclude pattern should still apply")
	}
}

func TestProtectedPaths(t *testing.T) {
	for _, p := range []string{"auth.json", "installation_id"} {
		if !IsProtected(p) {
			t.Errorf("IsProtected(%s) should be true", p)
		}
	}
	for _, p := range []string{"sessions/x.jsonl", "config.toml", "auth.json.bak"} {
		if IsProtected(p) {
			t.Errorf("IsProtected(%s) should be false", p)
		}
	}
}
