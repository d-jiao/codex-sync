package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/d-jiao/codex-sync/internal/storage"
)

func init() {
	storage.NewS3 = New
}

// Sync reaches these capabilities through a type assertion, so a drifting
// signature would silently disable them rather than fail to build.
var (
	_ storage.ConditionalDeleter = (*Client)(nil)
	_ storage.ObjectCopier       = (*Client)(nil)
)

// Client implements the storage.Storage interface for AWS S3
type Client struct {
	client *s3.Client
	bucket string
}

// New creates a new S3 storage client
func New(cfg *storage.StorageConfig) (storage.Storage, error) {
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		)),
		config.WithRegion(cfg.Region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, buildS3Options(cfg))

	return &Client{
		client: client,
		bucket: cfg.Bucket,
	}, nil
}

// buildS3Options returns the functional options applied to the S3 client.
// When a custom endpoint is configured (i.e. an S3-compatible provider such as
// Backblaze B2, MinIO or Wasabi rather than AWS), it points the client at that
// endpoint and relaxes checksum behavior to WhenRequired. The AWS SDK's default
// (WhenSupported) sends x-amz-checksum integrity headers that several
// S3-compatible providers reject; leaving the endpoint empty preserves the
// AWS-native defaults unchanged.
func buildS3Options(cfg *storage.StorageConfig) func(*s3.Options) {
	return func(o *s3.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(storage.NormalizeEndpoint(cfg.Endpoint))
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
			o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
			// Path-style addressing for servers that don't resolve buckets as
			// subdomains (Ceph RGW, MinIO without wildcard DNS). Left false for
			// AWS, which prefers virtual-hosted style.
			o.UsePathStyle = cfg.UsePathStyle
		}
	}
}

// Upload stores data with the given key
func (c *Client) Upload(ctx context.Context, key string, data []byte) error {
	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		return fmt.Errorf("failed to upload %s: %w", key, err)
	}
	return nil
}

// Download retrieves data for the given key
func (c *Client) Download(ctx context.Context, key string) ([]byte, error) {
	result, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", key, err)
	}
	defer func() { _ = result.Body.Close() }()

	// Limit download size to prevent memory exhaustion
	limited := io.LimitReader(result.Body, storage.MaxDownloadSize+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", key, err)
	}
	if int64(len(data)) > storage.MaxDownloadSize {
		return nil, fmt.Errorf("file %s exceeds maximum download size of %d bytes", key, storage.MaxDownloadSize)
	}

	return data, nil
}

// Delete removes the object with the given key
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to delete %s: %w", key, err)
	}
	return nil
}

// DeleteBatch removes multiple objects in a single operation
func (c *Client) DeleteBatch(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	const maxBatchSize = 1000

	for i := 0; i < len(keys); i += maxBatchSize {
		end := i + maxBatchSize
		if end > len(keys) {
			end = len(keys)
		}

		batch := keys[i:end]
		objects := make([]types.ObjectIdentifier, len(batch))
		for j, key := range batch {
			objects[j] = types.ObjectIdentifier{
				Key: aws.String(key),
			}
		}

		_, err := c.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(c.bucket),
			Delete: &types.Delete{
				Objects: objects,
				Quiet:   aws.Bool(true),
			},
		})
		if err != nil {
			return fmt.Errorf("failed to delete batch: %w", err)
		}
	}

	return nil
}

// List returns all objects with the given prefix
func (c *Client) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	var objects []storage.ObjectInfo
	var continuationToken *string

	for {
		input := &s3.ListObjectsV2Input{
			Bucket:            aws.String(c.bucket),
			ContinuationToken: continuationToken,
		}
		if prefix != "" {
			input.Prefix = aws.String(prefix)
		}

		result, err := c.client.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		for _, obj := range result.Contents {
			objects = append(objects, storage.ObjectInfo{
				Key:          aws.ToString(obj.Key),
				Size:         aws.ToInt64(obj.Size),
				LastModified: aws.ToTime(obj.LastModified),
				ETag:         aws.ToString(obj.ETag),
				Version:      aws.ToString(obj.ETag),
			})
		}

		if !aws.ToBool(result.IsTruncated) {
			break
		}
		continuationToken = result.NextContinuationToken
	}

	return objects, nil
}

// Head returns metadata for the given key without downloading content
func (c *Client) Head(ctx context.Context, key string) (*storage.ObjectInfo, error) {
	result, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}

	return &storage.ObjectInfo{
		Key:          key,
		Size:         aws.ToInt64(result.ContentLength),
		LastModified: aws.ToTime(result.LastModified),
		ETag:         aws.ToString(result.ETag),
		Version:      aws.ToString(result.ETag),
	}, nil
}

// DeleteIfUnchanged removes key only while its ETag still matches
// expectedVersion, so an object another device replaced in the meantime
// survives.
func (c *Client) DeleteIfUnchanged(ctx context.Context, key, expectedVersion string) error {
	if expectedVersion == "" {
		return fmt.Errorf("%w: no known version for %s", storage.ErrPreconditionFailed, key)
	}
	_, err := c.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:  aws.String(c.bucket),
		Key:     aws.String(key),
		IfMatch: aws.String(expectedVersion),
	})
	if err != nil {
		if isPreconditionFailed(err) {
			return fmt.Errorf("%w: %s", storage.ErrPreconditionFailed, key)
		}
		return fmt.Errorf("failed to delete %s: %w", key, err)
	}
	return nil
}

// isPreconditionFailed reports whether an S3 API error is a 412 rejection of
// our If-Match header.
func isPreconditionFailed(err error) bool {
	var respErr *awshttp.ResponseError
	if errors.As(err, &respErr) {
		return respErr.HTTPStatusCode() == http.StatusPreconditionFailed
	}
	return false
}

// Copy duplicates srcKey to dstKey inside the bucket without moving the bytes
// through this process. CopySource is a URL path, so each segment of the key
// is escaped; keys hold file names that legitimately contain spaces and "#".
func (c *Client) Copy(ctx context.Context, srcKey, dstKey string) error {
	_, err := c.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(c.bucket),
		Key:        aws.String(dstKey),
		CopySource: aws.String(escapeCopySource(c.bucket, srcKey)),
	})
	if err != nil {
		return fmt.Errorf("failed to copy %s to %s: %w", srcKey, dstKey, err)
	}
	return nil
}

// escapeCopySource builds the "bucket/key" value CopyObject expects, escaping
// the key one path segment at a time so slashes survive.
func escapeCopySource(bucket, key string) string {
	segments := strings.Split(key, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return bucket + "/" + strings.Join(segments, "/")
}

// BucketExists checks if the configured bucket exists
func (c *Client) BucketExists(ctx context.Context) (bool, error) {
	_, err := c.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(c.bucket),
	})
	if err != nil {
		var notFound *types.NotFound
		var noSuchBucket *types.NoSuchBucket
		if errors.As(err, &notFound) || errors.As(err, &noSuchBucket) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check bucket: %w", err)
	}
	return true, nil
}
