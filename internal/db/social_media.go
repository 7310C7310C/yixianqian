package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// GetCardPhotoForViewer returns a profile photo (original or thumbnail row)
// only when it belongs to a real user's profile gallery and the viewer is a
// permitted logged-in member. Photos are still streamed through the app; R2
// keys never leave the server.
func (d *DB) GetCardPhotoForViewer(ctx context.Context, mediaID string, viewerID int64) (Media, error) {
	var m Media
	err := d.pool.QueryRow(ctx,
		`SELECT m.id, m.user_id, m.original_name, m.object_key, m.thumb_key,
		        m.content_type, m.byte_size, m.thumb_size, m.width, m.height, m.created_at
		 FROM media m
		 JOIN profile_photos pp ON pp.media_id = m.id
		 JOIN users owner ON owner.id = m.user_id
		 WHERE m.id = $1
		   AND owner.account_kind = 'user'
		   AND owner.status = 'active'
		   AND owner.account_status NOT IN ('left', 'disabled')
		   AND EXISTS (
		     SELECT 1 FROM users viewer
		     WHERE viewer.id = $2 AND viewer.status = 'active'
		       AND viewer.account_status IN ('active', 'matched', 'observer')
		   )`, mediaID, viewerID).
		Scan(&m.ID, &m.UserID, &m.OriginalName, &m.ObjectKey, &m.ThumbKey,
			&m.ContentType, &m.ByteSize, &m.ThumbSize, &m.Width, &m.Height, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Media{}, ErrNotFound
	}
	if err != nil {
		return Media{}, fmt.Errorf("db: get card photo: %w", err)
	}
	return m, nil
}
