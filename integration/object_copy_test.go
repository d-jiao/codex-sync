//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/d-jiao/codex-sync/internal/storage"

	// Register storage adapters
	_ "github.com/d-jiao/codex-sync/internal/storage/r2"
)

// The recycle bin behind 'push --force' is only as good as the adapter's copy,
// and CopyObject is the one part of that path a unit test with an in-memory
// store cannot exercise: the CopySource format, its escaping, and whether the
// endpoint accepts it are all properties of the real service. This checks them
// against whatever endpoint the credentials point at, with a key that contains
// the characters Codex attachment names actually use.
func TestObjectCopyRoundTrip(t *testing.T) {
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
	copier, ok := store.(storage.ObjectCopier)
	if !ok {
		t.Skipf("%T does not implement ObjectCopier", store)
	}

	stamp := time.Now().UnixNano()
	src := fmt.Sprintf("_conformance/object copy #%d.age", stamp)
	dst := fmt.Sprintf("_conformance/_trash/%d/%s", stamp, src)
	payload := []byte("sentinel ciphertext")

	if err := store.Upload(ctx, src, payload); err != nil {
		t.Fatalf("failed to upload sentinel: %v", err)
	}
	t.Cleanup(func() {
		for _, key := range []string{src, dst} {
			if err := store.Delete(ctx, key); err != nil {
				t.Logf("failed to clean up %s: %v", key, err)
			}
		}
	})

	if err := copier.Copy(ctx, src, dst); err != nil {
		t.Fatalf("copy: %v", err)
	}

	copied, err := store.Download(ctx, dst)
	if err != nil {
		t.Fatalf("failed to download the copy: %v", err)
	}
	if !bytes.Equal(copied, payload) {
		t.Errorf("copy differs from the original: got %q", copied)
	}
	if _, err := store.Head(ctx, src); err != nil {
		t.Errorf("copying must leave the original in place: %v", err)
	}
}
