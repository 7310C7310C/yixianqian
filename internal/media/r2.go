package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// R2Config describes a Cloudflare R2 (or any S3-compatible) bucket.
type R2Config struct {
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	Secure          bool
}

// R2Store streams objects through the server; bucket URLs are never exposed.
type R2Store struct {
	client *minio.Client
	bucket string
}

// NewR2 builds a MinIO client for the configured endpoint.
func NewR2(cfg R2Config) (*R2Store, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, errors.New("media: incomplete R2 configuration")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.Secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("media: create R2 client: %w", err)
	}
	return &R2Store{client: client, bucket: cfg.Bucket}, nil
}

// EnsureBucket verifies the bucket exists; it never creates one.
func (s *R2Store) EnsureBucket(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("media: check bucket: %w", err)
	}
	if !ok {
		return fmt.Errorf("media: bucket %q does not exist", s.bucket)
	}
	return nil
}

func (s *R2Store) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{
			ContentType:  contentType,
			CacheControl: "private, max-age=0, no-store",
		})
	if err != nil {
		return fmt.Errorf("media: put object: %w", err)
	}
	return nil
}

func (s *R2Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("media: get object: %w", err)
	}
	return obj, nil
}

// IsObjectNotFound reports whether an object read failed because the key is
// missing. GetObject is lazy, so this is used after the first Read.
func IsObjectNotFound(err error) bool {
	return isNotFound(err)
}

func (s *R2Store) Remove(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		return fmt.Errorf("media: remove object: %w", err)
	}
	return nil
}

func isNotFound(err error) bool {
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.Code == "NoSuchKey" || resp.Code == "NoSuchObject" || resp.StatusCode == 404
	}
	return false
}
