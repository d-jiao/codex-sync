package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The default push must never remove remote data. A local deletion is
// reported so the user can apply it deliberately.
func TestPushWithoutForceKeepsRemoteFile(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "AGENTS.md", "# keep me")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("initial push: %v", err)
	}
	if err := os.Remove(filepath.Join(env.claudeDir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(result.Deleted) != 0 {
		t.Errorf("default push deleted remote objects: %v", result.Deleted)
	}
	if len(result.PendingDeletes) != 1 || result.PendingDeletes[0] != "AGENTS.md" {
		t.Fatalf("expected AGENTS.md reported as pending, got %v", result.PendingDeletes)
	}
	if len(result.Errors) != 0 {
		t.Errorf("a skipped deletion is not a failure: %v", result.Errors)
	}

	objs, _ := env.store.ListUserObjects(ctx)
	if len(objs) != 1 {
		t.Fatalf("expected the remote object to survive, got %d objects", len(objs))
	}
	if env.syncer.state.GetFile("AGENTS.md") == nil {
		t.Error("state entry must stay so the deletion is reported again next time")
	}
}

// Device B uploads a new version; device A, whose state still points at the
// old one, deletes the file locally. A forced push must keep B's version.
func TestForcedPushKeepsRemoteVersionAnotherDeviceReplaced(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.syncer.SetAllowRemoteDeletes(true)

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1 from A")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("A push: %v", err)
	}

	peer := newPeer(t, env)
	if _, err := peer.syncer.Pull(ctx); err != nil {
		t.Fatalf("B pull: %v", err)
	}
	writeFile(t, peer.claudeDir, "AGENTS.md", "# v2 from B")
	if _, err := peer.syncer.Push(ctx); err != nil {
		t.Fatalf("B push: %v", err)
	}

	if err := os.Remove(filepath.Join(env.claudeDir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("A push: %v", err)
	}
	if len(result.Deleted) != 0 {
		t.Errorf("A deleted a newer remote version: %v", result.Deleted)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected one delete-conflict error, got %v", result.Errors)
	}
	if !strings.Contains(result.Errors[0].Error(), "AGENTS.md") {
		t.Errorf("error should name the file: %v", result.Errors[0])
	}
	if objs, _ := env.store.ListUserObjects(ctx); len(objs) != 1 {
		t.Errorf("B's version must survive, got %d objects", len(objs))
	}
}

// The precondition is the second line of defence: even when state agrees with
// the listing, the adapter refuses a delete whose revision moved underneath it.
func TestForcedPushRespectsDeletePrecondition(t *testing.T) {
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
	// The object is replaced between the listing and the delete: its bytes,
	// timestamp and our state all still line up, only the revision moved.
	key := env.syncer.remoteKey("AGENTS.md")
	env.store.onDelete = func(string) { env.store.setVersion(key, "moved") }

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(result.Deleted) != 0 {
		t.Errorf("delete should have been refused, got %v", result.Deleted)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected a precondition error, got %v", result.Errors)
	}
	if objs, _ := env.store.ListUserObjects(ctx); len(objs) != 1 {
		t.Errorf("object must survive a failed precondition, got %d", len(objs))
	}
}

// A file already gone from the remote just leaves state: nothing to delete.
func TestForcedPushForgetsFileAlreadyGoneFromRemote(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.syncer.SetAllowRemoteDeletes(true)

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := env.store.Delete(ctx, env.syncer.remoteKey("AGENTS.md")); err != nil {
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
		t.Errorf("unexpected errors: %v", result.Errors)
	}
	if env.syncer.state.GetFile("AGENTS.md") != nil {
		t.Error("state should no longer track a file that is gone on both sides")
	}
}

// Not every S3-compatible server honours If-Match on a DELETE; some accept the
// header and remove the object anyway. The revision is therefore re-read
// immediately before each delete, so a copy another device uploaded after the
// listing survives even when the precondition is worthless.
func TestForcedPushRereadsRevisionWhenPreconditionsAreIgnored(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.syncer.SetAllowRemoteDeletes(true)
	env.store.ignorePreconditions = true

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := os.Remove(filepath.Join(env.claudeDir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	// Another device replaces the object after the delete listing was taken.
	key := env.syncer.remoteKey("AGENTS.md")
	env.store.afterList = func() { env.store.setVersion(key, "written-by-peer") }

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(result.Deleted) != 0 {
		t.Errorf("deleted a revision this device never saw: %v", result.Deleted)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected one delete-conflict error, got %v", result.Errors)
	}
	if objs, _ := env.store.ListUserObjects(ctx); len(objs) != 1 {
		t.Errorf("the peer's object must survive, got %d objects", len(objs))
	}
}

// A server that reports no revision (some WebDAV deployments omit ETags) can
// only be guarded by timestamps, and the user is told which deletes carried
// that weaker guarantee.
func TestForcedPushReportsDeletesTheProviderCouldNotVerify(t *testing.T) {
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
	env.store.omitVersions = true

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
	if len(result.UnverifiedDeletes) != 1 || result.UnverifiedDeletes[0] != "AGENTS.md" {
		t.Fatalf("expected AGENTS.md reported as unverified, got %v", result.UnverifiedDeletes)
	}
}

// With a revision available the delete is fully guarded, so nothing is
// reported as unverified.
func TestForcedPushReportsNoUnverifiedDeletesWithRevisions(t *testing.T) {
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

	result, err := env.syncer.Push(ctx)
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if len(result.UnverifiedDeletes) != 0 {
		t.Errorf("a revision-guarded delete is not unverified: %v", result.UnverifiedDeletes)
	}
}
