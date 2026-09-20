package sync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d-jiao/codex-sync/internal/storage"
)

// copylessStorage exposes only storage.Storage (plus conditional deletes), so
// the recycle bin has to fall back to download-then-upload the way it would
// against an adapter with no server-side copy.
type copylessStorage struct{ inner *mockStorage }

func (c copylessStorage) Upload(ctx context.Context, key string, data []byte) error {
	return c.inner.Upload(ctx, key, data)
}
func (c copylessStorage) Download(ctx context.Context, key string) ([]byte, error) {
	return c.inner.Download(ctx, key)
}
func (c copylessStorage) Delete(ctx context.Context, key string) error {
	return c.inner.Delete(ctx, key)
}
func (c copylessStorage) DeleteBatch(ctx context.Context, keys []string) error {
	return c.inner.DeleteBatch(ctx, keys)
}
func (c copylessStorage) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	return c.inner.List(ctx, prefix)
}
func (c copylessStorage) Head(ctx context.Context, key string) (*storage.ObjectInfo, error) {
	return c.inner.Head(ctx, key)
}
func (c copylessStorage) BucketExists(ctx context.Context) (bool, error) {
	return c.inner.BucketExists(ctx)
}
func (c copylessStorage) DeleteIfUnchanged(ctx context.Context, key, expectedVersion string) error {
	return c.inner.DeleteIfUnchanged(ctx, key, expectedVersion)
}

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

// trashedKeys returns the recycle-bin copies currently in the store.
func trashedKeys(t *testing.T, store *mockStorage) []string {
	t.Helper()
	objs, err := store.List(context.Background(), TrashPrefix)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(objs))
	for _, obj := range objs {
		keys = append(keys, obj.Key)
	}
	return keys
}

// A forced push deletes remote data, and the version checks that guard it can
// be defeated by a server that ignores preconditions. Every object it removes
// is therefore copied into the recycle bin first, still encrypted.
func TestForcedPushKeepsACopyBeforeDeleting(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.syncer.SetAllowRemoteDeletes(true)

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	key := env.syncer.remoteKey("AGENTS.md")
	original, err := env.store.Download(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(env.claudeDir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(result.Deleted) != 1 {
		t.Fatalf("expected the delete to go through, got %v", result.Deleted)
	}

	keys := trashedKeys(t, env.store)
	if len(keys) != 1 {
		t.Fatalf("expected one recycle-bin copy, got %v", keys)
	}
	if !strings.HasSuffix(keys[0], "/"+key) {
		t.Errorf("copy %q should keep the original key under the batch", keys[0])
	}
	if result.TrashBatch == "" || !strings.Contains(keys[0], result.TrashBatch) {
		t.Errorf("result should name the batch holding the copies, got %q", result.TrashBatch)
	}
	copied, err := env.store.Download(ctx, keys[0])
	if err != nil {
		t.Fatalf("recycle-bin copy is unreadable: %v", err)
	}
	if string(copied) != string(original) {
		t.Error("the copy must be the original ciphertext, byte for byte")
	}
	if _, err := env.store.Download(ctx, key); err == nil {
		t.Error("the live object should be gone")
	}
}

// An adapter with no server-side copy still has to produce the backup, by
// downloading the object and uploading it to the recycle bin.
func TestForcedPushCopiesThroughDownloadWhenTheProviderCannot(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.syncer.storage = copylessStorage{inner: env.store}
	env.syncer.SetAllowRemoteDeletes(true)

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := os.Remove(filepath.Join(env.claudeDir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if keys := trashedKeys(t, env.store); len(keys) != 1 {
		t.Fatalf("expected the fallback to produce one copy, got %v", keys)
	}
}

// A backup that cannot be taken is the one case where the delete must not
// proceed: without it the removal would be irreversible. Here neither route
// into the recycle bin works, as with a bucket that has gone read-only.
func TestForcedPushRefusesToDeleteWhenTheCopyFails(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.syncer.SetAllowRemoteDeletes(true)

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := os.Remove(filepath.Join(env.claudeDir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	env.store.copyErr = errors.New("bucket is read-only")
	env.store.downloadErr = map[string]error{
		env.syncer.remoteKey("AGENTS.md"): errors.New("bucket is read-only"),
	}

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(result.Deleted) != 0 {
		t.Errorf("deleted without a backup: %v", result.Deleted)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected one error, got %v", result.Errors)
	}
	if !strings.Contains(result.Errors[0].Error(), "AGENTS.md") {
		t.Errorf("error should name the file: %v", result.Errors[0])
	}
	if _, err := env.store.Head(ctx, env.syncer.remoteKey("AGENTS.md")); err != nil {
		t.Error("the remote object must survive a failed backup")
	}
	if env.syncer.state.GetFile("AGENTS.md") == nil {
		t.Error("state must keep tracking a file whose deletion did not happen")
	}
}

// A push that removes nothing must not leave an empty batch behind.
func TestPushWithoutDeletesWritesNoRecycleBin(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.syncer.SetAllowRemoteDeletes(true)

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if result.TrashBatch != "" {
		t.Errorf("no deletes happened, so no batch should be reported: %q", result.TrashBatch)
	}
	if keys := trashedKeys(t, env.store); len(keys) != 0 {
		t.Errorf("expected an empty recycle bin, got %v", keys)
	}
}

// forcePushDelete removes relPath locally and pushes with deletes allowed,
// returning the recycle-bin batch that captured it.
func forcePushDelete(t *testing.T, env *testEnv, relPath string) string {
	t.Helper()
	if err := os.Remove(filepath.Join(env.claudeDir, relPath)); err != nil {
		t.Fatal(err)
	}
	env.syncer.SetAllowRemoteDeletes(true)
	result, err := env.syncer.Push(context.Background())
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Fatalf("push errors: %v", result.Errors)
	}
	if result.TrashBatch == "" {
		t.Fatal("expected a recycle-bin batch")
	}
	return result.TrashBatch
}

// Copies are only useful if they can be found, so the bin is listed as
// batches with the counts and sizes needed to pick one.
func TestListRemoteTrashGroupsByBatch(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	writeFile(t, env.claudeDir, "sessions/rollout-a.jsonl", `{"id":"a"}`)
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	batch := forcePushDelete(t, env, "AGENTS.md")

	batches, err := env.syncer.ListRemoteTrash(ctx)
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	if len(batches) != 1 {
		t.Fatalf("expected one batch, got %+v", batches)
	}
	if batches[0].Batch != batch {
		t.Errorf("batch = %q, want %q", batches[0].Batch, batch)
	}
	if batches[0].Files != 1 {
		t.Errorf("files = %d, want 1", batches[0].Files)
	}
	if batches[0].Size <= 0 {
		t.Errorf("size = %d, want the stored ciphertext size", batches[0].Size)
	}
	if batches[0].Deleted.IsZero() {
		t.Error("batch should carry when the copies were written")
	}
}

// An empty bin lists nothing rather than failing.
func TestListRemoteTrashWithNothingInIt(t *testing.T) {
	env := setupTestEnv(t)
	batches, err := env.syncer.ListRemoteTrash(context.Background())
	if err != nil {
		t.Fatalf("list trash: %v", err)
	}
	if len(batches) != 0 {
		t.Errorf("expected no batches, got %+v", batches)
	}
}

// Restoring puts the object back under its original key, so the next pull
// brings the file down again on every device.
func TestRestoreRemoteTrashPutsObjectsBack(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	key := env.syncer.remoteKey("AGENTS.md")
	original, err := env.store.Download(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	batch := forcePushDelete(t, env, "AGENTS.md")

	restored, skipped, err := env.syncer.RestoreRemoteTrash(ctx, batch)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("nothing was in the way, so nothing should be skipped: %v", skipped)
	}
	if len(restored) != 1 || restored[0] != "AGENTS.md" {
		t.Fatalf("expected AGENTS.md restored, got %v", restored)
	}
	back, err := env.store.Download(ctx, key)
	if err != nil {
		t.Fatalf("the object should be back under its own key: %v", err)
	}
	if string(back) != string(original) {
		t.Error("restored bytes differ from the original ciphertext")
	}

	peer := newPeer(t, env)
	if _, err := peer.syncer.Pull(ctx); err != nil {
		t.Fatalf("peer pull: %v", err)
	}
	if got := readFile(t, peer.claudeDir, "AGENTS.md"); got != "# v1" {
		t.Errorf("peer should see the restored file, got %q", got)
	}
}

// A restore must never overwrite a live object: the copy is older by
// definition, and whatever is there now came from a device that still had it.
func TestRestoreRemoteTrashKeepsALiveObject(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	batch := forcePushDelete(t, env, "AGENTS.md")

	// Another device re-created the file after the delete.
	peer := newPeer(t, env)
	writeFile(t, peer.claudeDir, "AGENTS.md", "# newer from B")
	if _, err := peer.syncer.Push(ctx); err != nil {
		t.Fatalf("peer push: %v", err)
	}

	restored, skipped, err := env.syncer.RestoreRemoteTrash(ctx, batch)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(restored) != 0 {
		t.Errorf("restored over a live object: %v", restored)
	}
	if len(skipped) != 1 || skipped[0] != "AGENTS.md" {
		t.Fatalf("expected AGENTS.md skipped, got %v", skipped)
	}
	fresh := newPeer(t, env)
	if _, err := fresh.syncer.Pull(ctx); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got := readFile(t, fresh.claudeDir, "AGENTS.md"); got != "# newer from B" {
		t.Errorf("the live version must win, got %q", got)
	}
}

// A batch name that matches nothing is a mistake worth reporting rather than
// a silent no-op.
func TestRestoreRemoteTrashRejectsUnknownBatch(t *testing.T) {
	env := setupTestEnv(t)
	if _, _, err := env.syncer.RestoreRemoteTrash(context.Background(), "20260101-000000Z"); err == nil {
		t.Error("expected an error for a batch that does not exist")
	}
}

// The batch name comes from the user, so it must not be able to reach
// outside the recycle bin.
func TestRestoreRemoteTrashRejectsATraversingBatch(t *testing.T) {
	env := setupTestEnv(t)
	for _, batch := range []string{"../_metadata", "a/b", ""} {
		if _, _, err := env.syncer.RestoreRemoteTrash(context.Background(), batch); err == nil {
			t.Errorf("expected %q to be refused", batch)
		}
	}
}
