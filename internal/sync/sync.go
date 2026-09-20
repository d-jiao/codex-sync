package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/d-jiao/codex-sync/internal/config"
	"github.com/d-jiao/codex-sync/internal/crypto"
	"github.com/d-jiao/codex-sync/internal/storage"

	// Register storage adapters
	_ "github.com/d-jiao/codex-sync/internal/storage/gcs"
	_ "github.com/d-jiao/codex-sync/internal/storage/r2"
	_ "github.com/d-jiao/codex-sync/internal/storage/s3"
)

const defaultWorkers = 10

// maxDecompressedSize is the maximum allowed size for decompressed data (500MB).
// This prevents decompression bomb attacks from consuming excessive memory.
const maxDecompressedSize = 500 * 1024 * 1024

// ManifestKey is the remote storage key for file metadata (mtimes).
const ManifestKey = "_metadata/manifest.json"

// FileManifest stores metadata about synced files, primarily mtimes.
type FileManifest struct {
	Files map[string]FileMetadata `json:"files"`
}

// FileMetadata stores metadata for a single file.
type FileMetadata struct {
	ModTime time.Time `json:"mod_time"`
}

type Syncer struct {
	storage    storage.Storage
	encryptor  *crypto.Encryptor
	state      *SyncState
	claudeDir  string
	quiet      bool
	onProgress ProgressFunc
	cfg        *config.Config
	paths      *PathMapper
	noDelete   bool   // pull --no-delete: never remove local files that vanished remotely
	trashDir   string // where removed files are moved (config.TrashDirPath by default)
	// allowRemoteDeletes gates push-side removal of remote objects. It is off
	// by default: a local file that disappeared (a stale checkout, a restored
	// backup, a half-configured sync_paths) must not silently erase the copy
	// every other device pulls from.
	allowRemoteDeletes bool
}

type SyncResult struct {
	Uploaded   []string
	Downloaded []string
	Merged     []string
	Deleted    []string
	Conflicts  []string
	Errors     []error
	Removed    []string // moved to the trash directory: vanished remotely, unchanged locally
	KeptLocal  []string // vanished remotely but modified locally: left in place
	// PendingDeletes are files deleted locally whose remote copy was left
	// alone because the push was not forced.
	PendingDeletes []string
}

type ProgressEvent struct {
	Action   string // "upload", "download", "delete", "encrypt", "decrypt", "scan"
	Path     string
	Size     int64
	Current  int
	Total    int
	Complete bool
	Error    error
}

type ProgressFunc func(event ProgressEvent)

func NewSyncer(cfg *config.Config, quiet bool) (*Syncer, error) {
	storageCfg := cfg.GetStorageConfig()
	store, err := storage.New(storageCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}

	enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	// Use overridden state path if provided, otherwise use default
	var state *SyncState
	if cfg.StateDirOverride != "" {
		state, err = LoadStateFromDir(cfg.StateDirOverride)
	} else {
		state, err = LoadState()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}

	// Use overridden base dir if provided, otherwise use default
	claudeDir := config.BaseDir()
	if cfg.BaseDirOverride != "" {
		claudeDir = cfg.BaseDirOverride
	}

	homeDir, _ := os.UserHomeDir()
	mapper, err := NewPathMapper(homeDir, cfg.PathMap)
	if err != nil {
		return nil, err
	}

	return &Syncer{
		storage:   store,
		encryptor: enc,
		state:     state,
		claudeDir: claudeDir,
		quiet:     quiet,
		cfg:       cfg,
		paths:     mapper,
		trashDir:  config.TrashDirPath(),
	}, nil
}

// NewSyncerWith creates a Syncer with pre-built dependencies (for testing).
func NewSyncerWith(cfg *config.Config, store storage.Storage, enc *crypto.Encryptor, state *SyncState, claudeDir string, quiet bool) *Syncer {
	homeDir, _ := os.UserHomeDir()
	mapper, _ := NewPathMapper(homeDir, cfg.PathMap)
	return &Syncer{
		storage:   store,
		encryptor: enc,
		state:     state,
		claudeDir: claudeDir,
		quiet:     quiet,
		cfg:       cfg,
		paths:     mapper,
		trashDir:  config.TrashDirPath(),
	}
}

func (s *Syncer) SetProgressFunc(fn ProgressFunc) {
	s.onProgress = fn
}

// SetNoDelete disables pull-side removal of files that vanished from the remote.
func (s *Syncer) SetNoDelete(v bool) { s.noDelete = v }

// SetAllowRemoteDeletes enables push-side removal of remote objects whose
// local file is gone. Callers pass true only for `push --force`.
func (s *Syncer) SetAllowRemoteDeletes(v bool) { s.allowRemoteDeletes = v }

// SetTrashDir overrides where removed files are moved (for testing).
func (s *Syncer) SetTrashDir(dir string) { s.trashDir = dir }

func (s *Syncer) progress(event ProgressEvent) {
	if s.onProgress != nil {
		s.onProgress(event)
	}
}

func (s *Syncer) isExcluded(relPath string) bool {
	return s.cfg.IsExcluded(relPath)
}

// syncPaths returns the set of Codex home paths to sync, honoring both the
// configured scope ("full" by default, or "sessions" for portable data only)
// and any sync_paths override, with scope acting as a ceiling.
func (s *Syncer) syncPaths() []string {
	return s.cfg.GetEffectiveSyncPaths()
}

// Scope returns the configured sync scope (empty means the default "full").
func (s *Syncer) Scope() string {
	return s.cfg.Scope
}

// SyncPaths returns the effective Codex home paths this syncer operates on, so
// callers such as the pre-pull backup cover exactly the set that pull can
// overwrite rather than recomputing it from scope alone.
func (s *Syncer) SyncPaths() []string {
	return s.syncPaths()
}

func (s *Syncer) log(format string, args ...interface{}) {
	if !s.quiet {
		fmt.Printf(format+"\n", args...)
	}
}

func (s *Syncer) Push(ctx context.Context) (*SyncResult, error) {
	result := &SyncResult{}

	s.progress(ProgressEvent{Action: "scan", Path: "Detecting changes..."})

	changes, err := s.state.DetectChanges(s.claudeDir, s.syncPaths(), s.isExcluded)
	if err != nil {
		return nil, fmt.Errorf("failed to detect changes: %w", err)
	}

	if len(changes) == 0 {
		s.progress(ProgressEvent{Action: "scan", Complete: true})
		if !s.state.ManifestDirty {
			return result, nil
		}
		// Nothing to send, but a previous push failed to publish the mtime
		// manifest; finish that before reporting success.
		err := s.pushManifest(ctx)
		if saveErr := s.state.Save(); saveErr != nil && err == nil {
			err = fmt.Errorf("failed to save state: %w", saveErr)
		}
		return result, err
	}

	// Separate uploads from deletes. A file with a live conflict sidecar is
	// not published until the user resolves it (spec §7): pushing it would
	// silently overwrite the remote version the sidecar holds.
	var uploads, deletes []FileChange
	for _, change := range changes {
		switch change.Action {
		case "add", "modify":
			if s.hasConflictSidecar(change.Path) {
				result.Errors = append(result.Errors,
					fmt.Errorf("unresolved conflict for %s; run 'codex-sync conflicts'", change.Path))
				continue
			}
			uploads = append(uploads, change)
		case "delete":
			deletes = append(deletes, change)
		}
	}

	total := len(uploads) + len(deletes)
	var mu sync.Mutex
	var completed atomic.Int32

	// Process uploads concurrently
	if len(uploads) > 0 {
		sem := make(chan struct{}, defaultWorkers)
		var wg sync.WaitGroup

		for _, change := range uploads {
			wg.Add(1)
			go func(change FileChange) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				n := int(completed.Add(1))
				s.progress(ProgressEvent{
					Action:  "upload",
					Path:    change.Path,
					Size:    change.LocalSize,
					Current: n,
					Total:   total,
				})

				if err := s.uploadFile(ctx, change.Path); err != nil {
					s.progress(ProgressEvent{
						Action: "upload",
						Path:   change.Path,
						Error:  err,
					})
					mu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("%s: %w", change.Path, err))
					mu.Unlock()
					return
				}
				mu.Lock()
				result.Uploaded = append(result.Uploaded, change.Path)
				mu.Unlock()
			}(change)
		}
		wg.Wait()
	}

	if len(deletes) > 0 {
		s.applyDeletes(ctx, deletes, result)
	}

	s.progress(ProgressEvent{Action: "upload", Complete: true, Total: total})

	// Upload manifest with file mtimes for cross-device mtime preservation
	// The manifest carries the mtimes other devices restore, so a failure is
	// reported rather than logged. The uploads that already succeeded stay
	// recorded in state, and the flag makes the next push retry the manifest
	// even when no file changed.
	var manifestErr error
	if len(result.Uploaded) > 0 || len(result.Deleted) > 0 || s.state.ManifestDirty {
		manifestErr = s.pushManifest(ctx)
	}

	s.state.LastPush = time.Now()
	s.state.LastSync = time.Now()
	if err := s.state.Save(); err != nil {
		return result, fmt.Errorf("failed to save state: %w", err)
	}
	if manifestErr != nil {
		return result, manifestErr
	}

	return result, nil
}

// applyDeletes propagates local deletions to the remote. Without --force it
// only reports them. With --force each object is removed one at a time, and
// only while it still matches the revision this device last saw, so a copy
// another device updated in the meantime survives a deletion decided from
// stale local state.
func (s *Syncer) applyDeletes(ctx context.Context, deletes []FileChange, result *SyncResult) {
	if !s.allowRemoteDeletes {
		for _, change := range deletes {
			result.PendingDeletes = append(result.PendingDeletes, change.Path)
		}
		return
	}

	// One listing gives both the current revisions and the answer to "is it
	// even still there", without a per-file Head whose not-found error every
	// provider spells differently.
	remoteObjects, err := s.storage.List(ctx, "")
	if err != nil {
		result.Errors = append(result.Errors, fmt.Errorf("failed to list remote objects before deleting: %w", err))
		return
	}
	byKey := make(map[string]storage.ObjectInfo, len(remoteObjects))
	for _, obj := range remoteObjects {
		byKey[obj.Key] = obj
	}

	total := len(deletes)
	for i, change := range deletes {
		s.progress(ProgressEvent{Action: "delete", Path: change.Path, Current: i + 1, Total: total})

		key := s.remoteKey(change.Path)
		obj, stillThere := byKey[key]
		if !stillThere {
			// Already gone remotely; just stop tracking it.
			s.state.RemoveFile(change.Path)
			result.Deleted = append(result.Deleted, change.Path)
			continue
		}

		if remoteChanged(obj, s.state.GetFile(change.Path)) {
			result.Errors = append(result.Errors, fmt.Errorf(
				"%s: the remote copy changed on another device since this one last synced it; "+
					"run 'codex-sync pull' and delete it again if you still want it gone", change.Path))
			continue
		}

		if err := s.deleteRemoteObject(ctx, key, obj.Version); err != nil {
			if errors.Is(err, storage.ErrPreconditionFailed) {
				result.Errors = append(result.Errors, fmt.Errorf(
					"%s: the remote copy changed while deleting it; nothing was removed", change.Path))
				continue
			}
			result.Errors = append(result.Errors, fmt.Errorf("%s: %w", change.Path, err))
			continue
		}
		s.state.RemoveFile(change.Path)
		result.Deleted = append(result.Deleted, change.Path)
	}
}

// deleteRemoteObject removes key, preferring a conditional delete so the
// object survives a change that lands between the listing and the request.
// Providers that report no usable revision (some WebDAV servers omit ETags)
// fall back to a plain delete, which the staleness check above already
// guarded with timestamps.
func (s *Syncer) deleteRemoteObject(ctx context.Context, key, version string) error {
	if cd, ok := s.storage.(storage.ConditionalDeleter); ok && version != "" {
		return cd.DeleteIfUnchanged(ctx, key, version)
	}
	return s.storage.Delete(ctx, key)
}

func (s *Syncer) Pull(ctx context.Context) (*SyncResult, error) {
	result := &SyncResult{}

	s.progress(ProgressEvent{Action: "scan", Path: "Fetching remote file list..."})

	// List all remote objects
	remoteObjects, err := s.storage.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list remote objects: %w", err)
	}

	if len(remoteObjects) == 0 {
		s.progress(ProgressEvent{Action: "scan", Complete: true})
		return result, nil
	}

	// Download the manifest for mtime restoration. A remote written before
	// manifests existed simply has none; one that is listed but unreadable
	// means we cannot restore mtimes correctly, so stop before touching any
	// local file.
	manifest, err := s.downloadManifest(ctx, manifestPresent(remoteObjects))
	if err != nil {
		return nil, err
	}

	// Build remote file map
	remoteFiles, skipped := s.buildRemoteMap(remoteObjects)
	for _, key := range skipped {
		result.Errors = append(result.Errors,
			fmt.Errorf("%s: unknown path token; add the matching path_map entry on this device", key))
	}

	// Get current local files
	localFiles, err := GetLocalFiles(s.claudeDir, s.syncPaths(), s.isExcluded)
	if err != nil {
		return nil, fmt.Errorf("failed to get local files: %w", err)
	}

	// Build list of files to download
	type downloadTask struct {
		localPath string
		remoteObj storage.ObjectInfo
	}
	var toDownload []downloadTask

	for localPath, remoteObj := range remoteFiles {
		localInfo, localExists := localFiles[localPath]
		stateFile := s.state.GetFile(localPath)

		if IsMergeablePath(localPath) {
			merged, err := s.mergeRemote(ctx, localPath, remoteObj, localExists, stateFile)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Errorf("%s: %w", localPath, err))
			} else if merged {
				result.Merged = append(result.Merged, localPath)
			}
			continue
		}

		shouldDownload := false

		if !localExists {
			shouldDownload = true
		} else if stateFile != nil {
			// Check if remote is newer than our last known state
			if remoteChanged(remoteObj, stateFile) {
				// Remote was updated after we last uploaded
				// Check if local was also modified
				localHash, _ := HashFile(filepath.Join(s.claudeDir, localPath))
				if localHash != stateFile.Hash {
					// Conflict: both changed
					result.Conflicts = append(result.Conflicts, localPath)
					s.progress(ProgressEvent{
						Action: "conflict",
						Path:   localPath,
					})
					if err := s.handleConflict(ctx, localPath, remoteObj); err != nil {
						result.Errors = append(result.Errors, err)
					}
					continue
				}
				shouldDownload = true
			}
		} else if localInfo.ModTime().Before(remoteObj.LastModified) {
			shouldDownload = true
		}

		if shouldDownload {
			toDownload = append(toDownload, downloadTask{localPath, remoteObj})
		}
	}

	// Download files concurrently
	total := len(toDownload)
	if total > 0 {
		sem := make(chan struct{}, defaultWorkers)
		var wg sync.WaitGroup
		var mu sync.Mutex
		var completed atomic.Int32

		for _, task := range toDownload {
			wg.Add(1)
			go func(task downloadTask) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				n := int(completed.Add(1))
				s.progress(ProgressEvent{
					Action:  "download",
					Path:    task.localPath,
					Size:    task.remoteObj.Size,
					Current: n,
					Total:   total,
				})

				// Get original mtime from manifest if available
				var mtime *time.Time
				if manifest != nil {
					if meta, ok := manifest.Files[task.localPath]; ok {
						mtime = &meta.ModTime
					}
				}

				if err := s.downloadFile(ctx, task.localPath, task.remoteObj, mtime); err != nil {
					s.progress(ProgressEvent{
						Action: "download",
						Path:   task.localPath,
						Error:  err,
					})
					mu.Lock()
					result.Errors = append(result.Errors, fmt.Errorf("%s: %w", task.localPath, err))
					mu.Unlock()
					return
				}
				mu.Lock()
				result.Downloaded = append(result.Downloaded, task.localPath)
				mu.Unlock()
			}(task)
		}
		wg.Wait()
	}

	s.progress(ProgressEvent{Action: "download", Complete: true, Total: total})

	// Files that vanished from the remote (deleted or moved on another machine)
	// are moved to the trash when unchanged locally; never when this pull saw an
	// empty remote (Pull returned early above).
	if !s.noDelete {
		removable, kept, err := s.staleLocalFiles(remoteFiles, localFiles)
		if err != nil {
			result.Errors = append(result.Errors, err)
		} else {
			batch := time.Now().Format("20060102-150405")
			for _, relPath := range removable {
				if err := s.moveToTrash(relPath, batch); err != nil {
					result.Errors = append(result.Errors, fmt.Errorf("%s: %w", relPath, err))
					continue
				}
				s.state.RemoveFile(relPath)
				result.Removed = append(result.Removed, relPath)
			}
			result.KeptLocal = kept
		}
	}

	s.state.LastPull = time.Now()
	s.state.LastSync = time.Now()
	if err := s.state.Save(); err != nil {
		return result, fmt.Errorf("failed to save state: %w", err)
	}

	return result, nil
}

func (s *Syncer) Status(ctx context.Context) ([]FileChange, error) {
	return s.state.DetectChanges(s.claudeDir, s.syncPaths(), s.isExcluded)
}

func (s *Syncer) uploadFile(ctx context.Context, relativePath string) error {
	fullPath := filepath.Join(s.claudeDir, relativePath)

	// Snapshot the file before reading it so a concurrent writer (Codex
	// appending to a rollout, say) can be detected after the upload.
	before, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	// Read file
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// State records what we actually uploaded, so hash these exact bytes
	// rather than re-reading the file afterwards.
	localHash := hashBytes(data)

	// Replace machine-specific paths with portable tokens in session content
	if IsPortableContentPath(relativePath) {
		data = s.paths.NormalizeContent(data)
	}

	// Compress
	compressed, err := gzipCompress(data)
	if err != nil {
		return fmt.Errorf("failed to compress: %w", err)
	}

	// Encrypt
	encrypted, err := s.encryptor.Encrypt(compressed)
	if err != nil {
		return fmt.Errorf("failed to encrypt: %w", err)
	}

	// Upload
	remoteKey := s.remoteKey(relativePath)
	if err := s.storage.Upload(ctx, remoteKey, encrypted); err != nil {
		return fmt.Errorf("failed to upload: %w", err)
	}

	// If the file changed while we were uploading, the remote now holds a
	// prefix of it. Leave state untouched so the next push sees the file as
	// still pending instead of recording the newer bytes as synced.
	after, err := os.Stat(fullPath)
	if err != nil {
		return fmt.Errorf("failed to re-stat file after upload: %w", err)
	}
	if after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return fmt.Errorf("file changed while uploading; it will be retried on the next push")
	}

	// Storage services stamp objects with their own clock, and that stamp can
	// land a few milliseconds after the upload call returns (R2 does this).
	// Record the later of the two so this upload never looks newer than
	// "uploaded" to the next pull, which would re-download identical bytes.
	uploadedAt := time.Now()
	var remoteVersion string
	if hdr, err := s.storage.Head(ctx, remoteKey); err == nil && hdr != nil {
		remoteVersion = hdr.Version
		if hdr.LastModified.After(uploadedAt) {
			uploadedAt = hdr.LastModified
		}
	}

	// Update state
	s.state.UpdateFile(relativePath, after, localHash)
	s.state.MarkRemote(relativePath, uploadedAt, remoteVersion)

	return nil
}

// remoteChanged reports whether a remote object differs from the revision this
// device last recorded. Provider versions (ETag, GCS generation) are exact, so
// they win whenever both sides have one; timestamps are the fallback, and they
// are only trustworthy in one direction because storage clocks drift and some
// providers report whole seconds.
func remoteChanged(remote storage.ObjectInfo, state *FileState) bool {
	if state == nil {
		return true
	}
	if remote.Version != "" && state.RemoteVersion != "" {
		return remote.Version != state.RemoteVersion
	}
	return remote.LastModified.After(state.Uploaded)
}

// fetchRemote downloads, decrypts, decompresses and de-tokenizes one remote object.
func (s *Syncer) fetchRemote(ctx context.Context, relativePath, remoteKey string) ([]byte, error) {
	encrypted, err := s.storage.Download(ctx, remoteKey)
	if err != nil {
		return nil, fmt.Errorf("failed to download: %w", err)
	}
	data, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt: %w", err)
	}
	// Backward-compatible: older remote blobs may be uncompressed
	if isGzipped(data) {
		if data, err = gzipDecompress(data); err != nil {
			return nil, fmt.Errorf("failed to decompress: %w", err)
		}
	}
	if IsPortableContentPath(relativePath) {
		data = s.paths.ResolveContent(data)
	}
	return data, nil
}

// downloadFile downloads and decrypts a file from remote storage.
// If originalMtime is non-nil, the file's modification time will be restored to that value.
func (s *Syncer) downloadFile(ctx context.Context, relativePath string, remote storage.ObjectInfo, originalMtime *time.Time) error {
	if config.IsProtected(relativePath) {
		return fmt.Errorf("refusing to write protected file %s", relativePath)
	}
	data, err := s.fetchRemote(ctx, relativePath, remote.Key)
	if err != nil {
		return err
	}
	fullPath, err := safeLocalPath(s.claudeDir, relativePath)
	if err != nil {
		return err
	}

	// Transcripts can contain secrets echoed by tools: keep them user-only.
	// The write is atomic so an interrupted pull leaves the previous content
	// intact rather than a truncated file.
	if err := writeLocalFileAtomic(s.claudeDir, relativePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// Restore original modification time if provided
	if originalMtime != nil {
		if err := os.Chtimes(fullPath, *originalMtime, *originalMtime); err != nil {
			// Log but don't fail - mtime restoration is best-effort
			s.log("Warning: failed to restore mtime for %s: %v", relativePath, err)
		}
	}

	// Update state
	info, _ := os.Stat(fullPath)
	hash, _ := HashFile(fullPath)
	s.state.UpdateFile(relativePath, info, hash)
	s.state.MarkRemote(relativePath, remote.LastModified, remote.Version)

	return nil
}

// handleConflict keeps the local file and writes the remote copy next to it
// as `<path>.conflict.<timestamp>`. The sidecar is a local artifact for
// `codex-sync conflicts`: it is hard-excluded from every scan (config.HardExcludes)
// and dropped from state here, so it is never uploaded, tracked or trashed.
func (s *Syncer) handleConflict(ctx context.Context, relativePath string, remoteObj storage.ObjectInfo) error {
	s.log("Conflict detected: %s (keeping local, saving remote as .conflict)", relativePath)

	// downloadFile de-tokenizes content (IsPortableContentPath honors the
	// .conflict. suffix) but also records the sidecar in state; undo that.
	conflictPath, err := uniqueConflictPath(s.claudeDir, relativePath, time.Now())
	if err != nil {
		return err
	}
	defer s.state.RemoveFile(conflictPath)
	if err := s.downloadFile(ctx, conflictPath, remoteObj, nil); err != nil {
		return fmt.Errorf("failed to save conflict file: %w", err)
	}

	return nil
}

// isConflictSidecar reports whether relPath is a `<path>.conflict.<timestamp>`
// sidecar written by handleConflict.
func isConflictSidecar(relPath string) bool {
	return strings.Contains(relPath, ".conflict.")
}

// hasConflictSidecar reports whether a `<relPath>.conflict.*` file sits next
// to relPath. It lists the directory rather than globbing so names containing
// glob metacharacters are matched literally.
func (s *Syncer) hasConflictSidecar(relPath string) bool {
	fullPath := filepath.Join(s.claudeDir, filepath.FromSlash(relPath))
	entries, err := os.ReadDir(filepath.Dir(fullPath))
	if err != nil {
		return false
	}
	prefix := filepath.Base(fullPath) + ".conflict."
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			return true
		}
	}
	return false
}

// staleLocalFiles lists tracked files that are present locally but gone from
// the remote: removable when unchanged since the last sync, kept when modified
// locally. Mergeable and protected paths are never candidates, nor are conflict
// sidecars (handleConflict never tracks them; the skip covers state written by
// older builds that did).
func (s *Syncer) staleLocalFiles(remoteFiles map[string]storage.ObjectInfo, localFiles map[string]os.FileInfo) (removable, kept []string, err error) {
	s.state.mu.Lock()
	tracked := make([]string, 0, len(s.state.Files))
	for p := range s.state.Files {
		tracked = append(tracked, p)
	}
	s.state.mu.Unlock()
	sort.Strings(tracked)

	for _, relPath := range tracked {
		if _, onRemote := remoteFiles[relPath]; onRemote {
			continue
		}
		if _, onDisk := localFiles[relPath]; !onDisk {
			continue
		}
		if IsMergeablePath(relPath) || config.IsProtected(relPath) || isConflictSidecar(relPath) {
			continue
		}
		hash, err := HashFile(filepath.Join(s.claudeDir, relPath))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to hash %s: %w", relPath, err)
		}
		if hash == s.state.GetFile(relPath).Hash {
			removable = append(removable, relPath)
		} else {
			kept = append(kept, relPath)
		}
	}
	return removable, kept, nil
}

// moveToTrash relocates a local file to <trashDir>/<batch>/<relPath>. A rename
// that fails (different volume) falls back to copy-then-remove.
func (s *Syncer) moveToTrash(relPath, batch string) error {
	if s.trashDir == "" {
		return fmt.Errorf("trash directory not configured; refusing to remove %s", relPath)
	}
	src := filepath.Join(s.claudeDir, relPath)
	dst := filepath.Join(s.trashDir, batch, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return fmt.Errorf("failed to create trash directory: %w", err)
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0600); err != nil {
		return err
	}
	return os.Remove(src)
}

// mergeRemote handles a mergeable file (see IsMergeablePath): the remote copy is
// unioned into the local one whenever the remote changed since the last sync,
// or was never synced here. State records the remote hash, so the next push
// uploads the union exactly when the local copy contributed something.
func (s *Syncer) mergeRemote(ctx context.Context, relativePath string, remoteObj storage.ObjectInfo, localExists bool, stateFile *FileState) (bool, error) {
	if stateFile != nil && localExists && !remoteChanged(remoteObj, stateFile) {
		return false, nil // remote unchanged since we last merged it
	}
	remoteData, err := s.fetchRemote(ctx, relativePath, remoteObj.Key)
	if err != nil {
		return false, err
	}
	fullPath, err := safeLocalPath(s.claudeDir, relativePath)
	if err != nil {
		return false, err
	}
	var localData []byte
	if localExists {
		if localData, err = os.ReadFile(fullPath); err != nil {
			return false, fmt.Errorf("failed to read local file: %w", err)
		}
	}
	merged, err := MergeJSONL(relativePath, localData, remoteData)
	if err != nil {
		return false, err
	}
	if err := writeLocalFileAtomic(s.claudeDir, relativePath, merged, 0600); err != nil {
		return false, fmt.Errorf("failed to write merged file: %w", err)
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return false, err
	}
	s.state.UpdateFile(relativePath, info, hashBytes(remoteData))
	s.state.MarkRemote(relativePath, remoteObj.LastModified, remoteObj.Version)
	return true, nil
}

// pushManifest publishes the mtime manifest and records whether the remote
// copy is now current, so a failure is retried by the next push even if no
// file changes in between.
func (s *Syncer) pushManifest(ctx context.Context) error {
	if err := s.uploadManifest(ctx); err != nil {
		s.state.ManifestDirty = true
		return err
	}
	s.state.ManifestDirty = false
	return nil
}

// uploadManifest builds and uploads a manifest containing file mtimes from current state.
func (s *Syncer) uploadManifest(ctx context.Context) error {
	manifest := FileManifest{
		Files: make(map[string]FileMetadata),
	}

	// Build manifest from current state
	s.state.mu.Lock()
	for path, fs := range s.state.Files {
		manifest.Files[path] = FileMetadata{
			ModTime: fs.ModTime,
		}
	}
	s.state.mu.Unlock()

	// Serialize manifest
	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to serialize manifest: %w", err)
	}

	// Compress
	compressed, err := gzipCompress(data)
	if err != nil {
		return fmt.Errorf("failed to compress manifest: %w", err)
	}

	// Encrypt
	encrypted, err := s.encryptor.Encrypt(compressed)
	if err != nil {
		return fmt.Errorf("failed to encrypt manifest: %w", err)
	}

	// Upload
	remoteKey := ManifestKey + ".age"
	if err := s.storage.Upload(ctx, remoteKey, encrypted); err != nil {
		return fmt.Errorf("failed to upload manifest: %w", err)
	}

	return nil
}

// manifestPresent reports whether the remote listing contains the manifest, so
// a genuinely absent one can be told apart from one that failed to download.
func manifestPresent(remoteObjects []storage.ObjectInfo) bool {
	for _, obj := range remoteObjects {
		if obj.Key == ManifestKey+".age" {
			return true
		}
	}
	return false
}

// downloadManifest downloads and parses the file manifest from remote storage.
// It returns (nil, nil) when the remote has no manifest at all, which is how
// syncs written before manifests existed look; every other failure is an error.
func (s *Syncer) downloadManifest(ctx context.Context, present bool) (*FileManifest, error) {
	if !present {
		return nil, nil
	}
	remoteKey := ManifestKey + ".age"

	// Download
	encrypted, err := s.storage.Download(ctx, remoteKey)
	if err != nil {
		return nil, fmt.Errorf("failed to download manifest: %w", err)
	}

	// Decrypt
	data, err := s.encryptor.Decrypt(encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt manifest: %w", err)
	}

	// Decompress if gzipped
	if isGzipped(data) {
		data, err = gzipDecompress(data)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress manifest: %w", err)
		}
	}

	// Parse
	var manifest FileManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return &manifest, nil
}

func (s *Syncer) remoteKey(relativePath string) string {
	// Normalize machine-specific path segments, add .age extension
	return s.paths.NormalizeRelPath(relativePath) + ".age"
}

// localPath maps a remote key back to a local relative path. ok is false when
// the key uses a path_map token this device doesn't define.
func (s *Syncer) localPath(remoteKey string) (string, bool) {
	return s.paths.ResolveRelPath(strings.TrimSuffix(remoteKey, ".age"))
}

// buildRemoteMap maps remote objects to local relative paths, skipping
// non-encrypted keys, MCP data, excluded paths, and keys with unknown path
// tokens (reported via skipped). When a legacy un-normalized key and its
// normalized replacement both exist, the normalized one wins.
func (s *Syncer) buildRemoteMap(remoteObjects []storage.ObjectInfo) (remoteFiles map[string]storage.ObjectInfo, skipped []string) {
	remoteFiles = make(map[string]storage.ObjectInfo)
	for _, obj := range remoteObjects {
		// Skip non-encrypted files
		if !strings.HasSuffix(obj.Key, ".age") {
			continue
		}
		localPath, ok := s.localPath(obj.Key)
		if !ok {
			skipped = append(skipped, obj.Key)
			continue
		}
		// Skip legacy external objects (claude-sync MCP sync); never mapped to local files
		if strings.HasPrefix(localPath, "_external/") {
			continue
		}
		// Skip metadata files (manifest, etc.)
		if strings.HasPrefix(localPath, "_metadata/") {
			continue
		}
		// Skip excluded paths
		if s.isExcluded(localPath) {
			continue
		}
		if existing, dup := remoteFiles[localPath]; dup {
			// Prefer the canonical (normalized) key over a legacy duplicate
			if existing.Key == s.remoteKey(localPath) {
				continue
			}
		}
		remoteFiles[localPath] = obj
	}
	return remoteFiles, skipped
}

func (s *Syncer) GetState() *SyncState {
	return s.state
}

// HasState returns true if the syncer has existing sync state (not first sync)
func (s *Syncer) HasState() bool {
	return !s.state.IsEmpty()
}

// FilePreview represents a file that would be affected by a pull operation
type FilePreview struct {
	Path       string
	LocalTime  time.Time
	RemoteTime time.Time
	LocalSize  int64
	RemoteSize int64
	LocalOnly  bool // File exists only locally
	RemoteOnly bool // File exists only remotely
}

// PullPreview represents what would happen during a pull operation
type PullPreview struct {
	WouldDownload  []FilePreview // New remote files that would be downloaded
	WouldOverwrite []FilePreview // Existing local files that would be replaced
	WouldKeep      []FilePreview // Local files that would be kept (local newer)
	WouldConflict  []FilePreview // Files that would create a conflict
	WouldMerge     []FilePreview // Shared index files that would be unioned with the local copy
	LocalOnlyFiles []FilePreview // Files that exist only locally
	WouldRemove    []FilePreview // Tracked files gone from the remote, unchanged locally: would move to trash
	WouldKeepLocal []FilePreview // Tracked files gone from the remote but modified locally: would stay in place
}

// PreviewPull returns a preview of what would happen during a pull operation
// without actually making any changes
func (s *Syncer) PreviewPull(ctx context.Context) (*PullPreview, error) {
	preview := &PullPreview{}

	// List all remote objects
	remoteObjects, err := s.storage.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list remote objects: %w", err)
	}

	// Build remote file map
	remoteFiles, _ := s.buildRemoteMap(remoteObjects)

	// Get current local files
	localFiles, err := GetLocalFiles(s.claudeDir, s.syncPaths(), s.isExcluded)
	if err != nil {
		return nil, fmt.Errorf("failed to get local files: %w", err)
	}

	// Analyze each remote file
	for localPath, remoteObj := range remoteFiles {
		localInfo, localExists := localFiles[localPath]
		stateFile := s.state.GetFile(localPath)

		fp := FilePreview{
			Path:       localPath,
			RemoteTime: remoteObj.LastModified,
			RemoteSize: remoteObj.Size,
		}

		if localExists {
			fp.LocalTime = localInfo.ModTime()
			fp.LocalSize = localInfo.Size()
		}

		if IsMergeablePath(localPath) {
			if stateFile == nil || !localExists || remoteChanged(remoteObj, stateFile) {
				preview.WouldMerge = append(preview.WouldMerge, fp)
			}
			continue
		}

		if !localExists {
			// New file from remote
			fp.RemoteOnly = true
			preview.WouldDownload = append(preview.WouldDownload, fp)
		} else if stateFile != nil {
			// Check if remote is newer than our last known state
			if remoteChanged(remoteObj, stateFile) {
				// Remote was updated after we last uploaded
				localHash, _ := HashFile(filepath.Join(s.claudeDir, localPath))
				if localHash != stateFile.Hash {
					// Conflict: both changed
					preview.WouldConflict = append(preview.WouldConflict, fp)
				} else {
					// Only remote changed
					preview.WouldOverwrite = append(preview.WouldOverwrite, fp)
				}
			} else {
				// Local is current
				preview.WouldKeep = append(preview.WouldKeep, fp)
			}
		} else {
			// No state - compare timestamps
			if localInfo.ModTime().Before(remoteObj.LastModified) {
				preview.WouldOverwrite = append(preview.WouldOverwrite, fp)
			} else {
				preview.WouldKeep = append(preview.WouldKeep, fp)
			}
		}
	}

	// Find local-only files
	for localPath, localInfo := range localFiles {
		if _, exists := remoteFiles[localPath]; !exists {
			preview.LocalOnlyFiles = append(preview.LocalOnlyFiles, FilePreview{
				Path:      localPath,
				LocalTime: localInfo.ModTime(),
				LocalSize: localInfo.Size(),
				LocalOnly: true,
			})
		}
	}

	if len(remoteObjects) > 0 && !s.noDelete {
		removable, kept, err := s.staleLocalFiles(remoteFiles, localFiles)
		if err != nil {
			return nil, err
		}
		for _, p := range removable {
			preview.WouldRemove = append(preview.WouldRemove, FilePreview{Path: p, LocalSize: localFiles[p].Size()})
		}
		for _, p := range kept {
			preview.WouldKeepLocal = append(preview.WouldKeepLocal, FilePreview{Path: p, LocalSize: localFiles[p].Size()})
		}
	}

	return preview, nil
}

type DiffEntry struct {
	Path       string
	Status     string // "local_only", "remote_only", "modified", "synced"
	LocalSize  int64
	RemoteSize int64
	LocalTime  time.Time
	RemoteTime time.Time
}

func (s *Syncer) Diff(ctx context.Context) ([]DiffEntry, error) {
	var entries []DiffEntry

	// Get local files
	localFiles, err := GetLocalFiles(s.claudeDir, s.syncPaths(), s.isExcluded)
	if err != nil {
		return nil, fmt.Errorf("failed to get local files: %w", err)
	}

	// Get remote files
	remoteObjects, err := s.storage.List(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list remote objects: %w", err)
	}

	remoteFiles, _ := s.buildRemoteMap(remoteObjects)

	// Find local-only and modified files
	for relPath, info := range localFiles {
		remoteObj, exists := remoteFiles[relPath]
		if !exists {
			entries = append(entries, DiffEntry{
				Path:      relPath,
				Status:    "local_only",
				LocalSize: info.Size(),
				LocalTime: info.ModTime(),
			})
		} else {
			stateFile := s.state.GetFile(relPath)
			if stateFile != nil {
				localHash, _ := HashFile(filepath.Join(s.claudeDir, relPath))
				if localHash != stateFile.Hash || remoteChanged(remoteObj, stateFile) {
					entries = append(entries, DiffEntry{
						Path:       relPath,
						Status:     "modified",
						LocalSize:  info.Size(),
						RemoteSize: remoteObj.Size,
						LocalTime:  info.ModTime(),
						RemoteTime: remoteObj.LastModified,
					})
				} else {
					entries = append(entries, DiffEntry{
						Path:       relPath,
						Status:     "synced",
						LocalSize:  info.Size(),
						RemoteSize: remoteObj.Size,
						LocalTime:  info.ModTime(),
						RemoteTime: remoteObj.LastModified,
					})
				}
			} else {
				entries = append(entries, DiffEntry{
					Path:       relPath,
					Status:     "modified",
					LocalSize:  info.Size(),
					RemoteSize: remoteObj.Size,
					LocalTime:  info.ModTime(),
					RemoteTime: remoteObj.LastModified,
				})
			}
		}
	}

	// Find remote-only files
	for relPath, obj := range remoteFiles {
		if _, exists := localFiles[relPath]; !exists {
			entries = append(entries, DiffEntry{
				Path:       relPath,
				Status:     "remote_only",
				RemoteSize: obj.Size,
				RemoteTime: obj.LastModified,
			})
		}
	}

	return entries, nil
}

// isGzipped checks if data starts with the gzip magic number (0x1f 0x8b).
func isGzipped(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

func gzipCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gzipDecompress(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	// Limit decompressed size to prevent decompression bomb attacks
	limited := io.LimitReader(r, maxDecompressedSize+1)
	result, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(result)) > maxDecompressedSize {
		return nil, fmt.Errorf("decompressed data exceeds %d bytes limit", maxDecompressedSize)
	}
	return result, nil
}
