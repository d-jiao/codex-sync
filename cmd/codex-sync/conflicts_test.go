package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d-jiao/codex-sync/internal/sync"
)

const (
	conflictRel = "sessions/2026/01/01/rollout-a.jsonl"
	localTurns  = "v1\nlocal-turn\n"
	remoteTurns = "v1\nremote-turn\n"
)

// resolutionFixture builds a base dir holding a conflicted rollout: the local
// copy, the remote copy saved as a sidecar, and a state entry recording the
// last version both sides had in common (v1).
func resolutionFixture(t *testing.T) (baseDir string, state *sync.SyncState, c conflictFile) {
	t.Helper()
	baseDir = filepath.Join(t.TempDir(), ".codex")
	original := filepath.Join(baseDir, filepath.FromSlash(conflictRel))
	if err := os.MkdirAll(filepath.Dir(original), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := sync.LoadStateFromDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(original)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := sync.HashFile(original)
	if err != nil {
		t.Fatal(err)
	}
	state.UpdateFile(conflictRel, info, hash)
	state.MarkUploaded(conflictRel)

	// Both sides diverged from v1; the remote side arrived as a sidecar.
	if err := os.WriteFile(original, []byte(localTurns), 0600); err != nil {
		t.Fatal(err)
	}
	sidecar := original + ".conflict.20260101-000000"
	if err := os.WriteFile(sidecar, []byte(remoteTurns), 0600); err != nil {
		t.Fatal(err)
	}
	return baseDir, state, conflictFile{ConflictPath: sidecar, OriginalPath: original, Timestamp: "20260101-000000"}
}

func pendingChanges(t *testing.T, state *sync.SyncState, baseDir string) map[string]string {
	t.Helper()
	changes, err := state.DetectChanges(baseDir, []string{"sessions"})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, ch := range changes {
		out[ch.Path] = ch.Action
	}
	return out
}

func TestKeepLocalLeavesTheKeptFilePendingForPush(t *testing.T) {
	baseDir, state, c := resolutionFixture(t)
	if err := batchResolveConflicts([]conflictFile{c}, "local", baseDir, state); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.ConflictPath); !os.IsNotExist(err) {
		t.Fatal("sidecar should be removed")
	}
	if data, _ := os.ReadFile(c.OriginalPath); string(data) != localTurns {
		t.Fatalf("local content changed: %q", data)
	}
	if got := pendingChanges(t, state, baseDir)[conflictRel]; got != "modify" {
		t.Errorf("after keep-local the file must be pending as \"modify\" so push publishes it, got %q", got)
	}
	if f := state.GetFile(conflictRel); f == nil || f.Uploaded.IsZero() {
		t.Error("state must keep an uploaded timestamp so the next pull does not re-conflict")
	}
}

func TestKeepRemoteLeavesNothingPending(t *testing.T) {
	baseDir, state, c := resolutionFixture(t)
	if err := batchResolveConflicts([]conflictFile{c}, "remote", baseDir, state); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(c.OriginalPath); string(data) != remoteTurns {
		t.Fatalf("local should now hold the remote version: %q", data)
	}
	if got := pendingChanges(t, state, baseDir)[conflictRel]; got != "" {
		t.Errorf("after keep-remote nothing should be pending, got %q", got)
	}
}
