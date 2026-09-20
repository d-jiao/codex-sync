package sync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/d-jiao/codex-sync/internal/storage"
)

// TrashBatchInfo summarises one batch of recycle-bin copies, which is what a
// single forced push left behind.
type TrashBatchInfo struct {
	Batch   string    // the batch name, as it appears under TrashPrefix
	Files   int       // how many copies it holds
	Size    int64     // total stored size of those copies
	Deleted time.Time // when the most recent copy in the batch was written
}

// splitTrashKey separates a recycle-bin key into its batch and the key the
// object had before it was deleted. ok is false for anything that is not a
// copy, including the batch collection itself.
func splitTrashKey(key string) (batch, originalKey string, ok bool) {
	rest, found := strings.CutPrefix(key, TrashPrefix)
	if !found {
		return "", "", false
	}
	batch, originalKey, found = strings.Cut(rest, "/")
	if !found || batch == "" || originalKey == "" {
		return "", "", false
	}
	return batch, originalKey, true
}

// ListRemoteTrash reports the recycle-bin batches held remotely, newest first.
// A forced push names the batch it wrote; this is how that name is found again
// later, when the push output is long gone.
func (s *Syncer) ListRemoteTrash(ctx context.Context) ([]TrashBatchInfo, error) {
	objects, err := s.storage.List(ctx, TrashPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to list the remote recycle bin: %w", err)
	}

	byBatch := make(map[string]*TrashBatchInfo)
	for _, obj := range objects {
		batch, _, ok := splitTrashKey(obj.Key)
		if !ok {
			continue
		}
		info, seen := byBatch[batch]
		if !seen {
			info = &TrashBatchInfo{Batch: batch}
			byBatch[batch] = info
		}
		info.Files++
		info.Size += obj.Size
		if obj.LastModified.After(info.Deleted) {
			info.Deleted = obj.LastModified
		}
	}

	batches := make([]TrashBatchInfo, 0, len(byBatch))
	for _, info := range byBatch {
		batches = append(batches, *info)
	}
	// Batch names are timestamps, so sorting the names newest first also
	// orders them by age without trusting the provider's clock.
	sort.Slice(batches, func(i, j int) bool { return batches[i].Batch > batches[j].Batch })
	return batches, nil
}

// RestoreRemoteTrash copies a batch back to the keys the objects had before
// they were deleted, which is what undoes a forced push. It returns the paths
// it restored and the ones it left alone because something is live under that
// key again: the copy is older by definition, so the live object wins. Run
// 'codex-sync pull' afterwards to bring the files back down.
func (s *Syncer) RestoreRemoteTrash(ctx context.Context, batch string) (restored, skipped []string, err error) {
	if batch == "" || strings.ContainsAny(batch, "/\\") || batch == "." || batch == ".." {
		return nil, nil, fmt.Errorf("invalid recycle-bin batch name %q", batch)
	}

	prefix := TrashPrefix + batch + "/"
	copies, err := s.storage.List(ctx, prefix)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list the remote recycle bin: %w", err)
	}
	if len(copies) == 0 {
		return nil, nil, fmt.Errorf("no recycle-bin batch named %q; run 'codex-sync trash list' to see what is there", batch)
	}

	// One listing decides what is already live. Restoring over a current
	// object would undo whichever device re-created the file.
	remoteObjects, err := s.storage.List(ctx, "")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list remote objects: %w", err)
	}
	live := make(map[string]struct{}, len(remoteObjects))
	for _, obj := range liveObjects(remoteObjects) {
		live[obj.Key] = struct{}{}
	}

	var errs []error
	for _, obj := range copies {
		_, originalKey, ok := splitTrashKey(obj.Key)
		if !ok {
			continue
		}
		display := originalKey
		if localPath, known := s.localPath(originalKey); known {
			display = localPath
		}
		if _, exists := live[originalKey]; exists {
			skipped = append(skipped, display)
			continue
		}
		if err := s.copyRemoteObject(ctx, obj.Key, originalKey); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", display, err))
			continue
		}
		restored = append(restored, display)
	}

	sort.Strings(restored)
	sort.Strings(skipped)
	return restored, skipped, errors.Join(errs...)
}

// copyRemoteObject duplicates srcKey to dstKey, preferring a copy the provider
// performs itself and moving the bytes through this process when it cannot or
// refuses to. Objects are copied as stored, so the ciphertext is never opened.
func (s *Syncer) copyRemoteObject(ctx context.Context, srcKey, dstKey string) error {
	var copyErr error
	if copier, ok := s.storage.(storage.ObjectCopier); ok {
		if copyErr = copier.Copy(ctx, srcKey, dstKey); copyErr == nil {
			return nil
		}
	}

	data, err := s.storage.Download(ctx, srcKey)
	if err != nil {
		return errors.Join(copyErr, err)
	}
	if err := s.storage.Upload(ctx, dstKey, data); err != nil {
		return errors.Join(copyErr, err)
	}
	return nil
}
