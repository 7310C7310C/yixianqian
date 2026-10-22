// Package media validates images and stores them in an S3-compatible bucket.
package media

import (
	"context"
	"errors"
	"io"
)

var ErrObjectNotFound = errors.New("media: object not found")

// Store is the narrow S3 surface used by the app, kept small so handlers can
// be tested with an in-memory fake.
type Store interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Remove(ctx context.Context, key string) error
}
