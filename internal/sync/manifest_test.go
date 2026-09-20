package sync

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func manifestKey() string { return ManifestKey + ".age" }

// Remotes written before manifests existed have none; pull must carry on.
func TestPullWithoutManifestSucceeds(t *testing.T) {
	env := setupTestEnv(t)
	seedRemote(t, env, "AGENTS.md", "# remote")

	if _, ok := env.store.objects[manifestKey()]; ok {
		t.Fatal("test setup should not have produced a manifest")
	}

	result, err := env.syncer.Pull(context.Background())
	if err != nil {
		t.Fatalf("pull without a manifest failed: %v", err)
	}
	if len(result.Downloaded) != 1 {
		t.Errorf("expected 1 download, got %v (errors %v)", result.Downloaded, result.Errors)
	}
}

// A manifest that is listed but unreadable means mtimes would silently be
// wrong. Pull stops instead of guessing, and touches nothing.
func TestPullFailsWhenListedManifestCannotBeRead(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}

	peer := newPeer(t, env)
	peer.store.downloadErr = map[string]error{manifestKey(): fmt.Errorf("network is down")}

	result, err := peer.syncer.Pull(ctx)
	if err == nil {
		t.Fatal("expected pull to fail when the manifest cannot be read")
	}
	if !strings.Contains(err.Error(), "manifest") {
		t.Errorf("error should mention the manifest: %v", err)
	}
	if result != nil && len(result.Downloaded) != 0 {
		t.Errorf("no file should have been downloaded, got %v", result.Downloaded)
	}
	if len(peer.syncer.state.Files) != 0 {
		t.Error("state must not record anything from a failed pull")
	}
}

func TestPullFailsWhenManifestIsCorrupt(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := env.store.Upload(ctx, manifestKey(), []byte("not an age file")); err != nil {
		t.Fatal(err)
	}

	peer := newPeer(t, env)
	if _, err := peer.syncer.Pull(ctx); err == nil {
		t.Fatal("expected pull to fail on a corrupt manifest")
	}
}

// A failed manifest upload must be reported, must not discard the uploads that
// did succeed, and must be retried on the next push even when no file changed.
func TestPushReportsManifestUploadFailureAndRetriesIt(t *testing.T) {
	env := setupTestEnv(t)
	ctx := context.Background()
	env.store.uploadErr = map[string]error{manifestKey(): fmt.Errorf("storage rejected the manifest")}

	writeFile(t, env.claudeDir, "AGENTS.md", "# v1")
	result, err := env.syncer.Push(ctx)
	if err == nil {
		t.Fatal("expected push to report the manifest failure")
	}
	if !strings.Contains(err.Error(), "manifest") {
		t.Errorf("error should mention the manifest: %v", err)
	}
	if len(result.Uploaded) != 1 {
		t.Fatalf("the file upload itself succeeded; got %v", result.Uploaded)
	}
	if env.syncer.state.GetFile("AGENTS.md") == nil {
		t.Fatal("a successful file upload must stay recorded so it is not re-sent")
	}

	// Nothing changed locally, but the manifest is still owed.
	env.store.uploadErr = nil
	if _, err := env.syncer.Push(ctx); err != nil {
		t.Fatalf("retry push: %v", err)
	}
	if _, ok := env.store.objects[manifestKey()]; !ok {
		t.Error("the next push should have retried the manifest upload")
	}
}
