package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const conflicted = "sessions/2026/01/01/rollout-c.jsonl"

// twoDevicesWithConflict: A pushes a rollout, B pulls it, both append to it,
// A pushes, B pulls. Returns A and B with B holding its own copy plus the
// remote copy as a `<path>.conflict.<ts>` sidecar (relative path returned).
func twoDevicesWithConflict(t *testing.T) (a, b *testEnv, sidecar string) {
	t.Helper()
	ctx := context.Background()
	a = setupTestEnv(t)
	b = newPeer(t, a)
	writeFile(t, a.claudeDir, conflicted, rollout)
	if _, err := a.syncer.Push(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := b.syncer.Pull(ctx); err != nil {
		t.Fatal(err)
	}
	writeFile(t, a.claudeDir, conflicted, rollout+`{"type":"turn","device":"A"}`+"\n")
	writeFile(t, b.claudeDir, conflicted, rollout+`{"type":"turn","device":"B"}`+"\n")
	time.Sleep(10 * time.Millisecond) // remote mtime must land after B's pull
	if _, err := a.syncer.Push(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := b.syncer.Pull(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0] != conflicted {
		t.Fatalf("Conflicts = %v, want [%s]", res.Conflicts, conflicted)
	}
	sidecars := conflictSidecarsOnDisk(t, b.claudeDir, conflicted)
	if len(sidecars) != 1 {
		t.Fatalf("expected one sidecar, found %v", sidecars)
	}
	return a, b, sidecars[0]
}

// conflictSidecarsOnDisk lists `<relPath>.conflict.*` files as relative paths.
func conflictSidecarsOnDisk(t *testing.T, base, relPath string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(base, filepath.Dir(relPath)))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	prefix := filepath.Base(relPath) + ".conflict."
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			out = append(out, filepath.ToSlash(filepath.Join(filepath.Dir(relPath), e.Name())))
		}
	}
	return out
}

func TestConflictSidecarIsNeverTracked(t *testing.T) {
	_, b, sidecar := twoDevicesWithConflict(t)
	if b.syncer.state.GetFile(sidecar) != nil {
		t.Errorf("sidecar %s must not have a state entry", sidecar)
	}
	if b.syncer.state.GetFile(conflicted) == nil {
		t.Error("the conflicted file itself stays tracked")
	}
}

func TestConflictSidecarSurvivesNextPull(t *testing.T) {
	_, b, sidecar := twoDevicesWithConflict(t)
	res, err := b.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Removed {
		if strings.Contains(p, ".conflict.") {
			t.Errorf("pull must not trash a conflict sidecar: %v", res.Removed)
		}
	}
	if _, err := os.Stat(filepath.Join(b.claudeDir, sidecar)); err != nil {
		t.Errorf("sidecar must still be on disk after the next pull: %v", err)
	}
}

func TestConflictSidecarSurvivesNextPullWithLegacyStateEntry(t *testing.T) {
	// State written by older builds tracked the sidecar; pull must still not
	// treat it as a vanished remote file.
	_, b, sidecar := twoDevicesWithConflict(t)
	full := filepath.Join(b.claudeDir, sidecar)
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := HashFile(full)
	b.syncer.state.UpdateFile(sidecar, info, hash)
	b.syncer.state.MarkUploaded(sidecar)

	res, err := b.syncer.Pull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Removed) != 0 {
		t.Errorf("Removed = %v, want none", res.Removed)
	}
	if _, err := os.Stat(full); err != nil {
		t.Errorf("sidecar must still be on disk: %v", err)
	}
}

func TestPreviewPullDoesNotListConflictSidecar(t *testing.T) {
	_, b, _ := twoDevicesWithConflict(t)
	preview, err := b.syncer.PreviewPull(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range preview.WouldRemove {
		if strings.Contains(f.Path, ".conflict.") {
			t.Errorf("WouldRemove lists a sidecar: %+v", preview.WouldRemove)
		}
	}
	for _, f := range preview.LocalOnlyFiles {
		if strings.Contains(f.Path, ".conflict.") {
			t.Errorf("LocalOnlyFiles lists a sidecar: %+v", preview.LocalOnlyFiles)
		}
	}
}

func TestPushNeverUploadsConflictSidecar(t *testing.T) {
	_, b, sidecar := twoDevicesWithConflict(t)
	// Even an untracked sidecar (state lost, or written by an older build and
	// later dropped from state) must be invisible to the push scan.
	b.syncer.state.RemoveFile(sidecar)
	if _, err := b.syncer.Push(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.store.mu.Lock()
	defer b.store.mu.Unlock()
	for key := range b.store.objects {
		if strings.Contains(key, ".conflict.") {
			t.Errorf("sidecar reached the remote: %s", key)
		}
	}
	if b.syncer.state.GetFile(sidecar) != nil {
		t.Error("push must not start tracking the sidecar")
	}
}

func TestPushRefusesFileWithLiveConflictSidecar(t *testing.T) {
	_, b, sidecar := twoDevicesWithConflict(t)
	ctx := context.Background()
	key := b.syncer.remoteKey(conflicted)
	before, err := b.store.Download(ctx, key)
	if err != nil {
		t.Fatal(err)
	}

	res, err := b.syncer.Push(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Uploaded {
		if p == conflicted {
			t.Fatalf("push uploaded a file with a live sidecar: %v", res.Uploaded)
		}
	}
	want := "unresolved conflict for " + conflicted + "; run 'codex-sync conflicts'"
	found := false
	for _, e := range res.Errors {
		if e.Error() == want {
			found = true
		}
	}
	if !found {
		t.Errorf("Errors = %v, want one equal to %q", res.Errors, want)
	}
	after, err := b.store.Download(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("remote object must be untouched while the conflict is unresolved")
	}

	// Once the sidecar is gone (what resolving the conflict does on disk),
	// the next push publishes the local version.
	if err := os.Remove(filepath.Join(b.claudeDir, sidecar)); err != nil {
		t.Fatal(err)
	}
	res, err = b.syncer.Push(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Errorf("Errors = %v, want none", res.Errors)
	}
	if len(res.Uploaded) != 1 || res.Uploaded[0] != conflicted {
		t.Errorf("Uploaded = %v, want [%s]", res.Uploaded, conflicted)
	}
}

func TestPushWithLiveSidecarStillUploadsOtherFiles(t *testing.T) {
	_, b, _ := twoDevicesWithConflict(t)
	const other = "sessions/2026/01/02/rollout-other.jsonl"
	writeFile(t, b.claudeDir, other, rollout)
	res, err := b.syncer.Push(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Uploaded) != 1 || res.Uploaded[0] != other {
		t.Errorf("Uploaded = %v, want [%s]", res.Uploaded, other)
	}
	if len(res.Errors) != 1 {
		t.Errorf("Errors = %v, want exactly the unresolved-conflict error", res.Errors)
	}
}
