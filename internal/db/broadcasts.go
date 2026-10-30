package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Broadcast is one site-wide admin message (text plus optional image).
type Broadcast struct {
	ID             int64
	CreatedBy      *int64
	CreatorName    string
	Title          string
	Body           string
	MediaID        string
	Audience       string
	RecipientCount int
	CreatedAt      time.Time
}

// CreateBroadcast stores one admin message and fans it out to every active
// account in a single transaction. Notifications carry the broadcast id and
// optional media id so the feed can render the image without exposing R2.
func (d *DB) CreateBroadcast(ctx context.Context, createdBy int64, title, body, mediaID string) (Broadcast, int, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Broadcast{}, 0, fmt.Errorf("db: begin broadcast: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var b Broadcast
	if err := tx.QueryRow(ctx,
		`INSERT INTO broadcasts (created_by, title, body, media_id)
		 VALUES ($1, $2, $3, NULLIF($4, ''))
		 RETURNING id, created_at`,
		createdBy, title, body, mediaID).
		Scan(&b.ID, &b.CreatedAt); err != nil {
		return Broadcast{}, 0, fmt.Errorf("db: insert broadcast: %w", err)
	}
	payload := map[string]any{"broadcast_id": b.ID}
	if mediaID != "" {
		payload["media_id"] = mediaID
		payload["image_url"] = "/media/" + mediaID
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Broadcast{}, 0, fmt.Errorf("db: marshal broadcast payload: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO notifications (user_id, kind, title, body, link, payload)
		 SELECT id, 'admin_broadcast', $1, $2, '', $3::jsonb
		   FROM users WHERE status = 'active'`,
		title, body, string(raw))
	if err != nil {
		return Broadcast{}, 0, fmt.Errorf("db: create broadcast notifications: %w", err)
	}
	sent := int(tag.RowsAffected())
	if _, err := tx.Exec(ctx,
		`UPDATE broadcasts SET recipient_count = $2 WHERE id = $1`, b.ID, sent); err != nil {
		return Broadcast{}, 0, fmt.Errorf("db: update broadcast recipient count: %w", err)
	}
	if sent > 0 {
		if _, err := tx.Exec(ctx,
			`SELECT pg_notify($1, id::text) FROM users WHERE status = 'active'`,
			NotificationChannel); err != nil {
			return Broadcast{}, 0, fmt.Errorf("db: notify broadcast recipients: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Broadcast{}, 0, fmt.Errorf("db: commit broadcast: %w", err)
	}
	b.CreatedBy = &createdBy
	b.Title = title
	b.Body = body
	b.MediaID = mediaID
	b.Audience = "all_active"
	b.RecipientCount = sent
	return b, sent, nil
}

// ListBroadcasts returns the newest admin messages for the console.
func (d *DB) ListBroadcasts(ctx context.Context, limit int) ([]Broadcast, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := d.pool.Query(ctx,
		`SELECT b.id, b.created_by,
		        COALESCE(NULLIF(u.nickname, ''), u.name, ''),
		        b.title, b.body, COALESCE(b.media_id, ''), b.audience,
		        b.recipient_count, b.created_at
		   FROM broadcasts b
		   LEFT JOIN users u ON u.id = b.created_by
		  ORDER BY b.created_at DESC, b.id DESC
		  LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list broadcasts: %w", err)
	}
	defer rows.Close()
	out := make([]Broadcast, 0)
	for rows.Next() {
		var b Broadcast
		if err := rows.Scan(&b.ID, &b.CreatedBy, &b.CreatorName, &b.Title, &b.Body,
			&b.MediaID, &b.Audience, &b.RecipientCount, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetMediaForViewer allows the owner and any signed-in viewer when the media is
// referenced by a site-wide broadcast. Broadcast uploads stay private until the
// broadcast row exists.
func (d *DB) GetMediaForViewer(ctx context.Context, id string, userID int64) (Media, error) {
	var m Media
	err := d.pool.QueryRow(ctx,
		`SELECT `+mediaColumns+`
		   FROM media m
		  WHERE m.id = $1
		    AND (m.user_id = $2
		         OR EXISTS (SELECT 1 FROM broadcasts b WHERE b.media_id = m.id))`,
		id, userID).
		Scan(&m.ID, &m.UserID, &m.OriginalName, &m.ObjectKey, &m.ThumbKey,
			&m.ContentType, &m.ByteSize, &m.ThumbSize, &m.Width, &m.Height, &m.CreatedAt)
	if err == pgx.ErrNoRows {
		return Media{}, ErrNotFound
	}
	if err != nil {
		return Media{}, fmt.Errorf("db: get media for viewer: %w", err)
	}
	return m, nil
}
