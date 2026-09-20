package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-jiao/codex-sync/internal/config"
	"github.com/d-jiao/codex-sync/internal/storage"
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
		trashDir:  filepath.Join(stateDir, "trash"),
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
	err := env.syncer.downloadFile(context.Background(), "auth.json",
		storage.ObjectInfo{Key: env.syncer.remoteKey("auth.json")}, nil)
	if err == nil {
		t.Fatal("downloadFile must refuse protected paths")
	}
}
