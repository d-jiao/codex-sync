package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Recycle-bin copies live under the same prefix as the synced objects, so
// every listing pull works from has to skip them. Otherwise a deleted file
// returns on the next pull as a literal `_trash/...` file.
func TestPullIgnoresTheRemoteRecycleBin(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	// A forced push on another device left a copy behind before deleting.
	key := env.syncer.remoteKey("AGENTS.md")
	data, err := env.store.Download(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.Upload(ctx, TrashPrefix+"20260920T120000Z/"+key, data); err != nil {
		t.Fatal(err)
	}

	peer := newPeer(t, env)
	result, err := peer.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Errorf("the recycle bin must not be reported as an unknown path: %v", result.Errors)
	}
	for _, p := range result.Downloaded {
		if strings.HasPrefix(p, "_trash") {
			t.Errorf("downloaded a recycle-bin copy as a file: %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(peer.claudeDir, "_trash")); !os.IsNotExist(err) {
		t.Error("a _trash directory must never be written into the Codex home")
	}
}

// The empty-remote guard exists so a bucket that lost its objects cannot wipe
// a device. Copies in the recycle bin are not files anyone syncs, so they must
// not make such a remote look populated.
func TestPullTreatsARemoteOfOnlyTrashAsEmpty(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.syncer.SetTrashDir(filepath.Join(env.stateDir, "trash"))

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	// Everything the bucket held is now a recycle-bin copy.
	objs, err := env.store.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, obj := range objs {
		data, err := env.store.Download(ctx, obj.Key)
		if err != nil {
			t.Fatal(err)
		}
		if err := env.store.Upload(ctx, TrashPrefix+"20260920T120000Z/"+obj.Key, data); err != nil {
			t.Fatal(err)
		}
		if err := env.store.Delete(ctx, obj.Key); err != nil {
			t.Fatal(err)
		}
	}

	result, err := env.syncer.Pull(ctx)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Errorf("a remote holding only the recycle bin removed local files: %v", result.Removed)
	}
	if _, err := os.Stat(filepath.Join(env.claudeDir, "AGENTS.md")); err != nil {
		t.Errorf("the local file must survive: %v", err)
	}
}

// Diff describes the two sides to the user; recycle-bin copies are neither.
func TestDiffIgnoresTheRemoteRecycleBin(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	key := env.syncer.remoteKey("AGENTS.md")
	data, err := env.store.Download(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.Upload(ctx, TrashPrefix+"20260920T120000Z/"+key, data); err != nil {
		t.Fatal(err)
	}

	entries, err := env.syncer.Diff(ctx)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Path, "_trash") {
			t.Errorf("diff reported a recycle-bin copy: %+v", e)
		}
	}
}
