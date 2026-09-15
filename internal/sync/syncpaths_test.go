package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d-jiao/codex-sync/internal/config"
)

// TestSyncerHonorsCustomSyncPaths is the regression test for issue #64: the
// sync_paths override was wired into Config but never read by the syncer, so a
// user-supplied path list was silently ignored by push, status, and diff.
func TestSyncerHonorsCustomSyncPaths(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"CLAUDE.md", "my-extra-file.md", "unlisted.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.Config{SyncPaths: []string{"CLAUDE.md", "my-extra-file.md"}}
	s := &Syncer{cfg: cfg, claudeDir: dir}

	got, err := GetLocalFiles(dir, s.syncPaths(), s.isExcluded)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := got["my-extra-file.md"]; !ok {
		t.Errorf("custom sync_paths entry not synced; got %v", sortedKeys(got))
	}
	if _, ok := got["unlisted.md"]; ok {
		t.Errorf("file outside sync_paths was synced; got %v", sortedKeys(got))
	}
}

// TestSyncerScopeCeilingBlocksPluginsLeak verifies that a materialized
// sync_paths list cannot widen a sessions-scoped config back into plugins/,
// which bundles non-portable node_modules and .venv trees.
func TestSyncerScopeCeilingBlocksPluginsLeak(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "plugins", "node_modules"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugins", "node_modules", "big.js"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "projects", "a.jsonl"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Scope:     config.ScopeSessions,
		SyncPaths: append([]string{}, config.SyncPaths...), // what `paths add` writes
	}
	s := &Syncer{cfg: cfg, claudeDir: dir}

	got, err := GetLocalFiles(dir, s.syncPaths(), s.isExcluded)
	if err != nil {
		t.Fatal(err)
	}

	for p := range got {
		if filepath.ToSlash(p) == "plugins/node_modules/big.js" {
			t.Fatalf("sessions scope uploaded plugins/: %v", sortedKeys(got))
		}
	}
	if _, ok := got["projects/a.jsonl"]; !ok {
		t.Errorf("in-scope path missing: %v", sortedKeys(got))
	}
}

// TestGetLocalFilesRejectsTraversingSyncPath ensures a sync_paths entry cannot
// walk outside ~/.claude. Sync paths became user-controlled input once the
// override was honored, and a traversing entry would otherwise turn files like
// ~/.ssh/id_rsa into remote objects.
func TestGetLocalFilesRejectsTraversingSyncPath(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0700); err != nil {
		t.Fatal(err)
	}

	secretDir := filepath.Join(root, ".ssh")
	if err := os.MkdirAll(secretDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "id_rsa"), []byte("PRIVATE"), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := GetLocalFiles(claudeDir, []string{"../.ssh", "../.ssh/id_rsa"})
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 0 {
		t.Errorf("traversing sync path escaped the sync root: %v", sortedKeys(got))
	}
}

func sortedKeys(m map[string]os.FileInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
