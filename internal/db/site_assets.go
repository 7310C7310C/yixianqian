package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrSiteAssetNotFound reports a missing public site asset (for example before
// the first 小天使二维码 upload).
var ErrSiteAssetNotFound = errors.New("db: site asset not found")

// SiteAsset is one public, admin-managed image (currently only angel_qr). The
// bytes live in PostgreSQL because the asset is small and must be available
// without R2 credentials; it is served only by the dedicated public route.
type SiteAsset struct {
	Key         string
	ContentType string
	ByteSize    int64
	Width       int
	Height      int
	Data        []byte
	ETag        string
	UpdatedBy   *int64
	UpdatedAt   time.Time
}

// GetSiteAsset loads one asset. ErrSiteAssetNotFound is returned when the key
// has never been uploaded.
func (d *DB) GetSiteAsset(ctx context.Context, key string) (SiteAsset, error) {
	var asset SiteAsset
	err := d.pool.QueryRow(ctx, `
		SELECT key, content_type, byte_size, width, height, data, etag,
		       updated_by, updated_at
		  FROM site_assets
		 WHERE key = $1`, key).Scan(
		&asset.Key, &asset.ContentType, &asset.ByteSize, &asset.Width,
		&asset.Height, &asset.Data, &asset.ETag, &asset.UpdatedBy, &asset.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SiteAsset{}, ErrSiteAssetNotFound
	}
	if err != nil {
		return SiteAsset{}, fmt.Errorf("db: get site asset: %w", err)
	}
	return asset, nil
}

// SiteAssetExists is the cheap check used by the register page to avoid
// rendering an <img> before the first upload.
func (d *DB) SiteAssetExists(ctx context.Context, key string) (bool, error) {
	var exists bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM site_assets WHERE key = $1)`, key).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("db: check site asset: %w", err)
	}
	return exists, nil
}

// UpsertSiteAsset replaces the bytes and metadata atomically.
func (d *DB) UpsertSiteAsset(ctx context.Context, asset SiteAsset) error {
	_, err := d.pool.Exec(ctx, `
		INSERT INTO site_assets
		    (key, content_type, byte_size, width, height, data, etag, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (key) DO UPDATE SET
		    content_type = EXCLUDED.content_type,
		    byte_size    = EXCLUDED.byte_size,
		    width        = EXCLUDED.width,
		    height       = EXCLUDED.height,
		    data         = EXCLUDED.data,
		    etag         = EXCLUDED.etag,
		    updated_by   = EXCLUDED.updated_by,
		    updated_at   = now()`,
		asset.Key, asset.ContentType, asset.ByteSize, asset.Width, asset.Height,
		asset.Data, asset.ETag, asset.UpdatedBy)
	if err != nil {
		return fmt.Errorf("db: upsert site asset: %w", err)
	}
	return nil
}

// DeleteSiteAsset is used by tests and by a future "restore default" action.
func (d *DB) DeleteSiteAsset(ctx context.Context, key string) error {
	_, err := d.pool.Exec(ctx, `DELETE FROM site_assets WHERE key = $1`, key)
	if err != nil {
		return fmt.Errorf("db: delete site asset: %w", err)
	}
	return nil
}
