package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// CardPhotoBase is a media row plus the eligible gallery owners
// (profile_photos.user_id) that reference it. A media row can be shared by
// multiple galleries after the v1 migration (media.user_id is only the upload
// owner), so authorization and candidate grants always use the gallery owner;
// media.user_id is used only to load the stored object.
type CardPhotoBase struct {
	Media          Media
	GalleryUserIDs []int64
}

const cardPhotoBaseSelect = `SELECT m.id, m.user_id, m.original_name, m.object_key, m.thumb_key,
		        m.content_type, m.byte_size, m.thumb_size, m.width, m.height, m.created_at,
		        array_agg(DISTINCT pp.user_id)
		 FROM media m
		 JOIN profile_photos pp ON pp.media_id = m.id
		 JOIN users owner ON owner.id = pp.user_id
		 WHERE m.id = $1
		   AND owner.status = 'active'
		   AND (owner.id = $2 OR (
		         owner.account_kind = 'user'
		         AND owner.account_status IN ('active', 'matched', 'observer')))
		   AND EXISTS (
		     SELECT 1 FROM users viewer
		     WHERE viewer.id = $2 AND viewer.status = 'active'
		       AND viewer.account_status IN ('active', 'matched', 'observer')
		   )
		 GROUP BY m.id`

// GetCardPhotoBase loads one profile photo and the eligible gallery owners that
// reference it. It skips relationship subqueries; the HTTP layer checks those
// owners against the short-lived deck grant (and falls back to
// GetCardPhotoForViewer for persistent relationships). It still enforces owner
// and viewer eligibility, including an observer seeing their own gallery.
func (d *DB) GetCardPhotoBase(ctx context.Context, mediaID string, viewerID int64) (CardPhotoBase, error) {
	var out CardPhotoBase
	err := d.pool.QueryRow(ctx, cardPhotoBaseSelect, mediaID, viewerID).
		Scan(&out.Media.ID, &out.Media.UserID, &out.Media.OriginalName, &out.Media.ObjectKey,
			&out.Media.ThumbKey, &out.Media.ContentType, &out.Media.ByteSize, &out.Media.ThumbSize,
			&out.Media.Width, &out.Media.Height, &out.Media.CreatedAt, &out.GalleryUserIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return CardPhotoBase{}, ErrNotFound
	}
	if err != nil {
		return CardPhotoBase{}, fmt.Errorf("db: get card photo base: %w", err)
	}
	return out, nil
}

// GetCardPhotoForViewer loads a profile photo only when the viewer has one of
// the relationship-based rights accepted by the product. Authorization is
// evaluated against the gallery owner (`profile_photos.user_id`), not
// `media.user_id`, because migration data can share one media row across
// galleries.
//
//   - self (gallery owner);
//   - any admin role (RBAC);
//   - an active like in either direction (includes mutual likes);
//   - a non-cancelled signup in a shared activity (covers same activity, group
//     and round: group/round rows are always produced from activity signups);
//   - a non-deleted message in either direction (message counterparty).
//
// Deck candidates that have not liked each other yet are not a persistent
// relationship; they are authorized by the HTTP layer's short-lived grant
// recorded from the cards payload (see httpserver.photoGrants), never by a
// broad "any active user" predicate. Unauthorized and non-existent media both
// return ErrNotFound so callers can respond 404 without leaking existence.
func (d *DB) GetCardPhotoForViewer(ctx context.Context, mediaID string, viewerID int64) (Media, error) {
	var m Media
	err := d.pool.QueryRow(ctx,
		`SELECT m.id, m.user_id, m.original_name, m.object_key, m.thumb_key,
		        m.content_type, m.byte_size, m.thumb_size, m.width, m.height, m.created_at
		 FROM media m
		 JOIN profile_photos pp ON pp.media_id = m.id
		 JOIN users owner ON owner.id = pp.user_id
		 WHERE m.id = $1
		   AND owner.status = 'active'
		   AND (owner.id = $2 OR (
		         owner.account_kind = 'user'
		         AND owner.account_status IN ('active', 'matched', 'observer')))
		   AND EXISTS (
		     SELECT 1 FROM users viewer
		     WHERE viewer.id = $2 AND viewer.status = 'active'
		       AND viewer.account_status IN ('active', 'matched', 'observer')
		   )
		   AND (
		     pp.user_id = $2
		     OR EXISTS (
		       SELECT 1 FROM admin_user_roles ur WHERE ur.user_id = $2
		     )
		     OR EXISTS (
		       SELECT 1 FROM likes l
		       WHERE l.status IN ($3, $4)
		         AND l.initiator_id = $2 AND l.target_id = pp.user_id
		     )
		     OR EXISTS (
		       SELECT 1 FROM likes l
		       WHERE l.status IN ($3, $4)
		         AND l.target_id = $2 AND l.initiator_id = pp.user_id
		     )
		     OR EXISTS (
		       SELECT 1 FROM signups s_self
		       JOIN signups s_owner ON s_owner.activity_id = s_self.activity_id
		       WHERE s_self.user_id = $2
		         AND s_owner.user_id = pp.user_id
		         AND s_self.status <> $5
		         AND s_owner.status <> $5
		     )
		     OR EXISTS (
		       SELECT 1 FROM messages msg
		       WHERE msg.status <> $6
		         AND msg.author_id = $2 AND msg.target_user_id = pp.user_id
		     )
		     OR EXISTS (
		       SELECT 1 FROM messages msg
		       WHERE msg.status <> $6
		         AND msg.target_user_id = $2 AND msg.author_id = pp.user_id
		     )
		   )
		 LIMIT 1`,
		mediaID, viewerID, LikeStatusSingle, LikeStatusMutual,
		SignupStatusCancelled, MessageStatusDeleted).
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

// CanViewGallery reports whether viewerID may see galleryUserID's profile
// photos based on the viewer's relationship with that specific gallery owner
// (self, admin, like in either direction, shared activity, or message). It is
// used by the public profile endpoint, which must not accept a relationship
// with another gallery that happens to share the same media row.
func (d *DB) CanViewGallery(ctx context.Context, galleryUserID, viewerID int64) (bool, error) {
	if galleryUserID <= 0 || viewerID <= 0 {
		return false, nil
	}
	var allowed bool
	err := d.pool.QueryRow(ctx,
		`SELECT EXISTS (
		   SELECT 1
		   FROM users owner
		   JOIN users viewer ON viewer.id = $2
		   WHERE owner.id = $1
		     AND owner.status = 'active'
		     AND (owner.id = $2 OR (
		           owner.account_kind = 'user'
		           AND owner.account_status IN ('active', 'matched', 'observer')))
		     AND viewer.status = 'active'
		     AND viewer.account_status IN ('active', 'matched', 'observer')
		     AND (
		       owner.id = $2
		       OR EXISTS (SELECT 1 FROM admin_user_roles ur WHERE ur.user_id = $2)
		       OR EXISTS (
		         SELECT 1 FROM likes l
		         WHERE l.status IN ($3, $4)
		           AND l.initiator_id = $2 AND l.target_id = owner.id
		       )
		       OR EXISTS (
		         SELECT 1 FROM likes l
		         WHERE l.status IN ($3, $4)
		           AND l.target_id = $2 AND l.initiator_id = owner.id
		       )
		       OR EXISTS (
		         SELECT 1 FROM signups s_self
		         JOIN signups s_owner ON s_owner.activity_id = s_self.activity_id
		         WHERE s_self.user_id = $2 AND s_owner.user_id = owner.id
		           AND s_self.status <> $5 AND s_owner.status <> $5
		       )
		       OR EXISTS (
		         SELECT 1 FROM messages msg
		         WHERE msg.status <> $6
		           AND msg.author_id = $2 AND msg.target_user_id = owner.id
		       )
		       OR EXISTS (
		         SELECT 1 FROM messages msg
		         WHERE msg.status <> $6
		           AND msg.target_user_id = $2 AND msg.author_id = owner.id
		       )
		     )
		 )`, galleryUserID, viewerID, LikeStatusSingle, LikeStatusMutual,
		SignupStatusCancelled, MessageStatusDeleted).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("db: can view gallery: %w", err)
	}
	return allowed, nil
}
