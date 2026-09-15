package paths

import (
	"testing"

	"github.com/d-jiao/codex-sync/internal/config"
)

func TestValidatePath(t *testing.T) {
	valid := []string{
		"CLAUDE.md",
		"agents",
		"projects/foo",
		"a/b/c.json",
		"./CLAUDE.md",
	}
	for _, p := range valid {
		if err := ValidatePath(p); err != nil {
			t.Errorf("ValidatePath(%q) = %v, want nil", p, err)
		}
	}

	invalid := []string{
		"",
		"   ",
		"..",
		"../.ssh",
		"../../etc/passwd",
		"projects/../../.ssh/id_rsa",
		"/etc/passwd",
		"/",
	}
	for _, p := range invalid {
		if err := ValidatePath(p); err == nil {
			t.Errorf("ValidatePath(%q) = nil, want error", p)
		}
	}
}

// TestAddRejectsTraversal ensures the CLI cannot persist a sync path that would
// read outside ~/.claude on push or write outside it on pull.
func TestAddRejectsTraversal(t *testing.T) {
	m := NewManager(nil, nil, "/tmp/test", config.ScopeFull)
	before := len(m.SyncPaths())

	result := m.Add("../.ssh")
	if result.Invalid == nil {
		t.Error("Add(\"../.ssh\") should be rejected")
	}
	if result.Added {
		t.Error("traversing path must not be added")
	}
	if len(m.SyncPaths()) != before {
		t.Errorf("sync paths mutated on rejected add: %v", m.SyncPaths())
	}
}

// TestAddRejectsOutOfScopePathUnderSessions prevents writing config that the
// syncer would silently discard, since sessions scope intersects sync_paths.
func TestAddRejectsOutOfScopePathUnderSessions(t *testing.T) {
	m := NewManager(nil, nil, "/tmp/test", config.ScopeSessions)

	result := m.Add("plugins")
	if !result.OutOfScope {
		t.Error("adding plugins under sessions scope should report OutOfScope")
	}
	if result.Added {
		t.Error("out-of-scope path must not be added")
	}

	// An in-scope path is still addable.
	if got := m.Add("sessions"); got.OutOfScope {
		t.Error("sessions is within sessions scope and should be addable")
	}
}

// TestManagerDefaultsTrackScope guards the drift that left workflows/ shown by
// `paths list` but never synced, and full-scope defaults shown to sessions users.
func TestManagerDefaultsTrackScope(t *testing.T) {
	full := NewManager(nil, nil, "/tmp/test", config.ScopeFull)
	if len(full.SyncPaths()) != len(config.SyncPaths) {
		t.Errorf("full scope defaults = %d paths, want %d (config.SyncPaths)",
			len(full.SyncPaths()), len(config.SyncPaths))
	}

	sessions := NewManager(nil, nil, "/tmp/test", config.ScopeSessions)
	if len(sessions.SyncPaths()) != len(config.SessionSyncPaths) {
		t.Errorf("sessions scope defaults = %d paths, want %d (config.SessionSyncPaths)",
			len(sessions.SyncPaths()), len(config.SessionSyncPaths))
	}
	for _, p := range sessions.SyncPaths() {
		if p == "plugins" {
			t.Error("sessions scope must not list plugins/ as a default")
		}
	}
}
