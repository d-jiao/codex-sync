package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Provider represents a storage provider type
type Provider string

const (
	ProviderR2     Provider = "r2"
	ProviderS3     Provider = "s3"
	ProviderGCS    Provider = "gcs"
	ProviderWebDAV Provider = "webdav"
)

// MaxDownloadSize is the maximum allowed size for a single downloaded object (100MB).
// This prevents memory exhaustion from oversized or malicious remote files.
const MaxDownloadSize = 100 * 1024 * 1024

// ObjectInfo contains metadata about a stored object
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	ETag         string
	// Version identifies one exact revision of the object, for optimistic
	// concurrency. S3, R2 and WebDAV report the ETag here; GCS reports the
	// object generation. It is empty when the provider reports nothing usable,
	// in which case callers fall back to LastModified comparisons.
	Version string
}

// ConditionalDeleter is implemented by adapters that can delete an object only
// while it still matches a known revision. Sync uses it so a remote object
// another device updated after this device recorded its state survives a
// delete that was decided from that stale state.
type ConditionalDeleter interface {
	// DeleteIfUnchanged removes key only if its current revision equals
	// expectedVersion. It returns an error satisfying IsPreconditionFailed
	// when the revision no longer matches.
	DeleteIfUnchanged(ctx context.Context, key, expectedVersion string) error
}

// ErrPreconditionFailed reports that a conditional operation was refused
// because the remote object no longer matches the expected revision.
var ErrPreconditionFailed = errors.New("remote object changed since it was last seen")

// Storage defines the interface for cloud storage operations
type Storage interface {
	// Upload stores data with the given key
	Upload(ctx context.Context, key string, data []byte) error

	// Download retrieves data for the given key
	Download(ctx context.Context, key string) ([]byte, error)

	// Delete removes the object with the given key
	Delete(ctx context.Context, key string) error

	// DeleteBatch removes multiple objects in a single operation
	DeleteBatch(ctx context.Context, keys []string) error

	// List returns all objects with the given prefix
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)

	// Head returns metadata for the given key without downloading content
	Head(ctx context.Context, key string) (*ObjectInfo, error)

	// BucketExists checks if the configured bucket exists
	BucketExists(ctx context.Context) (bool, error)
}

// New creates a new Storage instance based on the provided configuration
func New(cfg *StorageConfig) (Storage, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid storage config: %w", err)
	}

	switch cfg.Provider {
	case ProviderR2:
		return NewR2(cfg)
	case ProviderS3:
		return NewS3(cfg)
	case ProviderGCS:
		return NewGCS(cfg)
	case ProviderWebDAV:
		return NewWebDAV(cfg)
	default:
		return nil, fmt.Errorf("unsupported storage provider: %s", cfg.Provider)
	}
}

// NewR2 creates a new R2 storage adapter (implemented in r2/r2.go)
var NewR2 func(cfg *StorageConfig) (Storage, error)

// NewS3 creates a new S3 storage adapter (implemented in s3/s3.go)
var NewS3 func(cfg *StorageConfig) (Storage, error)

// NewGCS creates a new GCS storage adapter (implemented in gcs/gcs.go)
var NewGCS func(cfg *StorageConfig) (Storage, error)

// NewWebDAV creates a new WebDAV storage adapter (implemented in webdav/webdav.go)
var NewWebDAV func(cfg *StorageConfig) (Storage, error)
