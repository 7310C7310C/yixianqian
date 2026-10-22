package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const mediaColumns = `id, user_id, original_name, object_key, thumb_key,
	content_type, byte_size, thumb_size, width, height, created_at`

// CreateMedia records an uploaded object pair.
func (d *DB) CreateMedia(ctx context.Context, m Media) (Media, error) {
	err := d.pool.QueryRow(ctx,
		`INSERT INTO media
		   (id, user_id, original_name, object_key, thumb_key, content_type,
		    byte_size, thumb_size, width, height)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 RETURNING created_at`,
		m.ID, m.UserID, m.OriginalName, m.ObjectKey, m.ThumbKey, m.ContentType,
		m.ByteSize, m.ThumbSize, m.Width, m.Height).Scan(&m.CreatedAt)
	if err != nil {
		return Media{}, mapWriteError(err)
	}
	return m, nil
}

// ListMediaByUser returns the newest uploads first.
func (d *DB) ListMediaByUser(ctx context.Context, userID int64, limit int) ([]Media, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := d.pool.Query(ctx,
		"SELECT "+mediaColumns+` FROM media
		 WHERE user_id = $1
		 ORDER BY created_at DESC, id DESC
		 LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list media: %w", err)
	}
	defer rows.Close()

	var out []Media
	for rows.Next() {
		var m Media
		if err := rows.Scan(&m.ID, &m.UserID, &m.OriginalName, &m.ObjectKey, &m.ThumbKey,
			&m.ContentType, &m.ByteSize, &m.ThumbSize, &m.Width, &m.Height, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("db: scan media: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate media: %w", err)
	}
	return out, nil
}

// GetMediaForUser fetches one media row, enforcing ownership.
func (d *DB) GetMediaForUser(ctx context.Context, id string, userID int64) (Media, error) {
	var m Media
	err := d.pool.QueryRow(ctx,
		"SELECT "+mediaColumns+" FROM media WHERE id = $1 AND user_id = $2", id, userID).
		Scan(&m.ID, &m.UserID, &m.OriginalName, &m.ObjectKey, &m.ThumbKey,
			&m.ContentType, &m.ByteSize, &m.ThumbSize, &m.Width, &m.Height, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, ErrNotFound
	}
	if err != nil {
		return Media{}, fmt.Errorf("db: get media: %w", err)
	}
	return m, nil
}
