# codex-sync v1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the claude-sync fork into codex-sync: a CLI that pushes/pulls the file-based state of OpenAI Codex (`$CODEX_HOME`, default `~/.codex`) between machines through encrypted object storage, with a daily launchd schedule.

**Architecture:** The claude-sync engine (storage adapters, age encryption, per-file state, push/pull, conflict sidecars, `${HOME}` path tokens) is kept. Codex-specific behavior is added in four places: the base directory and sync profile (`internal/config`), portability rules for Codex files (`internal/sync/paths.go`), union merges for two shared index files (`internal/sync/merge.go`), and pull-side removal to a trash directory (`internal/sync/sync.go`). Claude-only modules (MCP merge, Claude Code hooks, legacy key migration, history rebuild) are deleted.

**Tech Stack:** Go (version pinned in `go.mod`: 1.24), cobra + survey CLI, `filippo.io/age`, `doublestar` globs, Go `testing` with the in-memory `mockStorage` in `internal/sync/sync_push_pull_test.go`; macOS launchd for scheduling; Python 3 for the manual acceptance script.

**Spec:** `docs/specs/2026-09-15-codex-sync-design.md`

## Global Constraints

- Go toolchain as pinned in `go.mod` (`go 1.24.0`); no new dependencies unless a task says so (none do).
- Before every commit: `make check` (gofmt, go vet, `go test ./... -short`) must pass; the pre-commit hook runs the same.
- CI floor: 60% coverage on `internal/*` packages (`.github/workflows/ci.yml`). Every new engine behavior ships with tests.
- Commit messages: Conventional Commits (`feat`, `fix`, `refactor`, `docs`, `test`, `chore`), subject ≤ 72 chars, body explains why, last line `Co-Authored-By: Claude <noreply@anthropic.com>`.
- Nothing machine-specific in the repository: no usernames, hostnames, bucket names, or absolute paths from any real machine. Tests use `t.TempDir()`.
- The Argon2 salt string in `internal/crypto/encrypt.go` is `codex-sync-v1` and must never change (spec §3).
- `auth.json` and `installation_id` are never uploaded and never written by pull (spec §5).
- Sync set, excludes, merge and removal semantics exactly as in spec §5–§7.
- Work on `main` (single developer, no PRs); push when a task's commit is green.

---

## File map

| Path | Action | Responsibility |
|---|---|---|
| `internal/config/config.go` | modify | base dir (`BaseDir`, `CODEX_HOME`), Codex sync profile, hard excludes, protected paths, trash dir path; drop MCP fields |
| `internal/config/config_test.go` | modify | tests for the above |
| `internal/sync/paths.go` | modify | `IsPortableContentPath` for Codex roots and `.toml` |
| `internal/sync/paths_test.go` | modify | portability table test |
| `internal/sync/merge.go` | create | `IsMergeablePath`, `MergeJSONL`, `hashBytes`, `writeFileAtomic` |
| `internal/sync/merge_test.go` | create | merge semantics |
| `internal/sync/sync.go` | modify | `fetchRemote`/`localFilePath` extraction, merge hook, pull-side removal, trash, `SetNoDelete`, result/preview fields |
| `internal/sync/profile_test.go` | create | protected/hard-exclude engine tests, two-device helper `newPeer` |
| `internal/sync/pull_merge_test.go` | create | merge-on-pull tests |
| `internal/sync/pull_remove_test.go` | create | removal/trash tests |
| `internal/sync/state.go` | modify | drop `MCPBaseline` |
| `internal/sync/history.go`, `history_test.go`, `mcp.go`, `mcp_test.go`, `migrate.go`, `migrate_test.go` | delete | Claude-only |
| `internal/claudesettings/` | delete | Claude Code hooks |
| `cmd/codex-sync/main.go` | modify | command surface, flags, wording, release URLs, scope default |
| `cmd/codex-sync/release_test.go` | create | release URL test |
| `scripts/launchd/com.codex-sync.daily.plist.template` | create | daily agent |
| `Makefile` | modify | `install`, `install-launchd`, `uninstall-launchd` |
| `integration/codex_listing_check.py` | create | manual acceptance: two homes list the same threads |
| `README.md`, `CLAUDE.md`, `SECURITY-AUDIT.md`, `CHANGELOG.md`, `AGENTS.md` | modify | docs |

Engine field naming: the `Syncer.claudeDir` field and the `claudeDir` parameters in `GetLocalFiles`/`DetectChanges` keep their names (internal; renaming adds churn without value). Only the public config helpers are renamed.

---

### Task 1: Base directory resolved through `CODEX_HOME`

**Files:**
- Modify: `internal/config/config.go:150-159` (replace `ClaudeDir`/`ClaudeDirE`), `internal/config/config.go:72-73` (rename `ClaudeDirOverride`)
- Modify: `internal/sync/sync.go:113-117` (call site in `NewSyncer`)
- Modify: `cmd/codex-sync/main.go` (8 call sites, found by grep)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `config.BaseDir() string`, `config.BaseDirE() (string, error)`, `config.BaseDirEnv = "CODEX_HOME"`, `config.DefaultBaseDirName = ".codex"`, field `Config.BaseDirOverride string`.

- [ ] **Step 1: Write the failing tests** — append to `internal/config/config_test.go` (add `"os"` and `"path/filepath"` to its imports if missing):

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/ -run 'TestBaseDir' -v`
Expected: build failure `undefined: BaseDirE`.

- [ ] **Step 3: Implement** — in `internal/config/config.go` replace the `ClaudeDir` and `ClaudeDirE` functions (lines 150–159) with:

```go
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
```

Then rename the override field and every call site mechanically:

```bash
grep -rlE 'ClaudeDirOverride|ClaudeDirE\(\)|ClaudeDir\(\)' --include='*.go' . | xargs sed -i '' -e 's/ClaudeDirOverride/BaseDirOverride/g' -e 's/config\.ClaudeDirE()/config.BaseDirE()/g' -e 's/config\.ClaudeDir()/config.BaseDir()/g'
```

Update the field comment in `Config` to `// BaseDirOverride overrides the resolved Codex home (for testing)`.

- [ ] **Step 4: Run tests and build**

Run: `go build ./... && go test ./internal/config/ ./internal/sync/ ./cmd/... -short`
Expected: PASS; `grep -rn 'ClaudeDir' --include='*.go' .` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor(config): resolve the Codex base directory via CODEX_HOME" -m "Codex honors CODEX_HOME; codex-sync now follows the same rule and defaults to ~/.codex." -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: Remove Claude-only modules (MCP merge, Claude Code hooks, legacy migrate, history rebuild)

**Files:**
- Delete: `internal/sync/mcp.go`, `internal/sync/mcp_test.go`, `internal/sync/history.go`, `internal/sync/history_test.go`, `internal/sync/migrate.go`, `internal/sync/migrate_test.go` (if present), `internal/claudesettings/` (whole directory)
- Modify: `cmd/codex-sync/main.go` (root command list lines 63–78; delete `rebuildHistoryCmd`, `runHistoryRebuild`, `migrateCmd`, `mcpCmd` and every `mcp*Cmd`, `runMCPPush`, `runMCPPull`, `autoCmd`, `autoEnableCmd`, `autoDisableCmd`, `autoStatusCmd`; delete the `--include-mcp` flags in `pushCmd`/`pullCmd` and the `--rebuild-history` flag in `pullCmd` together with their `includeMCP`/`rebuildHistory` variables and the code blocks at `main.go:1244-1256`)
- Modify: `internal/config/config.go` (delete `MCPRemoteKey`, the `MCPSync` field, `IsMCPSyncEnabled`, `SetMCPSync`, `ClaudeJSONPath`, `ClaudeJSONOverride`)
- Modify: `internal/sync/state.go` (delete the `MCPBaseline` field, `GetMCPBaseline`, `SetMCPBaseline`)
- Modify: `internal/sync/sync.go` (delete `PushMCP`, `PullMCP`, `MCPPushResult`, `MCPPullResult` and helpers they alone use, from line 872 to the end of that block)
- Modify: `internal/sync/sync.go:646-649` — keep the `_external/` skip in `buildRemoteMap`, change its comment to `// Skip legacy external objects (claude-sync MCP sync); never mapped to local files`.

**Interfaces:**
- Produces: nothing new. Removes `rebuild-history`, `migrate`, `mcp`, `auto` commands and the `--include-mcp`, `--rebuild-history` flags.

- [ ] **Step 1: Delete the files**

```bash
git rm -rq internal/sync/mcp.go internal/sync/mcp_test.go internal/sync/history.go internal/sync/history_test.go internal/sync/migrate.go internal/claudesettings
git rm -q internal/sync/migrate_test.go 2>/dev/null || true
```

- [ ] **Step 2: Remove the symbols listed above from `main.go`, `config.go`, `state.go`, `sync.go`**

Locate each with `grep -n 'func mcp\|func auto\|func migrateCmd\|func rebuildHistoryCmd\|func runHistoryRebuild\|func runMCP' cmd/codex-sync/main.go` and delete the whole function bodies. Remove the four entries `rebuildHistoryCmd(),`, `migrateCmd(),`, `mcpCmd(),`, `autoCmd(),` from `rootCmd.AddCommand(...)`.

- [ ] **Step 3: Let the compiler find leftovers**

Run: `go build ./... 2>&1 | head -40`
Every error names a remaining reference to a deleted symbol (an unused import such as `encoding/json` in `state.go`, a variable such as `includeMCP`, a test in `state_test.go` or `cmd/codex-sync/*_test.go` calling `SetMCPBaseline`). Delete each reference and rerun until the build is clean. Then:

Run: `go vet ./... && go test ./... -short`
Expected: PASS.

- [ ] **Step 4: Verify the command surface**

Run: `go run ./cmd/codex-sync --help | grep -Ei 'mcp|auto|migrate|rebuild-history'`
Expected: no output. And `grep -rniE 'claudesettings|MCPBaseline|mcp_sync|rebuildHistory' --include='*.go' .` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "refactor: remove Claude-specific MCP, hooks, migrate and history-rebuild code" -m "Codex keeps MCP servers in config.toml (synced as a file), has no settings.json hooks, and its history is merged rather than rebuilt (spec §6, §10)." -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: Codex sync profile, hard excludes, protected paths

**Files:**
- Modify: `internal/config/config.go:81-118` (`SyncPaths`, `SessionSyncPaths`, add `HardExcludes`, `ProtectedPaths`, `IsHardExcluded`, `IsProtected`, refactor `IsExcluded` at `config.go:316-362`)
- Modify: `internal/sync/sync.go` `downloadFile` (protected guard)
- Test: `internal/config/config_test.go`, create `internal/sync/profile_test.go`
- Modify: existing sync tests whose fixtures use Claude paths (see Step 6)

**Interfaces:**
- Produces: `config.SyncPaths`, `config.SessionSyncPaths` (Codex lists), `config.HardExcludes []string`, `config.ProtectedPaths []string`, `config.IsHardExcluded(relPath string) bool`, `config.IsProtected(relPath string) bool`, `matchExcludePattern(pattern, relPath string) bool` (package-private). `(*Config).IsExcluded` now returns true for hard excludes before consulting user patterns.
- Consumes: nothing from earlier tasks.

- [ ] **Step 1: Write the failing config tests** — append to `internal/config/config_test.go` (import `"reflect"`):

```go
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
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/config/ -run 'TestSyncPathsAreTheCodexProfile|TestHardExcludes|TestUserExcludes|TestProtectedPaths' -v`
Expected: FAIL (`undefined: IsHardExcluded`, list mismatch).

- [ ] **Step 3: Implement the profile** — in `internal/config/config.go` replace the `SyncPaths` and `SessionSyncPaths` declarations (with their comments) by:

```go
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
```

Then refactor `IsExcluded` (bottom of the file) into a hard-exclude check plus a per-pattern helper:

```go
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
```

- [ ] **Step 4: Run the config tests**

Run: `go test ./internal/config/ -v -run 'TestSyncPathsAreTheCodexProfile|TestHardExcludes|TestUserExcludes|TestProtectedPaths|TestIsExcluded'`
Expected: PASS (existing `IsExcluded` tests keep passing — the helper preserves their rules).

- [ ] **Step 5: Write the failing engine tests** — create `internal/sync/profile_test.go`:

```go
package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-jiao/codex-sync/internal/config"
)

// seedRemote plants an already-encrypted object so pull tests can start from a
// remote state without pushing from a peer.
func seedRemote(t *testing.T, env *testEnv, relPath, content string) {
	t.Helper()
	compressed, err := gzipCompress([]byte(content))
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	encrypted, err := env.syncer.encryptor.Encrypt(compressed)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if err := env.store.Upload(context.Background(), env.syncer.remoteKey(relPath), encrypted); err != nil {
		t.Fatalf("seed upload: %v", err)
	}
}

// newPeer creates a second device: its own base dir and state, sharing env's
// remote store and encryption key.
func newPeer(t *testing.T, env *testEnv) *testEnv {
	t.Helper()
	tmp := t.TempDir()
	base := filepath.Join(tmp, ".codex")
	stateDir := filepath.Join(tmp, ".codex-sync")
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	state, err := LoadStateFromDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	syncer := &Syncer{
		storage:   env.store,
		encryptor: env.syncer.encryptor,
		state:     state,
		claudeDir: base,
		quiet:     true,
		cfg:       &config.Config{},
	}
	return &testEnv{syncer: syncer, store: env.store, claudeDir: base, stateDir: stateDir}
}

func TestPushNeverUploadsProtectedOrHardExcludedFiles(t *testing.T) {
	env := setupTestEnv(t)
	env.syncer.cfg.SyncPaths = []string{"."} // the widest configuration a user can write
	writeFile(t, env.claudeDir, "auth.json", `{"token":"secret"}`)
	writeFile(t, env.claudeDir, "installation_id", "abc")
	writeFile(t, env.claudeDir, "state_5.sqlite", "binary")
	writeFile(t, env.claudeDir, "sessions/2026/01/01/rollout-a.jsonl", `{"type":"session_meta"}`+"\n")

	if _, err := env.syncer.Push(context.Background()); err != nil {
		t.Fatalf("push: %v", err)
	}
	for key := range env.store.objects {
		for _, banned := range []string{"auth.json", "installation_id", "state_5.sqlite"} {
			if strings.Contains(key, banned) {
				t.Errorf("uploaded a file that must never leave the machine: %s", key)
			}
		}
	}
	if _, ok := env.store.objects[env.syncer.remoteKey("sessions/2026/01/01/rollout-a.jsonl")]; !ok {
		t.Error("rollout should have been uploaded")
	}
}

func TestPullNeverWritesProtectedFiles(t *testing.T) {
	env := setupTestEnv(t)
	seedRemote(t, env, "auth.json", `{"token":"remote"}`)
	seedRemote(t, env, "sessions/2026/01/01/rollout-a.jsonl", "line\n")

	if _, err := env.syncer.Pull(context.Background()); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.claudeDir, "auth.json")); !os.IsNotExist(err) {
		t.Error("pull must never write auth.json")
	}
	if _, err := os.Stat(filepath.Join(env.claudeDir, "sessions/2026/01/01/rollout-a.jsonl")); err != nil {
		t.Errorf("rollout should have been pulled: %v", err)
	}
}

func TestDownloadFileRefusesProtectedPath(t *testing.T) {
	env := setupTestEnv(t)
	seedRemote(t, env, "auth.json", `{"token":"remote"}`)
	err := env.syncer.downloadFile(context.Background(), "auth.json", env.syncer.remoteKey("auth.json"), nil)
	if err == nil {
		t.Fatal("downloadFile must refuse protected paths")
	}
}
```

- [ ] **Step 6: Migrate existing test fixtures from Claude paths to Codex paths**

The default profile no longer includes `projects/`, `plans/`, `tasks/`, `CLAUDE.md`, `settings.json`, so tests that write those fixtures and expect them synced will fail. Apply this mapping in `internal/sync/sync_push_pull_test.go`, `sync_test.go`, `syncer_test.go`, `state_test.go`, `syncpaths_test.go` and `cmd/codex-sync/*_test.go` (not in `paths_test.go` — its `projects/` cases exercise key rewriting, which is unchanged):

| Old fixture | New fixture |
|---|---|
| `projects/<x>/<name>.jsonl` | `sessions/2026/01/01/<name>.jsonl` |
| `plans/<name>` | `rules/<name>` |
| `tasks/<name>` | `skills/<name>` |
| `CLAUDE.md` | `AGENTS.md` |
| `settings.json`, `settings.local.json` | `config.toml` |
| `plugins/...` (used as an excluded example) | keep — still hard-excluded |

Run: `go test ./... -short 2>&1 | grep -E '^(--- FAIL|FAIL|ok)'` and fix each failing assertion by the table above until green.

- [ ] **Step 7: Add the protected guard to `downloadFile`** — in `internal/sync/sync.go`, as the first statement of `downloadFile`:

```go
	if config.IsProtected(relativePath) {
		return fmt.Errorf("refusing to write protected file %s", relativePath)
	}
```

- [ ] **Step 8: Run everything**

Run: `make check`
Expected: `All checks passed!`

- [ ] **Step 9: Commit**

```bash
git add -A && git commit -m "feat(config): define the Codex sync profile with hard excludes and protected paths" -m "Files only — every SQLite database is derived or machine-local; auth.json and installation_id never leave or enter the machine (spec §5)." -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: Portability rules for Codex files

**Files:**
- Modify: `internal/sync/paths.go:196-214` (`IsPortableContentPath`)
- Test: `internal/sync/paths_test.go`

**Interfaces:**
- Produces: `IsPortableContentPath(relPath string) bool` with Codex semantics (spec §6). Signature unchanged; callers in `uploadFile`/`downloadFile` need no edits.

- [ ] **Step 1: Replace the existing `IsPortableContentPath` test** (find it with `grep -n IsPortableContentPath internal/sync/paths_test.go`; delete that test function) and add:

```go
func TestIsPortableContentPathCodex(t *testing.T) {
	cases := map[string]bool{
		"history.jsonl":                               true,
		"session_index.jsonl":                         true,
		"config.toml":                                 true,
		"AGENTS.md":                                   true,
		"sessions/2026/01/01/rollout-a.jsonl":         true,
		"sessions/2026/01/01/rollout-a.jsonl.conflict.20260101-000000": true,
		"archived_sessions/rollout-b.jsonl":           true,
		"rules/default.rules":                         false,
		"rules/notes.md":                              true,
		"skills/foo/SKILL.md":                         true,
		"skills/foo/tool.py":                          false,
		"skills/foo/config.toml":                      true,
		"memories/notes.txt":                          true,
		"attachments/abc/goal.md":                     false,
		"attachments/pasted-text-attachments.json":    false,
		"projects/-Users-alice-app/session.jsonl":     false,
	}
	for relPath, want := range cases {
		if got := IsPortableContentPath(relPath); got != want {
			t.Errorf("IsPortableContentPath(%q) = %v, want %v", relPath, got, want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sync/ -run TestIsPortableContentPathCodex -v`
Expected: FAIL on `config.toml`, `session_index.jsonl`, `sessions/...`.

- [ ] **Step 3: Implement** — replace the function in `internal/sync/paths.go`:

```go
// portableContentRoots are directories whose text files may embed absolute
// paths (rollout cwd fields, tool output, skill docs).
var portableContentRoots = []string{"sessions/", "archived_sessions/", "rules/", "skills/", "memories/"}

// portableContentFiles are single files that embed absolute paths
// (config.toml project trust keys, history and index entries).
var portableContentFiles = map[string]bool{
	"history.jsonl":       true,
	"session_index.jsonl": true,
	"config.toml":         true,
	"AGENTS.md":           true,
}

// IsPortableContentPath reports whether content path translation applies to
// this relative path. Conflict copies (path.conflict.<timestamp>) inherit the
// base path's rule. Attachments are user files and are copied verbatim.
func IsPortableContentPath(relPath string) bool {
	if i := strings.Index(relPath, ".conflict."); i >= 0 {
		relPath = relPath[:i]
	}
	if portableContentFiles[relPath] {
		return true
	}
	for _, root := range portableContentRoots {
		if strings.HasPrefix(relPath, root) {
			switch path.Ext(relPath) {
			case ".jsonl", ".json", ".md", ".txt", ".toml":
				return true
			}
			return false
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/sync/ -short`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(sync): apply path portability to Codex rollouts and config" -m "Rollouts, the index files, config.toml and skill/rule docs get \${HOME} tokenized; attachments and scripts are copied verbatim (spec §6)." -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: Union merges for `session_index.jsonl` and `history.jsonl`

**Files:**
- Create: `internal/sync/merge.go`
- Test: create `internal/sync/merge_test.go`

**Interfaces:**
- Produces: `IsMergeablePath(relPath string) bool`; `MergeJSONL(relPath string, local, remote []byte) ([]byte, error)`; `hashBytes(data []byte) string` (same hex-SHA256 format as `HashFile`); `writeFileAtomic(path string, data []byte, perm os.FileMode) error`; constants `SessionIndexFile = "session_index.jsonl"`, `HistoryFile = "history.jsonl"`.
- Consumed by Task 6 and Task 7.

- [ ] **Step 1: Write the failing tests** — `internal/sync/merge_test.go`:

```go
package sync

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	idxA = `{"id":"t1","thread_name":"alpha","updated_at":"2026-09-01T10:00:00.000000Z"}` + "\n"
	idxB = `{"id":"t2","thread_name":"beta","updated_at":"2026-09-02T10:00:00.000000Z"}` + "\n"
	idxA2 = `{"id":"t1","thread_name":"alpha renamed","updated_at":"2026-09-03T10:00:00.000000Z"}` + "\n"
)

func TestIsMergeablePath(t *testing.T) {
	for p, want := range map[string]bool{"session_index.jsonl": true, "history.jsonl": true, "sessions/x.jsonl": false, "config.toml": false} {
		if got := IsMergeablePath(p); got != want {
			t.Errorf("IsMergeablePath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestMergeSessionIndexUnionKeepsLatestPerID(t *testing.T) {
	got, err := MergeJSONL(SessionIndexFile, []byte(idxA+idxB), []byte(idxA2))
	if err != nil {
		t.Fatal(err)
	}
	want := idxB + idxA2 // ascending updated_at: t2 (Sep 2) then t1 (Sep 3)
	if string(got) != want {
		t.Errorf("merge =\n%s\nwant\n%s", got, want)
	}
}

func TestMergeSessionIndexIsIdempotent(t *testing.T) {
	once, _ := MergeJSONL(SessionIndexFile, []byte(idxA), []byte(idxB))
	twice, _ := MergeJSONL(SessionIndexFile, once, []byte(idxB))
	self, _ := MergeJSONL(SessionIndexFile, once, once)
	if !bytes.Equal(once, twice) || !bytes.Equal(once, self) {
		t.Errorf("merge is not idempotent:\n%s\n%s\n%s", once, twice, self)
	}
}

func TestMergeSessionIndexKeepsUnparsableLines(t *testing.T) {
	got, _ := MergeJSONL(SessionIndexFile, []byte(idxA+"not json\n"), []byte("not json\n"+idxB))
	if !strings.HasSuffix(string(got), "not json\n") || strings.Count(string(got), "not json") != 1 {
		t.Errorf("unparsable lines must be kept once, at the end:\n%s", got)
	}
}

func TestMergeHistoryOrdersByTsAndDedupes(t *testing.T) {
	h1 := `{"session_id":"s","ts":1700000002,"text":"second"}` + "\n"
	h2 := `{"session_id":"s","ts":1700000001,"text":"first"}` + "\n"
	got, err := MergeJSONL(HistoryFile, []byte(h1), []byte(h2+h1))
	if err != nil {
		t.Fatal(err)
	}
	if want := h2 + h1; string(got) != want {
		t.Errorf("merge =\n%s\nwant\n%s", got, want)
	}
}

func TestMergeHistoryLinesWithoutTsComeFirst(t *testing.T) {
	noTS := `{"session_id":"s","text":"legacy"}` + "\n"
	h := `{"session_id":"s","ts":1700000001,"text":"first"}` + "\n"
	got, _ := MergeJSONL(HistoryFile, []byte(h), []byte(noTS))
	if want := noTS + h; string(got) != want {
		t.Errorf("merge =\n%s\nwant\n%s", got, want)
	}
}

func TestMergeJSONLRejectsOtherPaths(t *testing.T) {
	if _, err := MergeJSONL("sessions/x.jsonl", nil, nil); err == nil {
		t.Error("expected an error for a non-mergeable path")
	}
}

func TestHashBytesMatchesHashFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	fromFile, err := HashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := hashBytes([]byte("abc")); got != fromFile {
		t.Errorf("hashBytes = %s, HashFile = %s", got, fromFile)
	}
}

func TestWriteFileAtomicCreatesParentAndSetsPerm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b", "f.jsonl")
	if err := writeFileAtomic(p, []byte("x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("perm = %o, want 600", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(p)); len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sync/ -run 'TestIsMergeablePath|TestMerge|TestHashBytes|TestWriteFileAtomic' -v`
Expected: build failure (`undefined: MergeJSONL`).

- [ ] **Step 3: Implement** — create `internal/sync/merge.go`:

```go
package sync

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mergeable files are shared single files that every machine appends to. A
// last-writer-wins upload would silently drop the other machine's entries, so
// pull merges them (union) instead of writing a conflict sidecar (spec §6).
const (
	// SessionIndexFile holds one {id, thread_name, updated_at} object per line;
	// the entry with the latest updated_at wins per id.
	SessionIndexFile = "session_index.jsonl"
	// HistoryFile holds one {session_id, ts, text} object per line; distinct
	// lines are kept and ordered by ts.
	HistoryFile = "history.jsonl"
)

// IsMergeablePath reports whether relPath is merged on pull rather than
// downloaded or conflicted.
func IsMergeablePath(relPath string) bool {
	return relPath == SessionIndexFile || relPath == HistoryFile
}

// MergeJSONL returns the union of two copies of a mergeable file. The result is
// deterministic and idempotent: merging a file with itself, or merging twice,
// yields identical bytes. Lines that are not JSON objects are preserved verbatim
// (once each) after the merged entries, in first-seen order.
func MergeJSONL(relPath string, local, remote []byte) ([]byte, error) {
	switch relPath {
	case SessionIndexFile:
		return mergeSessionIndex(local, remote), nil
	case HistoryFile:
		return mergeHistory(local, remote), nil
	}
	return nil, fmt.Errorf("%s is not a mergeable file", relPath)
}

type jsonlLine struct {
	raw []byte
	obj map[string]json.RawMessage // nil when the line is not a JSON object
}

func splitJSONL(data []byte) []jsonlLine {
	var lines []jsonlLine
	for _, raw := range bytes.Split(data, []byte("\n")) {
		raw = bytes.TrimRight(raw, "\r")
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		l := jsonlLine{raw: raw}
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil {
			l.obj = obj
		}
		lines = append(lines, l)
	}
	return lines
}

func joinJSONL(lines [][]byte) []byte {
	if len(lines) == 0 {
		return nil
	}
	return append(bytes.Join(lines, []byte("\n")), '\n')
}

// rawString decodes a JSON string value; non-strings are returned as their raw text.
func rawString(v json.RawMessage) string {
	var s string
	if json.Unmarshal(v, &s) == nil {
		return s
	}
	return string(v)
}

// laterTimestamp reports whether a is strictly later than b. RFC 3339 values
// are compared as times; anything else falls back to string comparison.
func laterTimestamp(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339Nano, a)
	tb, errB := time.Parse(time.RFC3339Nano, b)
	if errA == nil && errB == nil {
		return ta.After(tb)
	}
	return a > b
}

func mergeSessionIndex(local, remote []byte) []byte {
	type entry struct {
		raw     []byte
		updated string
	}
	byID := map[string]entry{}
	var ids []string
	var other [][]byte
	seenOther := map[string]bool{}

	for _, l := range append(splitJSONL(local), splitJSONL(remote)...) {
		if l.obj == nil || l.obj["id"] == nil {
			if key := string(l.raw); !seenOther[key] {
				seenOther[key] = true
				other = append(other, l.raw)
			}
			continue
		}
		id := rawString(l.obj["id"])
		updated := rawString(l.obj["updated_at"])
		cur, ok := byID[id]
		if !ok {
			ids = append(ids, id)
			byID[id] = entry{l.raw, updated}
			continue
		}
		if laterTimestamp(updated, cur.updated) {
			byID[id] = entry{l.raw, updated}
		}
	}

	// Ascending updated_at, id as the tie-breaker, so the output is stable.
	sort.SliceStable(ids, func(i, j int) bool {
		a, b := byID[ids[i]].updated, byID[ids[j]].updated
		if a == b {
			return ids[i] < ids[j]
		}
		return laterTimestamp(b, a)
	})

	out := make([][]byte, 0, len(ids)+len(other))
	for _, id := range ids {
		out = append(out, byID[id].raw)
	}
	out = append(out, other...)
	return joinJSONL(out)
}

func mergeHistory(local, remote []byte) []byte {
	type entry struct {
		raw   []byte
		ts    float64
		hasTS bool
		seq   int
	}
	var entries []entry
	seen := map[string]bool{}

	for _, l := range append(splitJSONL(local), splitJSONL(remote)...) {
		key := string(l.raw)
		if seen[key] {
			continue
		}
		seen[key] = true
		e := entry{raw: l.raw, seq: len(entries)}
		if l.obj != nil && l.obj["ts"] != nil {
			if v, err := strconv.ParseFloat(strings.Trim(string(l.obj["ts"]), `"`), 64); err == nil {
				e.ts, e.hasTS = v, true
			}
		}
		entries = append(entries, e)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.hasTS != b.hasTS {
			return !a.hasTS // lines without a parsable ts sort first
		}
		if a.hasTS && a.ts != b.ts {
			return a.ts < b.ts
		}
		return a.seq < b.seq
	})

	out := make([][]byte, len(entries))
	for i, e := range entries {
		out[i] = e.raw
	}
	return joinJSONL(out)
}

// hashBytes returns the hex SHA-256 of data, the same format HashFile records in state.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeFileAtomic writes data to path via a temp file in the same directory and
// a rename, so a crash never leaves a half-written file behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	return os.Rename(tmpName, path)
}
```

Note: `HashFile` in `state.go` must produce the same format — confirm with `grep -n 'hex.EncodeToString' internal/sync/state.go` (it does; the test in Step 1 guards it).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/sync/ -run 'TestIsMergeablePath|TestMerge|TestHashBytes|TestWriteFileAtomic' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sync/merge.go internal/sync/merge_test.go && git commit -m "feat(sync): union-merge session_index.jsonl and history.jsonl" -m "Two machines appending to the same single file must not lose each other's entries; the merge is deterministic and idempotent (spec §6)." -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: Merge on pull

**Files:**
- Modify: `internal/sync/sync.go` — `SyncResult` (line ~54), `Pull` loop (line ~320, after `stateFile := s.state.GetFile(localPath)`), `downloadFile` (~line 467; extract `fetchRemote` and `localFilePath`), `PullPreview`/`PreviewPull` (~line 688/697)
- Modify: `cmd/codex-sync/main.go` — pull summary (after "downloaded" part, ~line 1200) and `showPullPreview` (~line 2442)
- Test: create `internal/sync/pull_merge_test.go`

**Interfaces:**
- Consumes: `IsMergeablePath`, `MergeJSONL`, `hashBytes`, `writeFileAtomic` (Task 5); `newPeer` (Task 3).
- Produces: `SyncResult.Merged []string`, `PullPreview.WouldMerge []FilePreview`, `(*Syncer).fetchRemote(ctx, relativePath, remoteKey string) ([]byte, error)`, `(*Syncer).localFilePath(relativePath string) (string, error)`, `(*Syncer).mergeRemote(...)`.

- [ ] **Step 1: Write the failing tests** — `internal/sync/pull_merge_test.go`:

```go
package sync

import (
	"context"
	"strings"
	"testing"
)

func TestPullMergesSessionIndexFromBothDevices(t *testing.T) {
	a := setupTestEnv(t)
	b := newPeer(t, a)
	ctx := context.Background()
	writeFile(t, a.claudeDir, "session_index.jsonl", idxA)
	writeFile(t, b.claudeDir, "session_index.jsonl", idxB)

	if _, err := a.syncer.Push(ctx); err != nil {
		t.Fatalf("A push: %v", err)
	}
	res, err := b.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("B pull: %v", err)
	}
	if len(res.Merged) != 1 || res.Merged[0] != SessionIndexFile {
		t.Fatalf("expected one merge, got %+v", res)
	}
	if len(res.Conflicts) != 0 {
		t.Fatalf("mergeable file must not conflict: %v", res.Conflicts)
	}
	got := readFile(t, b.claudeDir, "session_index.jsonl")
	if !strings.Contains(got, `"t1"`) || !strings.Contains(got, `"t2"`) {
		t.Fatalf("union is missing an entry:\n%s", got)
	}

	// B's push uploads the union; A's pull merges it back.
	if _, err := b.syncer.Push(ctx); err != nil {
		t.Fatalf("B push: %v", err)
	}
	if _, err := a.syncer.Pull(ctx); err != nil {
		t.Fatalf("A pull: %v", err)
	}
	if gotA := readFile(t, a.claudeDir, "session_index.jsonl"); gotA != got {
		t.Fatalf("devices diverged:\nA:\n%s\nB:\n%s", gotA, got)
	}
}

func TestPullMergeIsIdempotentAndPushesNothingAfterwards(t *testing.T) {
	a := setupTestEnv(t)
	b := newPeer(t, a)
	ctx := context.Background()
	writeFile(t, a.claudeDir, "history.jsonl", `{"session_id":"s","ts":1700000001,"text":"first"}`+"\n")
	if _, err := a.syncer.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.syncer.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, b.claudeDir, "history.jsonl")
	res, err := b.syncer.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Merged) != 0 {
		t.Errorf("second pull merged again: %v", res.Merged)
	}
	if readFile(t, b.claudeDir, "history.jsonl") != first {
		t.Error("second pull changed the file")
	}
	pushed, err := b.syncer.Push(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pushed.Uploaded) != 0 {
		t.Errorf("nothing new locally, but push uploaded %v", pushed.Uploaded)
	}
}

func TestPreviewPullReportsMerge(t *testing.T) {
	a := setupTestEnv(t)
	b := newPeer(t, a)
	ctx := context.Background()
	writeFile(t, a.claudeDir, "session_index.jsonl", idxA)
	writeFile(t, b.claudeDir, "session_index.jsonl", idxB)
	if _, err := a.syncer.Push(ctx); err != nil {
		t.Fatal(err)
	}
	preview, err := b.syncer.PreviewPull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.WouldMerge) != 1 || preview.WouldMerge[0].Path != SessionIndexFile {
		t.Errorf("WouldMerge = %+v", preview.WouldMerge)
	}
	if len(preview.WouldOverwrite) != 0 || len(preview.WouldConflict) != 0 {
		t.Errorf("mergeable file classified as overwrite/conflict: %+v", preview)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sync/ -run 'TestPullMerge|TestPreviewPullReportsMerge' -v`
Expected: build failure (`res.Merged undefined`, `preview.WouldMerge undefined`).

- [ ] **Step 3: Extract `fetchRemote` and `localFilePath`** — in `internal/sync/sync.go`, add below `uploadFile`:

```go
// fetchRemote downloads, decrypts, decompresses and de-tokenizes one remote object.
func (s *Syncer) fetchRemote(ctx context.Context, relativePath, remoteKey string) ([]byte, error) {
	encrypted, err := s.storage.Download(ctx, remoteKey)
	if err != nil {
		return nil, fmt.Errorf("failed to download: %w", err)
	}
	data, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}
	// Backward-compatible: older remote blobs may be uncompressed
	if isGzipped(data) {
		if data, err = gzipDecompress(data); err != nil {
			return nil, fmt.Errorf("failed to decompress: %w", err)
		}
	}
	if IsPortableContentPath(relativePath) {
		data = s.paths.ResolveContent(data)
	}
	return data, nil
}

// localFilePath resolves a relative path under the base dir, refusing anything
// that would escape it (crafted remote keys).
func (s *Syncer) localFilePath(relativePath string) (string, error) {
	fullPath := filepath.Join(s.claudeDir, relativePath)
	if !strings.HasPrefix(filepath.Clean(fullPath), filepath.Clean(s.claudeDir)+string(filepath.Separator)) {
		return "", fmt.Errorf("refusing to write outside %s: %s", s.claudeDir, relativePath)
	}
	return fullPath, nil
}
```

Rewrite the top of `downloadFile` so it reads (keep the protected guard from Task 3 first, and everything from "Ensure directory exists" onward unchanged):

```go
func (s *Syncer) downloadFile(ctx context.Context, relativePath, remoteKey string, originalMtime *time.Time) error {
	if config.IsProtected(relativePath) {
		return fmt.Errorf("refusing to write protected file %s", relativePath)
	}
	data, err := s.fetchRemote(ctx, relativePath, remoteKey)
	if err != nil {
		return err
	}
	fullPath, err := s.localFilePath(relativePath)
	if err != nil {
		return err
	}
	// Ensure directory exists
	...unchanged from here...
```

- [ ] **Step 4: Add the merge path** — add `Merged []string` to `SyncResult` (after `Downloaded`), `WouldMerge []FilePreview // Shared index files that would be unioned with the local copy` to `PullPreview`, and this method next to `handleConflict`:

```go
// mergeRemote handles a mergeable file (see IsMergeablePath): the remote copy is
// unioned into the local one whenever the remote changed since the last sync,
// or was never synced here. State records the remote hash, so the next push
// uploads the union exactly when the local copy contributed something.
func (s *Syncer) mergeRemote(ctx context.Context, relativePath string, remoteObj storage.ObjectInfo, localExists bool, stateFile *FileState) (bool, error) {
	if stateFile != nil && localExists && !remoteObj.LastModified.After(stateFile.Uploaded) {
		return false, nil // remote unchanged since we last merged it
	}
	remoteData, err := s.fetchRemote(ctx, relativePath, remoteObj.Key)
	if err != nil {
		return false, err
	}
	fullPath, err := s.localFilePath(relativePath)
	if err != nil {
		return false, err
	}
	var localData []byte
	if localExists {
		if localData, err = os.ReadFile(fullPath); err != nil {
			return false, fmt.Errorf("failed to read local file: %w", err)
		}
	}
	merged, err := MergeJSONL(relativePath, localData, remoteData)
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(fullPath, merged, 0600); err != nil {
		return false, fmt.Errorf("failed to write merged file: %w", err)
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return false, err
	}
	s.state.UpdateFile(relativePath, info, hashBytes(remoteData))
	s.state.MarkUploaded(relativePath)
	return true, nil
}
```

In `Pull`, immediately after `stateFile := s.state.GetFile(localPath)` inside the `for localPath, remoteObj := range remoteFiles` loop, insert:

```go
		if IsMergeablePath(localPath) {
			merged, err := s.mergeRemote(ctx, localPath, remoteObj, localExists, stateFile)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", localPath, err))
			} else if merged {
				result.Merged = append(result.Merged, localPath)
			}
			continue
		}
```

In `PreviewPull`, inside its `for localPath, remoteObj := range remoteFiles` loop, `stateFile` and `fp` are already built at the top of the loop (`fp := FilePreview{...}` followed by the `if localExists { fp.LocalTime = ...; fp.LocalSize = ... }` block). Insert directly after that `if localExists {...}` block, before `if !localExists {`:

```go
		if IsMergeablePath(localPath) {
			if stateFile == nil || !localExists || remoteObj.LastModified.After(stateFile.Uploaded) {
				preview.WouldMerge = append(preview.WouldMerge, fp)
			}
			continue
		}
```

- [ ] **Step 5: Surface it in the CLI** — in `cmd/codex-sync/main.go` `pullCmd`: add `if len(result.Merged) > 0 { parts = append(parts, fmt.Sprintf("%s%d merged%s", colorGreen, len(result.Merged), colorReset)) }` next to the "downloaded" part, and change the "nothing happened" condition to also require `len(result.Merged) == 0`. In `showPullPreview`, count `len(preview.WouldMerge)` in `total` and print:

```go
	if len(preview.WouldMerge) > 0 {
		fmt.Printf("Would merge (%d shared index files, union of local and remote):\n", len(preview.WouldMerge))
		for _, f := range preview.WouldMerge {
			fmt.Printf("  %s∪%s %s\n", colorGreen, colorReset, f.Path)
		}
		fmt.Println()
	}
```

- [ ] **Step 6: Run tests**

Run: `make check`
Expected: `All checks passed!` (the three new tests included).

- [ ] **Step 7: Commit**

```bash
git add -A && git commit -m "feat(sync): merge shared index files on pull instead of overwriting" -m "session_index.jsonl and history.jsonl are unioned with the local copy; state keeps the remote hash so the next push uploads the union (spec §6)." -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: Pull-side removal into a trash directory

**Files:**
- Modify: `internal/config/config.go` (constants block: `TrashDir = "trash"`; add `TrashDirPath()` next to `AgeKeyFilePath`)
- Modify: `internal/sync/sync.go` — `Syncer` struct, `NewSyncer`, `NewSyncerWith`, `SyncResult`, `PullPreview`, `Pull` (after downloads, before state save), `PreviewPull`
- Modify: `internal/sync/profile_test.go` `newPeer` (set `trashDir`) and `sync_push_pull_test.go` `setupTestEnv` (set `trashDir: filepath.Join(stateDir, "trash")`)
- Modify: `cmd/codex-sync/main.go` `pullCmd` (flag `--no-delete`, summary) and `showPullPreview`
- Test: create `internal/sync/pull_remove_test.go`

**Interfaces:**
- Produces: `config.TrashDirPath() string`; `(*Syncer).SetNoDelete(bool)`; `(*Syncer).SetTrashDir(string)`; `SyncResult.Removed []string`, `SyncResult.KeptLocal []string`; `PullPreview.WouldRemove []FilePreview`, `PullPreview.WouldKeepLocal []FilePreview`; `(*Syncer).staleLocalFiles(remoteFiles map[string]storage.ObjectInfo, localFiles map[string]os.FileInfo) (removable, kept []string, err error)`; `(*Syncer).moveToTrash(relPath, batch string) error`.
- Consumes: `IsMergeablePath` (Task 5), `config.IsProtected` (Task 3), `newPeer` (Task 3).

- [ ] **Step 1: Write the failing tests** — `internal/sync/pull_remove_test.go`:

```go
package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const (
	keep   = "sessions/2026/01/01/rollout-keep.jsonl"
	gone   = "sessions/2026/01/02/rollout-gone.jsonl"
	rollout = `{"type":"session_meta"}` + "\n"
)

// twoDevicesWithGoneFile: A pushes keep+gone, B pulls both, A deletes gone and
// pushes again. Returns A and B with B still holding its copy of gone.
func twoDevicesWithGoneFile(t *testing.T) (*testEnv, *testEnv) {
	t.Helper()
	ctx := context.Background()
	a := setupTestEnv(t)
	b := newPeer(t, a)
	writeFile(t, a.claudeDir, keep, rollout)
	writeFile(t, a.claudeDir, gone, rollout)
	if _, err := a.syncer.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.syncer.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(a.claudeDir, gone)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.syncer.Push(ctx); err != nil {
		t.Fatal(err)
	}
	return a, b
}

func TestPullMovesUnchangedFileGoneFromRemoteToTrash(t *testing.T) {
	_, b := twoDevicesWithGoneFile(t)
	res, err := b.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 1 || res.Removed[0] != gone {
		t.Fatalf("Removed = %v, want [%s]", res.Removed, gone)
	}
	if _, err := os.Stat(filepath.Join(b.claudeDir, gone)); !os.IsNotExist(err) {
		t.Error("file should be gone from the base dir")
	}
	if _, err := os.Stat(filepath.Join(b.claudeDir, keep)); err != nil {
		t.Error("unrelated file must stay")
	}
	matches, _ := filepath.Glob(filepath.Join(b.syncer.trashDir, "*", filepath.FromSlash(gone)))
	if len(matches) != 1 {
		t.Errorf("expected one trashed copy, got %v", matches)
	}
	if b.syncer.state.GetFile(gone) != nil {
		t.Error("state entry should be removed")
	}
}

func TestPullKeepsLocallyModifiedFileGoneFromRemote(t *testing.T) {
	_, b := twoDevicesWithGoneFile(t)
	writeFile(t, b.claudeDir, gone, rollout+`{"type":"turn"}`+"\n") // B appended locally
	res, err := b.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 {
		t.Errorf("modified file must not be removed: %v", res.Removed)
	}
	if len(res.KeptLocal) != 1 || res.KeptLocal[0] != gone {
		t.Errorf("KeptLocal = %v, want [%s]", res.KeptLocal, gone)
	}
	if _, err := os.Stat(filepath.Join(b.claudeDir, gone)); err != nil {
		t.Error("modified file must stay in place")
	}
}

func TestPullNoDeleteLeavesFiles(t *testing.T) {
	_, b := twoDevicesWithGoneFile(t)
	b.syncer.SetNoDelete(true)
	res, err := b.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 {
		t.Errorf("--no-delete must not remove: %v", res.Removed)
	}
	if _, err := os.Stat(filepath.Join(b.claudeDir, gone)); err != nil {
		t.Error("file must stay with --no-delete")
	}
}

func TestPullWithEmptyRemoteRemovesNothing(t *testing.T) {
	_, b := twoDevicesWithGoneFile(t)
	b.store.mu.Lock()
	b.store.objects = map[string]mockObject{} // bucket wiped or misconfigured
	b.store.mu.Unlock()
	res, err := b.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 {
		t.Errorf("an empty remote must never remove local files: %v", res.Removed)
	}
	for _, p := range []string{keep, gone} {
		if _, err := os.Stat(filepath.Join(b.claudeDir, p)); err != nil {
			t.Errorf("%s must stay", p)
		}
	}
}

func TestPreviewPullReportsRemovals(t *testing.T) {
	_, b := twoDevicesWithGoneFile(t)
	preview, err := b.syncer.PreviewPull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.WouldRemove) != 1 || preview.WouldRemove[0].Path != gone {
		t.Errorf("WouldRemove = %+v", preview.WouldRemove)
	}
	if _, err := os.Stat(filepath.Join(b.claudeDir, gone)); err != nil {
		t.Error("preview must not change files")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/sync/ -run 'TestPullMovesUnchanged|TestPullKeepsLocally|TestPullNoDelete|TestPullWithEmptyRemote|TestPreviewPullReportsRemovals' -v`
Expected: build failure (`res.Removed`, `SetNoDelete`, `trashDir` undefined).

- [ ] **Step 3: Implement** — `internal/config/config.go`: add `TrashDir = "trash"` to the constants block and

```go
// TrashDirPath is where pull moves local files that vanished from the remote
// (spec §7); nothing is ever unlinked outright.
func TrashDirPath() string {
	return filepath.Join(ConfigDirPath(), TrashDir)
}
```

`internal/sync/sync.go`: add `"sort"` to the imports; add fields to `Syncer`:

```go
	noDelete bool   // pull --no-delete: never remove local files that vanished remotely
	trashDir string // where removed files are moved (config.TrashDirPath by default)
```

Set `trashDir: config.TrashDirPath(),` in both `NewSyncer` and `NewSyncerWith` literals. Add:

```go
// SetNoDelete disables pull-side removal of files that vanished from the remote.
func (s *Syncer) SetNoDelete(v bool) { s.noDelete = v }

// SetTrashDir overrides where removed files are moved (for testing).
func (s *Syncer) SetTrashDir(dir string) { s.trashDir = dir }
```

Add to `SyncResult`: `Removed []string // moved to the trash directory: vanished remotely, unchanged locally` and `KeptLocal []string // vanished remotely but modified locally: left in place`. Add to `PullPreview`: `WouldRemove []FilePreview` and `WouldKeepLocal []FilePreview`.

Add next to `handleConflict`:

```go
// staleLocalFiles lists tracked files that are present locally but gone from
// the remote: removable when unchanged since the last sync, kept when modified
// locally. Mergeable and protected paths are never candidates.
func (s *Syncer) staleLocalFiles(remoteFiles map[string]storage.ObjectInfo, localFiles map[string]os.FileInfo) (removable, kept []string, err error) {
	s.state.mu.Lock()
	tracked := make([]string, 0, len(s.state.Files))
	for p := range s.state.Files {
		tracked = append(tracked, p)
	}
	s.state.mu.Unlock()
	sort.Strings(tracked)

	for _, relPath := range tracked {
		if _, onRemote := remoteFiles[relPath]; onRemote {
			continue
		}
		if _, onDisk := localFiles[relPath]; !onDisk {
			continue
		}
		if IsMergeablePath(relPath) || config.IsProtected(relPath) {
			continue
		}
		hash, err := HashFile(filepath.Join(s.claudeDir, relPath))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to hash %s: %w", relPath, err)
		}
		if hash == s.state.GetFile(relPath).Hash {
			removable = append(removable, relPath)
		} else {
			kept = append(kept, relPath)
		}
	}
	return removable, kept, nil
}

// moveToTrash relocates a local file to <trashDir>/<batch>/<relPath>. A rename
// that fails (different volume) falls back to copy-then-remove.
func (s *Syncer) moveToTrash(relPath, batch string) error {
	src := filepath.Join(s.claudeDir, relPath)
	dst := filepath.Join(s.trashDir, batch, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return fmt.Errorf("failed to create trash directory: %w", err)
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0600); err != nil {
		return err
	}
	return os.Remove(src)
}
```

In `Pull`, after the download block's `s.progress(ProgressEvent{Action: "download", Complete: true, Total: total})` and before `s.state.LastPull = time.Now()`, insert:

```go
	// Files that vanished from the remote (deleted or moved on another machine)
	// are moved to the trash when unchanged locally; never when this pull saw an
	// empty remote (Pull returned early above).
	if !s.noDelete {
		removable, kept, err := s.staleLocalFiles(remoteFiles, localFiles)
		if err != nil {
			result.Errors = append(result.Errors, err)
		} else {
			batch := time.Now().Format("20060102-150405")
			for _, relPath := range removable {
				if err := s.moveToTrash(relPath, batch); err != nil {
					result.Errors = append(result.Errors, fmt.Errorf("%s: %w", relPath, err))
					continue
				}
				s.state.RemoveFile(relPath)
				result.Removed = append(result.Removed, relPath)
			}
			result.KeptLocal = kept
		}
	}
```

In `PreviewPull`, after the existing loops and before `return preview, nil`, insert:

```go
	if len(remoteObjects) > 0 && !s.noDelete {
		removable, kept, err := s.staleLocalFiles(remoteFiles, localFiles)
		if err != nil {
			return nil, err
		}
		for _, p := range removable {
			preview.WouldRemove = append(preview.WouldRemove, FilePreview{Path: p, LocalSize: localFiles[p].Size()})
		}
		for _, p := range kept {
			preview.WouldKeepLocal = append(preview.WouldKeepLocal, FilePreview{Path: p, LocalSize: localFiles[p].Size()})
		}
	}
```

(`PreviewPull` already lists local-only files in `LocalOnlyFiles`; a file that is both tracked-and-gone and local-only now also appears in `WouldRemove`/`WouldKeepLocal`, which is the actionable list.)

Test helpers: in `sync_push_pull_test.go` `setupTestEnv` add `trashDir: filepath.Join(stateDir, "trash"),` to the `Syncer` literal; in `profile_test.go` `newPeer` add `trashDir: filepath.Join(stateDir, "trash"),`.

- [ ] **Step 4: CLI** — in `pullCmd`: declare `var noDelete bool`, register `cmd.Flags().BoolVar(&noDelete, "no-delete", false, "Never remove local files that vanished from the remote")`, call `syncer.SetNoDelete(noDelete)` right after `NewSyncer`. In the summary add parts for `len(result.Removed)` ("%d removed") and `len(result.KeptLocal)` ("%d kept"), include both in the "nothing happened" condition, and after the conflicts block print:

```go
					if len(result.Removed) > 0 {
						fmt.Printf("\n%sRemoved (vanished from remote; moved to %s):%s\n", colorDim, config.TrashDirPath(), colorReset)
						for _, p := range result.Removed {
							fmt.Printf("  %s-%s %s\n", colorYellow, colorReset, p)
						}
					}
					if len(result.KeptLocal) > 0 {
						fmt.Printf("\n%sKept (vanished from remote but changed locally):%s\n", colorDim, colorReset)
						for _, p := range result.KeptLocal {
							fmt.Printf("  %s•%s %s\n", colorYellow, colorReset, p)
						}
					}
```

In `showPullPreview`: set `syncer.SetNoDelete(noDelete)` before calling it (the flag variable is in scope of `pullCmd`; pass it as a parameter `showPullPreview(ctx, syncer)` already receives the syncer, so calling `SetNoDelete` before is enough), include `len(preview.WouldRemove)` in `total`, and print:

```go
	if len(preview.WouldRemove) > 0 {
		fmt.Printf("Would remove (%d files vanished from remote; moved to %s):\n", len(preview.WouldRemove), config.TrashDirPath())
		for _, f := range preview.WouldRemove {
			fmt.Printf("  %s-%s %s (%s)\n", colorYellow, colorReset, f.Path, util.FormatSize(f.LocalSize))
		}
		fmt.Println()
	}
	if len(preview.WouldKeepLocal) > 0 {
		fmt.Printf("Would keep (%d files vanished from remote but changed locally):\n", len(preview.WouldKeepLocal))
		for _, f := range preview.WouldKeepLocal {
			fmt.Printf("  %s•%s %s\n", colorYellow, colorReset, f.Path)
		}
		fmt.Println()
	}
```

- [ ] **Step 5: Run everything**

Run: `make check`
Expected: `All checks passed!`

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat(sync): move files that vanished remotely to a trash directory on pull" -m "codex archive moves rollouts between directories; without pull-side removal the other machine keeps the thread twice. Only unchanged files are removed, never unlinked, and an empty remote removes nothing (spec §7)." -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 8: CLI wording, full scope by default, releases from this repository

**Files:**
- Modify: `cmd/codex-sync/main.go` — `resolveScope` (lines ~249–276), `--scope` help (~178), root/push/pull/diff `Long` strings, `hasExistingClaudeFiles` (~2290), `getLatestRelease` (~2054), `getAllReleases` (~2676), `updateCmd`/`changelogCmd` error handling
- Test: create `cmd/codex-sync/release_test.go`

**Interfaces:**
- Produces: `const githubRepo = "d-jiao/codex-sync"`, `latestReleaseURL() string`, `releasesURL(limit int) string`, `errNoReleases`.

- [ ] **Step 1: Write the failing test** — `cmd/codex-sync/release_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

func TestReleaseURLsPointAtThisRepository(t *testing.T) {
	for _, u := range []string{latestReleaseURL(), releasesURL(5)} {
		if !strings.Contains(u, "https://api.github.com/repos/d-jiao/codex-sync/releases") {
			t.Errorf("unexpected release URL %q", u)
		}
		if strings.Contains(u, "tawanorg") {
			t.Errorf("release URL still points at upstream: %q", u)
		}
	}
	if !strings.HasSuffix(releasesURL(5), "per_page=5") {
		t.Errorf("releasesURL(5) = %q", releasesURL(5))
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/codex-sync/ -run TestReleaseURLs -v`
Expected: build failure (`undefined: latestReleaseURL`).

- [ ] **Step 3: Implement the release plumbing** — near `getLatestRelease` add:

```go
// githubRepo is where codex-sync publishes releases (update/changelog read from it).
const githubRepo = "d-jiao/codex-sync"

var errNoReleases = errors.New("no published releases yet")

func latestReleaseURL() string {
	return "https://api.github.com/repos/" + githubRepo + "/releases/latest"
}

func releasesURL(limit int) string {
	return fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=%d", githubRepo, limit)
}
```

(`main.go` does not import `"errors"` yet — add it). In `getLatestRelease` replace the `url := ...` line with `url := latestReleaseURL()`, and change the status check to:

```go
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoReleases
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}
```

In `getAllReleases` replace its `url := ...` with `url := releasesURL(limit)` and apply the same 404 branch. In `updateCmd`'s and `changelogCmd`'s `RunE`, where `getLatestRelease()` / `getAllReleases(...)` return an error, add before returning it:

```go
				if errors.Is(err, errNoReleases) {
					fmt.Printf("%sNo published releases yet.%s Update from source:\n  git pull && make build && make install\n", colorYellow, colorReset)
					return nil
				}
```

- [ ] **Step 4: Scope default and wording** — replace the `case "":` branch of `resolveScope` with:

```go
	case "":
		prompt := &survey.Select{
			Message: "What should be synced?",
			Options: []string{
				"Everything — sessions, names, history, attachments, config.toml, rules, skills, memories (recommended)",
				"Sessions only — conversations, names, history and attachments",
			},
		}
		var c int
		if err := survey.AskOne(prompt, &c); err != nil {
			return "", err
		}
		if c == 1 {
			return config.ScopeSessions, nil
		}
		return config.ScopeFull, nil
```

Update the `--scope` flag help to `"Sync scope: 'full' (default: sessions + config, rules, skills, memories) or 'sessions' (conversation data only)"`. Then the mechanical wording pass:

```bash
sed -i '' -e 's#~/\.claude/settings\.json#~/.codex/config.toml#g' -e 's#~/\.claude\.json#~/.codex/config.toml#g' -e 's#~/\.claude#~/.codex#g' -e 's/Claude Code/Codex/g' -e 's/claude --resume/codex resume/g' -e 's/hasExistingClaudeFiles/hasExistingBaseFiles/g' cmd/codex-sync/main.go
```

Read `git diff cmd/codex-sync/main.go` line by line and fix any sentence the substitution made wrong (for example a `Long:` text that still describes plugins or agents — the Codex profile has neither; describe "sessions, config, rules, skills, memories" instead). The root command `Long` becomes: `A CLI tool to sync your Codex home (~/.codex, or $CODEX_HOME) across devices using cloud storage with encryption.`

- [ ] **Step 5: Run everything**

Run: `make check && go run ./cmd/codex-sync --help && go run ./cmd/codex-sync pull --help | grep -- --no-delete`
Expected: checks pass; help shows Codex wording, no `~/.claude`, and the `--no-delete` flag. `grep -n 'claude' cmd/codex-sync/main.go` shows nothing but the upstream attribution, if any.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "feat(cli): Codex wording, full scope by default, releases from this repository" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 9: launchd agent template and install targets

**Files:**
- Create: `scripts/launchd/com.codex-sync.daily.plist.template`
- Modify: `Makefile` (add `INSTALL_DIR`, `install`, `install-launchd`, `uninstall-launchd`; add them to `.PHONY`)

**Interfaces:**
- Produces: `make install` (copies `bin/codex-sync` to `$(INSTALL_DIR)`, default `~/.local/bin`), `make install-launchd`, `make uninstall-launchd`; launchd label `com.codex-sync.daily`.

- [ ] **Step 1: Create the template** — `scripts/launchd/com.codex-sync.daily.plist.template`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.codex-sync.daily</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-c</string>
    <string>__BIN__ pull -q &amp;&amp; __BIN__ push -q</string>
  </array>
  <key>StartCalendarInterval</key>
  <dict>
    <key>Hour</key>
    <integer>3</integer>
    <key>Minute</key>
    <integer>0</integer>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>StandardOutPath</key>
  <string>__HOME__/Library/Logs/codex-sync.log</string>
  <key>StandardErrorPath</key>
  <string>__HOME__/Library/Logs/codex-sync.log</string>
</dict>
</plist>
```

`RunAtLoad` gives the "pull at login" from spec §9 (the same pull-then-push runs at login and at 03:00; launchd runs a missed calendar slot at the next wake).

- [ ] **Step 2: Add the Makefile targets** — after the `setup-hooks` target:

```make
INSTALL_DIR ?= $(HOME)/.local/bin
LAUNCHD_LABEL = com.codex-sync.daily
LAUNCHD_PLIST = $(HOME)/Library/LaunchAgents/$(LAUNCHD_LABEL).plist

# Install the binary for the current user
install: build
	mkdir -p $(INSTALL_DIR)
	install -m 755 $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_DIR)/$(BINARY_NAME)
	@echo "Installed $(INSTALL_DIR)/$(BINARY_NAME)"

# Install the daily launchd agent (pull then push at 03:00 and at login)
install-launchd: install
	mkdir -p $(HOME)/Library/LaunchAgents $(HOME)/Library/Logs
	sed -e 's#__BIN__#$(INSTALL_DIR)/$(BINARY_NAME)#g' -e 's#__HOME__#$(HOME)#g' \
		scripts/launchd/$(LAUNCHD_LABEL).plist.template > $(LAUNCHD_PLIST)
	plutil -lint $(LAUNCHD_PLIST)
	launchctl bootout gui/$$(id -u) $(LAUNCHD_PLIST) 2>/dev/null || true
	launchctl bootstrap gui/$$(id -u) $(LAUNCHD_PLIST)
	@echo "Installed $(LAUNCHD_LABEL): daily at 03:00 and at login; log: ~/Library/Logs/codex-sync.log"

uninstall-launchd:
	launchctl bootout gui/$$(id -u) $(LAUNCHD_PLIST) 2>/dev/null || true
	rm -f $(LAUNCHD_PLIST)
	@echo "Removed $(LAUNCHD_LABEL)"
```

Add `install install-launchd uninstall-launchd` to the `.PHONY` line.

- [ ] **Step 3: Verify without installing**

Run: `make -n install-launchd | head -12` and `sed -e 's#__BIN__#/usr/local/bin/codex-sync#g' -e "s#__HOME__#$HOME#g" scripts/launchd/com.codex-sync.daily.plist.template > /tmp/codex-sync-test.plist && plutil -lint /tmp/codex-sync-test.plist && rm /tmp/codex-sync-test.plist`
Expected: the dry run shows the sed/plutil/launchctl steps; `plutil` prints `OK`.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "feat(launchd): daily pull/push agent template and install targets" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 10: Acceptance script — two Codex homes list the same threads

**Files:**
- Create: `integration/codex_listing_check.py`
- Delete: `plan/spike/list_threads.py` (superseded)

**Interfaces:**
- Produces: `python3 integration/codex_listing_check.py --source <HOME_A> --synced <HOME_B> [--codex-bin PATH]`; exit 0 when the user-visible thread ids match, 1 otherwise. Manual only (needs a Codex engine binary); not wired into CI.

- [ ] **Step 1: Write the script**

```python
#!/usr/bin/env python3
"""Compare the user-visible Codex threads of two homes (spec §12, step 3).

Lists threads through the app-server protocol of a Codex engine binary,
using every model provider defined in the source home's config.toml so the
comparison is not narrowed to the current provider. Nothing in either home is
modified beyond what the engine itself writes on startup.

Usage:
  codex_listing_check.py --source ~/.codex --synced /path/to/other/home \
      [--codex-bin /Applications/ChatGPT.app/Contents/Resources/codex]
"""
import argparse, json, os, re, subprocess, sys, time

KINDS = ["cli", "vscode", "exec", "appServer"]  # user-visible kinds; sub-agent threads are children


def providers(home):
    found = {"openai"}
    try:
        with open(os.path.join(home, "config.toml")) as f:
            for line in f:
                m = re.match(r'\s*\[model_providers\.([^\]]+)\]', line)
                if m:
                    found.add(m.group(1).strip('"'))
    except FileNotFoundError:
        pass
    return sorted(found)


def list_threads(codex_bin, home, provs):
    env = dict(os.environ, CODEX_HOME=home)
    p = subprocess.Popen([codex_bin, "app-server", "--stdio"], stdin=subprocess.PIPE,
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, text=True)

    def send(o):
        p.stdin.write(json.dumps(o) + "\n"); p.stdin.flush()

    def wait(rid, timeout=120):
        end = time.time() + timeout
        while time.time() < end:
            line = p.stdout.readline()
            if not line:
                raise RuntimeError("app-server closed its output")
            try:
                m = json.loads(line)
            except ValueError:
                continue
            if m.get("id") == rid:
                if "error" in m:
                    raise RuntimeError(m["error"])
                return m["result"]
        raise RuntimeError("timeout waiting for id %s" % rid)

    try:
        send({"id": 1, "method": "initialize", "params": {"clientInfo": {"name": "codex-sync-check", "version": "0"}}})
        wait(1)
        send({"method": "initialized", "params": {}})
        threads, cursor, rid = {}, None, 2
        for archived in (False, True):
            cursor = None
            while True:
                params = {"limit": 200, "sourceKinds": KINDS, "modelProviders": provs, "archived": archived}
                if cursor:
                    params["cursor"] = cursor
                send({"id": rid, "method": "thread/list", "params": params})
                res = wait(rid); rid += 1
                for t in res["data"]:
                    threads[t["id"]] = {"name": t.get("name"), "archived": archived}
                cursor = res.get("nextCursor")
                if not cursor:
                    break
        return threads
    finally:
        p.kill()


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--source", required=True, help="Codex home the data was pushed from")
    ap.add_argument("--synced", required=True, help="Codex home that pulled it")
    ap.add_argument("--codex-bin", default=os.environ.get("CODEX_BIN", "codex"), help="engine binary (default: codex on PATH or $CODEX_BIN)")
    a = ap.parse_args()

    provs = providers(a.source)
    src = list_threads(a.codex_bin, os.path.expanduser(a.source), provs)
    dst = list_threads(a.codex_bin, os.path.expanduser(a.synced), provs)
    missing = sorted(set(src) - set(dst))
    extra = sorted(set(dst) - set(src))
    unnamed = sorted(i for i in src if src[i]["name"] and i in dst and not dst[i]["name"])

    print(f"source threads: {len(src)}   synced threads: {len(dst)}   providers: {provs}")
    for i in missing[:20]:
        print("  MISSING in synced:", i)
    for i in extra[:20]:
        print("  EXTRA in synced:  ", i)
    if unnamed:
        print(f"  {len(unnamed)} threads are named in source but unnamed in synced (known v1 limitation, spec §8)")
    ok = not missing and not extra
    print("OK: listings match" if ok else "MISMATCH")
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Sanity-run it against one home twice** (same home as source and synced must report OK):

Run: `python3 integration/codex_listing_check.py --source ~/.codex --synced ~/.codex --codex-bin /Applications/ChatGPT.app/Contents/Resources/codex`
Expected: `OK: listings match` (adjust `--codex-bin` to any Codex engine ≥ the version that wrote the data).

- [ ] **Step 3: Commit**

```bash
git rm -q plan/spike/list_threads.py && git add integration/codex_listing_check.py && git commit -m "test(integration): acceptance script comparing thread listings of two Codex homes" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 11: Documentation

**Files:**
- Modify: `README.md` (rewrite), `CLAUDE.md` (rewrite), `SECURITY-AUDIT.md` (salt note), `CHANGELOG.md`, `AGENTS.md`

- [ ] **Step 1: Rewrite `README.md`** with these sections and content (keep the upstream sections on provider setup — R2/S3/GCS/S3-compatible/WebDAV bucket creation, init options, quiet mode, exclude patterns, forgot-passphrase, cost — verbatim except for the tool name):

```markdown
# codex-sync

Encrypted cross-device sync for OpenAI Codex local state. Continue a Codex
conversation on another Mac; keep config, rules, skills and memories in step.

codex-sync is a fork of [claude-sync](https://github.com/tawanorg/claude-sync)
(MIT) that syncs `~/.codex` (or `$CODEX_HOME`) instead of `~/.claude`. Files are
gzip-compressed and age-encrypted before upload; storage is your own bucket
(Cloudflare R2, S3, GCS, S3-compatible, or WebDAV).

## Quick start

    make build && make install          # installs ~/.local/bin/codex-sync
    codex-sync init                     # provider, bucket, passphrase, scope
    codex-sync push                     # first machine
    codex-sync init && codex-sync pull  # second machine, same passphrase
    make install-launchd                # daily pull+push at 03:00 and at login

Keep both Macs on the same Codex version: an older engine cannot list threads
written by a newer one.

## What gets synced

| Path (under ~/.codex) | Scope | Notes |
|---|---|---|
| `sessions/`, `archived_sessions/` | sessions | conversations (rollout files; the source of truth) |
| `session_index.jsonl` | sessions | thread names — merged, never overwritten |
| `history.jsonl` | sessions | prompt history — merged, never overwritten |
| `attachments/` | sessions | files you attached to threads |
| `config.toml` | full | providers, MCP servers, project trust |
| `rules/`, `skills/`, `memories/`, `AGENTS.md` | full | |

Never synced: `auth.json`, `installation_id`, every `*.sqlite`/`*.db`, `plugins/`,
`packages/`, `cache/`, logs, worktrees and other runtime state. Codex rebuilds
its databases from the rollout files.

## How pull behaves

- New and changed remote files are downloaded; a file changed on both sides is
  kept locally and the remote copy saved as `<file>.conflict.<timestamp>`
  (`codex-sync conflicts` resolves them).
- `session_index.jsonl` and `history.jsonl` are unioned with your local copy.
- A file that vanished from the remote (deleted or archived on the other Mac) is
  moved to `~/.codex-sync/trash/<timestamp>/` when unchanged locally; changed
  files stay. `codex-sync pull --no-delete` disables this; `--dry-run` previews it.
- An empty remote never removes anything.

## Limitations (v1)

- Thread names: pulled threads appear unnamed in the Codex app until renamed
  there (Codex does not restore names from the synced index).
- Do not resume the same thread on two Macs between syncs; you would get a
  conflict sidecar instead of a merged transcript.
- Project organization, automations and the memories database live only in
  SQLite and are not synced.
- macOS only; no npm package — build from source.

## Security

Same model as claude-sync: gzip → age (X25519/ChaCha20-Poly1305); passphrase
keys derived with Argon2id and the fixed salt `sha256("codex-sync-v1")` (so the
same passphrase gives the same key on every device, and a different key than
claude-sync). Config and keys are stored 0600 under `~/.codex-sync/`.
```

Then the retained upstream sections (provider setup, commands table without `migrate`/`rebuild-history`/`mcp`/`auto`, pull options with `--no-delete`, exclude patterns, forgot passphrase, cost, build from source, license — MIT, with the NOTICE attribution).

- [ ] **Step 2: Rewrite `CLAUDE.md`** — drop the fork-status banner; update Repository Purpose (Codex, base dir via `CODEX_HOME`), Common Commands (add `make install`, `make install-launchd`), Architecture (config layer: profile/hard excludes/protected paths; sync layer: merge on pull, trash removal; remove MCP section), On-disk layout (`~/.codex-sync/` with `trash/`), Sync semantics (add merge and removal bullets), Distribution (build from source; `update` reads this repository's releases), Gotchas (replace the path-based-indexing note with: rollout names are portable; content is `${HOME}`-tokenized; keep engine versions aligned; never sync SQLite; the salt is `codex-sync-v1` and must not change). Keep the CI/pre-commit section.

- [ ] **Step 3: `SECURITY-AUDIT.md`** — add at the top:

```markdown
> **codex-sync note (2026-09-15):** this audit was inherited from claude-sync at
> commit 49420ef. The crypto and storage design is unchanged except the Argon2
> salt constant, which is `sha256("codex-sync-v1")` in this fork (domain
> separation from claude-sync). It must remain fixed.
```

- [ ] **Step 4: `CHANGELOG.md`** — under `## [Unreleased]` list: Codex base dir via `CODEX_HOME`; Codex sync profile with hard excludes and protected paths; merge-on-pull for `session_index.jsonl`/`history.jsonl`; pull-side removal to trash with `--no-delete`; launchd install targets; removed `mcp`, `auto`, `migrate`, `rebuild-history`.

- [ ] **Step 5: `AGENTS.md`** — replace the "CLAUDE.md still describes claude-sync" line with `CLAUDE.md — build, test and architecture guidance`.

- [ ] **Step 6: Verify and commit**

Run: `make check && grep -n 'claude' README.md CLAUDE.md | grep -viE 'claude-sync|Claude <noreply|Co-Authored'`
Expected: checks pass; no stray Claude Code references.

```bash
git add -A && git commit -m "docs: rewrite README and CLAUDE.md for codex-sync" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 12: Final verification and handoff to rollout

**Files:**
- Modify: `plan/task_plan.md` (Phase 3 and 4 done, status → Phase 5)

- [ ] **Step 1: Full verification**

Run:
```bash
make check && go test -cover ./internal/... 2>&1 | grep -E 'coverage|FAIL' && make build && ./bin/codex-sync --help && ./bin/codex-sync pull --help | grep -- --no-delete && grep -rnE 'ClaudeDir|claudesettings|MCPBaseline|tawanorg/codex-sync' --include='*.go' . ; echo "grep exit $? (1 = clean)"
```
Expected: all packages ≥ 60% coverage (`internal/storage/gcs` and `r2` have no tests upstream and are excluded by CI's package list — check `.github/workflows/ci.yml` lines 27–45 for the exact list), build succeeds, help shows the Codex surface, grep is clean.

- [ ] **Step 2: Smoke-test against a throwaway home** (no real bucket needed — the wizard is skipped):

```bash
export CODEX_HOME=$(mktemp -d)/codex-home && mkdir -p "$CODEX_HOME/sessions/2026/01/01" && echo '{"type":"session_meta"}' > "$CODEX_HOME/sessions/2026/01/01/rollout-x.jsonl" && echo '{"token":"x"}' > "$CODEX_HOME/auth.json" && ./bin/codex-sync status 2>&1 | head -5; unset CODEX_HOME
```
Expected: `status` reports the rollout as a pending change and never mentions `auth.json` (it exits with "config not found" if `init` was never run on this machine — that is fine; the point is the base-dir and profile wiring, visible in `codex-sync paths list`).

- [ ] **Step 3: Update the plan status and push**

Mark Phase 3 and Phase 4 `[x]` in `plan/task_plan.md`, set Status to "Phase 5 next — rollout per spec §12", then:

```bash
git add plan/task_plan.md && git commit -m "docs(plan): implementation complete; next is rollout" -m "Co-Authored-By: Claude <noreply@anthropic.com>" && git push
```

---

## Self-review

**Spec coverage.** §2 constraints → Global Constraints; §3 salt → Task 11 docs (code already correct); §4 base dir → Task 1; §5 profile, hard excludes, protected, regenerated files → Task 3; §6 portability → Task 4, merges → Tasks 5–6; §7 removal/trash/`--no-delete`/preview → Task 7, conflicts unchanged; §8 names → documented in Task 11 (limitation) and checked by Task 10's script; §9 scheduling → Task 9; §10 CLI surface → Tasks 2, 7, 8; §11 module map → Tasks 1–8, 10; §12 acceptance → Task 10 script + Phase 5 in `plan/task_plan.md`; §14 tests → each task.

**Placeholders.** None: every code step carries the code; the only "find with grep" steps are for deleting known symbols (Task 2) and reviewing a mechanical substitution (Task 8).

**Type consistency.** `newPeer`/`seedRemote` defined in Task 3 and used in Tasks 6–7; `idxA`/`idxB` constants defined in Task 5's test file and reused in Task 6's tests (same package); `Syncer.trashDir` introduced in Task 7 and set in both test helpers there; `fetchRemote` signature `(ctx, relativePath, remoteKey string) ([]byte, error)` used identically in `downloadFile` and `mergeRemote`; `SyncResult.Merged/Removed/KeptLocal` and `PullPreview.WouldMerge/WouldRemove/WouldKeepLocal` named identically in engine and CLI steps.
