//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/d-jiao/codex-sync/internal/storage"

	// Register storage adapters
	_ "github.com/d-jiao/codex-sync/internal/storage/r2"
)

// Conditional delete is the second line of defence behind the version re-read
// in Syncer.applyDeletes, and whether a server actually enforces it is a
// property of that server, not of this code: several S3-compatible
// implementations accept If-Match on a DELETE and remove the object anyway.
// This test measures the answer for whatever endpoint the credentials point
// at, so the guarantee documented in docs/security.md is checked rather than
// assumed.
func TestConditionalDeleteIsEnforced(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	if getTestStorageConfig().AccountID == "" {
		t.Skip("R2 credentials not set - skipping integration test")
	}

	ctx := context.Background()
	store, err := storage.New(getTestStorageConfig())
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	conditional, ok := store.(storage.ConditionalDeleter)
	if !ok {
		t.Skipf("%T does not implement ConditionalDeleter", store)
	}

	key := fmt.Sprintf("_conformance/conditional-delete-%d", time.Now().UnixNano())
	if err := store.Upload(ctx, key, []byte("sentinel")); err != nil {
		t.Fatalf("failed to upload sentinel: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Delete(ctx, key); err != nil {
			t.Logf("failed to clean up %s: %v", key, err)
		}
	})

	info, err := store.Head(ctx, key)
	if err != nil {
		t.Fatalf("failed to head sentinel: %v", err)
	}
	if info.Version == "" {
		t.Skip("provider reports no object version; deletes here are guarded by timestamps only")
	}

	// Ask for a delete conditional on a revision the object never had. A
	// server that enforces the precondition must refuse.
	deleteErr := conditional.DeleteIfUnchanged(ctx, key, "\"codex-sync-never-written\"")

	survivor, headErr := store.Head(ctx, key)
	switch {
	case headErr == nil && survivor != nil && errors.Is(deleteErr, storage.ErrPreconditionFailed):
		t.Logf("provider enforces conditional delete (version %q)", info.Version)
	case headErr == nil && survivor != nil:
		t.Errorf("object survived but the adapter reported %v; expected ErrPreconditionFailed", deleteErr)
	default:
		t.Errorf("this endpoint ignored the delete precondition and removed an object whose "+
			"version did not match (delete returned %v, head after delete: %v). Sync still "+
			"re-reads each version immediately before deleting, so the exposure is one round "+
			"trip rather than the whole batch, but this storage cannot refuse a stale delete.",
			deleteErr, headErr)
	}
}
