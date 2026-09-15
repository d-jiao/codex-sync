package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/d-jiao/codex-sync/internal/storage"
	"gopkg.in/yaml.v3"
)

const (
	ConfigDir  = ".codex-sync"
	ConfigFile = "config.yaml"
	StateFile  = "state.json"
	AgeKeyFile = "age-key.txt"
	TrashDir   = "trash"

	// Sync scopes control which subset of ~/.claude is synced.
	// ScopeFull (default) syncs everything in SyncPaths; ScopeSessions limits
	// syncing to portable conversation data only.
	ScopeFull     = "full"
	ScopeSessions = "sessions"
)

type Config struct {
	// New storage configuration (preferred)
	Storage *storage.StorageConfig `yaml:"storage,omitempty"`

	// Legacy R2-only fields (for backward compatibility)
	AccountID       string `yaml:"account_id,omitempty"`
	AccessKeyID     string `yaml:"access_key_id,omitempty"`
	SecretAccessKey string `yaml:"secret_access_key,omitempty"`
	Bucket          string `yaml:"bucket,omitempty"`
	Endpoint        string `yaml:"endpoint,omitempty"`

	// Common fields
	EncryptionKey string `yaml:"encryption_key_path"`

	// Exclude patterns (glob-style) for paths to skip during sync
	Exclude []string `yaml:"exclude,omitempty"`

	// Scope selects which subset of ~/.claude to sync: "full" (default, empty)
	// or "sessions" (portable conversation data only). See ScopedSyncPaths.
	Scope string `yaml:"scope,omitempty"`

	// SyncPaths overrides the scope-based default paths when non-empty.
	// Use GetEffectiveSyncPaths() to get the actual paths to sync.
	SyncPaths []string `yaml:"sync_paths,omitempty"`

	// PathMap maps local directory prefixes to shared token names so project
	// sessions stay resumable across devices with different layouts.
	// The home directory is always mapped (token HOME); add entries here when
	// project roots differ beyond that, e.g.:
	//   path_map:
	//     ~/work: WORK        # this device keeps projects in ~/work
	// with the other device mapping its own location to the same token:
	//   path_map:
	//     ~/Projects: WORK
	PathMap map[string]string `yaml:"path_map,omitempty"`

	// BaseDirOverride overrides the resolved Codex home (for testing)
	BaseDirOverride string `yaml:"-"`

	// StateDirOverride allows overriding the state file directory (for testing)
	StateDirOverride string `yaml:"-"`
}

// SyncPaths is the "full" scope: everything under the Codex home that is
// portable user state. Rollout files (sessions/, archived_sessions/) are the
// source of truth for conversations; Codex rebuilds its SQLite indexes from
// them (spike, 2026-09-15), so no database is ever synced.
var SyncPaths = []string{
	"sessions",
	"archived_sessions",
	"session_index.jsonl",
	"history.jsonl",
	"attachments",
	"config.toml",
	"rules",
	"skills",
	"memories",
	"AGENTS.md",
}

// SessionSyncPaths is the "sessions" scope: conversation data only — the
// rollouts, their names (session_index.jsonl), prompt history and attachments.
var SessionSyncPaths = []string{
	"sessions",
	"archived_sessions",
	"session_index.jsonl",
	"history.jsonl",
	"attachments",
}

// HardExcludes always apply, even when a user lists a parent directory (or ".")
// in sync_paths. They cover identity files, every SQLite database (derived or
// machine-local), runtime state, caches, logs and scratch files. Patterns use
// the same matching rules as user excludes (see matchExcludePattern).
var HardExcludes = []string{
	// identity — also protected, see ProtectedPaths
	"auth.json", "installation_id",
	// databases
	"*.sqlite", "*.sqlite-wal", "*.sqlite-shm", "*.db", "*.db-wal", "*.db-shm", "sqlite",
	// runtime state, caches, logs
	"logs", "logs*", ".codex-global-state.json*", "..codex-global-state.json*",
	"plugins", "packages", "cache", ".tmp", "tmp", "ipc", "thread-writer-locks",
	"shell_snapshots", "models_cache.json", "computer-use", "vendor_imports", "browser",
	"node_repl", "process_manager", "dictation-history", "transcription-history.jsonl",
	"version.json", "worktrees",
	// scratch files
	"*.bak", "*.tmp-*",
}

// ProtectedPaths are never uploaded and never written by pull, regardless of
// configuration: they identify this machine's login and installation.
var ProtectedPaths = []string{"auth.json", "installation_id"}

// IsHardExcluded reports whether relPath matches a HardExcludes pattern.
func IsHardExcluded(relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	for _, pattern := range HardExcludes {
		if matchExcludePattern(pattern, relPath) {
			return true
		}
	}
	return false
}

// IsProtected reports whether relPath is one of ProtectedPaths (exact match).
func IsProtected(relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	for _, p := range ProtectedPaths {
		if relPath == p {
			return true
		}
	}
	return false
}

// ScopedSyncPaths returns the sync path set for the given scope. "sessions"
// limits syncing to SessionSyncPaths; "full", empty, or any unrecognized value
// returns the complete SyncPaths so existing configs keep their behavior.
func ScopedSyncPaths(scope string) []string {
	if scope == ScopeSessions {
		return SessionSyncPaths
	}
	return SyncPaths
}

// ErrNoHomeDir is returned when the user's home directory cannot be determined.
var ErrNoHomeDir = fmt.Errorf("could not determine home directory (is $HOME set?)")

func ConfigDirPath() string {
	path, _ := ConfigDirPathE()
	return path
}

// ConfigDirPathE returns the config directory path or an error if home dir is unavailable.
func ConfigDirPathE() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ErrNoHomeDir
	}
	return filepath.Join(home, ConfigDir), nil
}

func ConfigFilePath() string {
	return filepath.Join(ConfigDirPath(), ConfigFile)
}

func StateFilePath() string {
	return filepath.Join(ConfigDirPath(), StateFile)
}

func AgeKeyFilePath() string {
	return filepath.Join(ConfigDirPath(), AgeKeyFile)
}

// TrashDirPath is where pull moves local files that vanished from the remote
// (spec §7); nothing is ever unlinked outright.
func TrashDirPath() string {
	return filepath.Join(ConfigDirPath(), TrashDir)
}

const (
	// BaseDirEnv is Codex's own override for its home directory; codex-sync honors it.
	BaseDirEnv = "CODEX_HOME"
	// DefaultBaseDirName is the directory under $HOME that Codex uses by default.
	DefaultBaseDirName = ".codex"
)

// BaseDir returns the Codex home directory ($CODEX_HOME, else ~/.codex).
func BaseDir() string {
	path, _ := BaseDirE()
	return path
}

// BaseDirE returns the Codex home directory or an error if it cannot be
// determined. $CODEX_HOME wins when set (a leading ~ is expanded); otherwise
// ~/.codex, mirroring Codex's own resolution.
func BaseDirE() (string, error) {
	if custom := strings.TrimSpace(os.Getenv(BaseDirEnv)); custom != "" {
		if strings.HasPrefix(custom, "~") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", ErrNoHomeDir
			}
			custom = filepath.Join(home, custom[1:])
		}
		return filepath.Clean(custom), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", ErrNoHomeDir
	}
	return filepath.Join(home, DefaultBaseDirName), nil
}

func Load() (*Config, error) {
	configPath := ConfigFilePath()

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config not found: run 'codex-sync init' first")
		}
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Expand ~ in encryption key path
	if cfg.EncryptionKey != "" && cfg.EncryptionKey[0] == '~' {
		home, _ := os.UserHomeDir()
		cfg.EncryptionKey = filepath.Join(home, cfg.EncryptionKey[1:])
	}

	// Expand ~ in path_map keys
	if len(cfg.PathMap) > 0 {
		home, _ := os.UserHomeDir()
		expanded := make(map[string]string, len(cfg.PathMap))
		for p, name := range cfg.PathMap {
			if p != "" && p[0] == '~' {
				p = filepath.Join(home, p[1:])
			}
			expanded[p] = name
		}
		cfg.PathMap = expanded
	}

	// Set default endpoint for Cloudflare R2
	if cfg.Endpoint == "" && cfg.AccountID != "" {
		cfg.Endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)
	}

	return &cfg, nil
}

func Save(cfg *Config) error {
	configDir := ConfigDirPath()
	if err := os.MkdirAll(configDir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	configPath := ConfigFilePath()
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func Exists() bool {
	_, err := os.Stat(ConfigFilePath())
	return err == nil
}

// GetStorageConfig returns the storage configuration, migrating from legacy format if needed
func (c *Config) GetStorageConfig() *storage.StorageConfig {
	// If new format is already configured, use it
	if c.Storage != nil && c.Storage.Provider != "" {
		return c.Storage
	}

	// Migrate from legacy R2 format
	return &storage.StorageConfig{
		Provider:        storage.ProviderR2,
		Bucket:          c.Bucket,
		AccountID:       c.AccountID,
		AccessKeyID:     c.AccessKeyID,
		SecretAccessKey: c.SecretAccessKey,
		Endpoint:        c.Endpoint,
	}
}

// IsLegacyConfig returns true if using the legacy R2-only config format
func (c *Config) IsLegacyConfig() bool {
	return c.Storage == nil && c.AccountID != ""
}

// GetEffectiveSyncPaths returns the paths to sync: custom SyncPaths if set,
// otherwise the scope-based defaults.
//
// Scope is a ceiling, not merely a default. Under ScopeSessions the custom list
// is intersected with SessionSyncPaths so a sync_paths entry can never widen a
// sessions-scoped config back into non-portable trees like plugins/ (which
// bundles node_modules and .venv). Under "full" scope the custom list wins
// outright, since there is nothing narrower to protect.
//
// This matters because `codex-sync paths add|remove` materializes the entire
// default path list into SyncPaths, so most configs carrying a sync_paths block
// never explicitly opted into one.
//
// The result is never empty: an empty set would make DetectChanges observe zero
// local files and report every tracked file as a deletion, wiping the remote on
// the next push. If a custom list shares nothing with the scope, the scope
// defaults are used instead.
func (c *Config) GetEffectiveSyncPaths() []string {
	scoped := ScopedSyncPaths(c.Scope)
	if len(c.SyncPaths) == 0 {
		return scoped
	}

	if c.Scope != ScopeSessions {
		return c.SyncPaths
	}

	allowed := make(map[string]struct{}, len(scoped))
	for _, p := range scoped {
		allowed[p] = struct{}{}
	}

	// Preserve the user's ordering, keeping only in-scope entries.
	within := make([]string, 0, len(c.SyncPaths))
	for _, p := range c.SyncPaths {
		if _, ok := allowed[p]; ok {
			within = append(within, p)
		}
	}

	if len(within) == 0 {
		return scoped
	}
	return within
}

// IsExcluded returns true if the given relative path is hard-excluded or
// matches a user exclude pattern.
func (c *Config) IsExcluded(relPath string) bool {
	relPath = filepath.ToSlash(relPath)
	if IsHardExcluded(relPath) {
		return true
	}
	for _, pattern := range c.Exclude {
		if matchExcludePattern(filepath.ToSlash(pattern), relPath) {
			return true
		}
	}
	return false
}

// matchExcludePattern applies one exclude pattern to a slash-separated relative
// path. Patterns support:
//   - full doublestar glob syntax including ** (e.g. "**/.git/**", "plugins/cache/**")
//   - filename globs without a separator (e.g. "*.tmp" matches "foo/bar/file.tmp")
//   - plain names as a directory prefix or exact match (e.g. "plugins" matches
//     "plugins" and everything under "plugins/")
func matchExcludePattern(pattern, relPath string) bool {
	if matched, err := doublestar.Match(pattern, relPath); err == nil && matched {
		return true
	}
	hasGlob := strings.ContainsAny(pattern, "*?")
	if hasGlob && !strings.Contains(pattern, "/") {
		if matched, _ := doublestar.Match(pattern, filepath.Base(relPath)); matched {
			return true
		}
	}
	if !hasGlob {
		if relPath == pattern {
			return true
		}
		if len(relPath) > len(pattern) && strings.HasPrefix(relPath, pattern) && relPath[len(pattern)] == '/' {
			return true
		}
	}
	return false
}
