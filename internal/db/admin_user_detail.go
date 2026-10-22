package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminUserReview is the reviewer attribution stored on the user row.
type AdminUserReview struct {
	ReviewerName     string
	ReviewerLegacyID string
	ReviewedAt       *time.Time
	Note             string
}

// AdminUserAuditEntry is the safe projection of one audit_log row. Only the
// whitelisted metadata keys are loaded, never the whole payload.
type AdminUserAuditEntry struct {
	ID             int64
	CreatedAt      time.Time
	Action         string
	ActorName      string
	ActorLegacyID  string
	Note           string
	PreviousStatus string
}

// GetAdminUserReview loads who reviewed the account and the stored note.
func (d *DB) GetAdminUserReview(ctx context.Context, userID int64) (AdminUserReview, error) {
	var out AdminUserReview
	err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(rv.name, ''), COALESCE(rv.legacy_user_id, ''),
		        u.reviewed_at, u.review_note
		 FROM users u
		 LEFT JOIN users rv ON rv.id = u.reviewed_by
		 WHERE u.id = $1`, userID).
		Scan(&out.ReviewerName, &out.ReviewerLegacyID, &out.ReviewedAt, &out.Note)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminUserReview{}, ErrNotFound
	}
	if err != nil {
		return AdminUserReview{}, fmt.Errorf("db: get admin user review: %w", err)
	}
	return out, nil
}

// ListAdminUserAudit returns recent audit rows for one user, newest first.
func (d *DB) ListAdminUserAudit(ctx context.Context, userID int64, limit int) ([]AdminUserAuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := d.pool.Query(ctx,
		`SELECT a.id, a.created_at, a.action,
		        COALESCE(actor.name, ''), COALESCE(actor.legacy_user_id, ''),
		        COALESCE(a.metadata->>'note', ''),
		        COALESCE(a.metadata->>'previous_status', '')
		 FROM audit_log a
		 LEFT JOIN users actor ON actor.id = a.actor_user_id
		 WHERE a.target_type = 'user' AND a.target_id = $1
		 ORDER BY a.created_at DESC, a.id DESC
		 LIMIT $2`, strconv.FormatInt(userID, 10), limit)
	if err != nil {
		return nil, fmt.Errorf("db: list admin user audit: %w", err)
	}
	defer rows.Close()

	out := make([]AdminUserAuditEntry, 0, limit)
	for rows.Next() {
		var entry AdminUserAuditEntry
		if err := rows.Scan(&entry.ID, &entry.CreatedAt, &entry.Action,
			&entry.ActorName, &entry.ActorLegacyID, &entry.Note,
			&entry.PreviousStatus); err != nil {
			return nil, fmt.Errorf("db: scan admin user audit: %w", err)
		}
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate admin user audit: %w", err)
	}
	return out, nil
}

// GetAdminUserPhotoMedia loads a media row only when it is attached to the
// given profile. The object keys stay server-side for the streaming handler.
func (d *DB) GetAdminUserPhotoMedia(ctx context.Context, userID int64, mediaID string) (Media, error) {
	var m Media
	err := d.pool.QueryRow(ctx,
		`SELECT m.id, m.user_id, m.original_name, m.object_key, m.thumb_key,
		        m.content_type, m.byte_size, m.thumb_size, m.width, m.height, m.created_at
		 FROM profile_photos pp
		 JOIN media m ON m.id = pp.media_id
		 WHERE pp.user_id = $1 AND pp.media_id = $2`, userID, mediaID).
		Scan(&m.ID, &m.UserID, &m.OriginalName, &m.ObjectKey, &m.ThumbKey,
			&m.ContentType, &m.ByteSize, &m.ThumbSize, &m.Width, &m.Height, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, ErrNotFound
	}
	if err != nil {
		return Media{}, fmt.Errorf("db: get admin user photo media: %w", err)
	}
	return m, nil
}
