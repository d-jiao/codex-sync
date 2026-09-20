package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/d-jiao/codex-sync/internal/storage"
)

// A state file written before remote versions existed must still load, with an
// empty RemoteVersion that falls back to timestamp comparison.
func TestLegacyFileStateLoadsWithoutRemoteVersion(t *testing.T) {
	legacy := `{"path":"AGENTS.md","hash":"abc","size":3,"mod_time":"2026-01-02T03:04:05Z","uploaded":"2026-01-02T03:04:06Z"}`
	var fs FileState
	if err := json.Unmarshal([]byte(legacy), &fs); err != nil {
		t.Fatalf("legacy state failed to unmarshal: %v", err)
	}
	if fs.RemoteVersion != "" {
		t.Errorf("expected empty RemoteVersion, got %q", fs.RemoteVersion)
	}
	if fs.Hash != "abc" {
		t.Errorf("expected hash preserved, got %q", fs.Hash)
	}
}

func TestMarkRemotePersistsVersion(t *testing.T) {
	dir := t.TempDir()
	state, err := LoadStateFromDir(dir)
	if err != nil {
		t.Fatalf("LoadStateFromDir: %v", err)
	}

	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("hi"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	remoteTime := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	state.UpdateFile("file.txt", info, "hash")
	state.MarkRemote("file.txt", remoteTime, "etag-1")
	if err := state.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := LoadStateFromDir(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := reloaded.GetFile("file.txt")
	if got == nil {
		t.Fatal("expected file entry after reload")
	}
	if got.RemoteVersion != "etag-1" {
		t.Errorf("RemoteVersion = %q, want etag-1", got.RemoteVersion)
	}
	if !got.Uploaded.Equal(remoteTime) {
		t.Errorf("Uploaded = %v, want %v", got.Uploaded, remoteTime)
	}
}

func TestRemoteChangedPrefersVersionOverTimestamp(t *testing.T) {
	uploaded := time.Now()
	tests := []struct {
		name  string
		state *FileState
		obj   storage.ObjectInfo
		want  bool
	}{
		{
			name:  "untracked file is always new",
			state: nil,
			obj:   storage.ObjectInfo{LastModified: uploaded.Add(-time.Hour), Version: "v1"},
			want:  true,
		},
		{
			name:  "same version is unchanged even when the remote clock jumps ahead",
			state: &FileState{Uploaded: uploaded, RemoteVersion: "v1"},
			obj:   storage.ObjectInfo{LastModified: uploaded.Add(time.Hour), Version: "v1"},
			want:  false,
		},
		{
			name:  "new version is changed even when the remote clock lags",
			state: &FileState{Uploaded: uploaded, RemoteVersion: "v1"},
			obj:   storage.ObjectInfo{LastModified: uploaded.Add(-time.Hour), Version: "v2"},
			want:  true,
		},
		{
			name:  "without versions it falls back to timestamps",
			state: &FileState{Uploaded: uploaded},
			obj:   storage.ObjectInfo{LastModified: uploaded.Add(time.Second)},
			want:  true,
		},
		{
			name:  "older timestamp without versions is unchanged",
			state: &FileState{Uploaded: uploaded},
			obj:   storage.ObjectInfo{LastModified: uploaded.Add(-time.Second)},
			want:  false,
		},
		{
			name:  "a state with no version cannot trust the remote version alone",
			state: &FileState{Uploaded: uploaded},
			obj:   storage.ObjectInfo{LastModified: uploaded.Add(-time.Second), Version: "v9"},
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := remoteChanged(tc.obj, tc.state); got != tc.want {
				t.Errorf("remoteChanged = %v, want %v", got, tc.want)
			}
		})
	}
}

// A provider whose timestamps never advance (coarse or stuck clock) must still
// produce a download when another device replaces the object.
func TestPullDetectsRemoteChangeWhenTimestampsDoNotAdvance(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.store.freezeTime = time.Now().Add(-time.Hour)

	writeFile(t, env.claudeDir, "AGENTS.md", "# V1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	// Pull now must be a no-op: the object we just uploaded is the one we have.
	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("first pull: %v", err)
	}
	if len(result.Downloaded) != 0 {
		t.Fatalf("expected no downloads, got %v", result.Downloaded)
	}

	// Another device replaces the object. Its timestamp is unchanged, so only
	// the version reveals the update.
	other := newPeer(t, env)
	writeFile(t, other.claudeDir, "AGENTS.md", "# V2 from the other device")
	if _, err := other.syncer.Push(ctx); err != nil {
		t.Fatalf("other push: %v", err)
	}

	result, err = env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("second pull: %v", err)
	}
	if len(result.Downloaded) != 1 {
		t.Fatalf("expected 1 download, got %v (errors: %v)", result.Downloaded, result.Errors)
	}
	if got := readFile(t, env.claudeDir, "AGENTS.md"); got != "# V2 from the other device" {
		t.Errorf("local content = %q", got)
	}
}

// A file that grows while it is being uploaded must not be recorded as synced:
// the remote holds the shorter prefix, so the next push has to retry.
func TestPushDoesNotMarkFileSyncedWhenItChangesDuringUpload(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "sessions/rollout-2026.jsonl", "line one\n")
	appended := false
	env.store.onUpload = func(key string) {
		if appended {
			return
		}
		appended = true
		f, err := os.OpenFile(filepath.Join(env.claudeDir, "sessions/rollout-2026.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Errorf("open for append: %v", err)
			return
		}
		defer func() { _ = f.Close() }()
		if _, err := f.WriteString("line two\n"); err != nil {
			t.Errorf("append: %v", err)
		}
	}

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(result.Uploaded) != 0 {
		t.Errorf("expected no file reported as uploaded, got %v", result.Uploaded)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error about the concurrent change, got %v", result.Errors)
	}

	// The next push must still see the file as pending.
	env.store.onUpload = nil
	changes, err := env.syncer.Status(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(changes) != 1 || changes[0].Path != "sessions/rollout-2026.jsonl" {
		t.Fatalf("expected history.md still pending, got %v", changes)
	}

	result, err = env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("second push: %v", err)
	}
	if len(result.Uploaded) != 1 {
		t.Fatalf("expected the retry to upload, got %v (errors %v)", result.Uploaded, result.Errors)
	}
}
