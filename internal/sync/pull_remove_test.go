package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const (
	keep    = "sessions/2026/01/01/rollout-keep.jsonl"
	gone    = "sessions/2026/01/02/rollout-gone.jsonl"
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
