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
