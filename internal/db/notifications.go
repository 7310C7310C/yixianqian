package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// Notification is one in-app message for a user.
type Notification struct {
	ID        int64
	UserID    int64
	Kind      string
	Title     string
	Body      string
	Link      string
	Payload   map[string]any
	ReadAt    *time.Time
	CreatedAt time.Time
}

// NotificationInput describes a notification to create. DedupeKey is stored in
// payload.dedupe_key so the schema needs no extra unique column. When non-empty,
// later calls with the same key are ignored.
type NotificationInput struct {
	UserID    int64
	Kind      string
	Title     string
	Body      string
	Link      string
	Payload   map[string]any
	DedupeKey string
}

// CreateNotification appends an in-app notification.
func (d *DB) CreateNotification(ctx context.Context, userID int64, kind, title, body string) error {
	_, err := d.pool.Exec(ctx,
		`INSERT INTO notifications (user_id, kind, title, body) VALUES ($1, $2, $3, $4)`,
		userID, kind, title, body)
	if err != nil {
		return fmt.Errorf("db: create notification: %w", err)
	}
	return nil
}

// CreateNotificationOnce inserts a notification unless the dedupe key already
// exists. An advisory transaction lock serialises concurrent callers sharing a
// key even though the schema has no unique index for it.
func (d *DB) CreateNotificationOnce(ctx context.Context, in NotificationInput) (bool, error) {
	if in.UserID == 0 || in.Kind == "" || in.Title == "" {
		return false, fmt.Errorf("db: notification user, kind and title are required")
	}
	payload := make(map[string]any, len(in.Payload)+1)
	for k, v := range in.Payload {
		payload[k] = v
	}
	if in.DedupeKey != "" {
		payload["dedupe_key"] = in.DedupeKey
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("db: marshal notification payload: %w", err)
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("db: begin notification: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if in.DedupeKey != "" {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"notification:"+in.DedupeKey); err != nil {
			return false, fmt.Errorf("db: lock notification key: %w", err)
		}
		var exists bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (
			   SELECT 1 FROM notifications WHERE payload->>'dedupe_key' = $1
			 )`, in.DedupeKey).Scan(&exists); err != nil {
			return false, fmt.Errorf("db: check notification key: %w", err)
		}
		if exists {
			return false, nil
		}
	}

	tag, err := tx.Exec(ctx,
		`INSERT INTO notifications (user_id, kind, title, body, link, payload)
		 VALUES ($1, $2, $3, $4, $5, $6::jsonb)`,
		in.UserID, in.Kind, in.Title, in.Body, in.Link, string(raw))
	if err != nil {
		return false, fmt.Errorf("db: create notification: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("db: commit notification: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// CreateNotificationsOnce is the batch form of CreateNotificationOnce used by
// site-wide broadcasts. It locks every dedupe key in sorted order (so two
// broadcasts cannot deadlock), filters keys that already exist, and inserts the
// rest with one statement inside one transaction. Duplicate keys inside the
// input keep the first entry.
func (d *DB) CreateNotificationsOnce(ctx context.Context, inputs []NotificationInput) (int, error) {
	if len(inputs) == 0 {
		return 0, nil
	}
	type row struct {
		userID    int64
		kind      string
		title     string
		body      string
		link      string
		payload   string
		dedupeKey string
	}
	rows := make([]row, 0, len(inputs))
	seen := make(map[string]bool, len(inputs))
	keys := make([]string, 0, len(inputs))
	for _, in := range inputs {
		if in.UserID == 0 || in.Kind == "" || in.Title == "" {
			return 0, fmt.Errorf("db: notification user, kind and title are required")
		}
		if in.DedupeKey != "" && seen[in.DedupeKey] {
			continue
		}
		payload := make(map[string]any, len(in.Payload)+1)
		for k, v := range in.Payload {
			payload[k] = v
		}
		if in.DedupeKey != "" {
			payload["dedupe_key"] = in.DedupeKey
			seen[in.DedupeKey] = true
			keys = append(keys, in.DedupeKey)
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, fmt.Errorf("db: marshal notification payload: %w", err)
		}
		rows = append(rows, row{
			userID: in.UserID, kind: in.Kind, title: in.Title,
			body: in.Body, link: in.Link, payload: string(raw), dedupeKey: in.DedupeKey,
		})
	}

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("db: begin notifications batch: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"notification:"+key); err != nil {
			return 0, fmt.Errorf("db: lock notification key: %w", err)
		}
	}
	existing := make(map[string]bool, len(keys))
	if len(keys) > 0 {
		existingRows, err := tx.Query(ctx,
			`SELECT payload->>'dedupe_key' FROM notifications
			  WHERE payload->>'dedupe_key' = ANY($1)`, keys)
		if err != nil {
			return 0, fmt.Errorf("db: check notification keys: %w", err)
		}
		for existingRows.Next() {
			var key string
			if err := existingRows.Scan(&key); err != nil {
				existingRows.Close()
				return 0, fmt.Errorf("db: scan notification key: %w", err)
			}
			existing[key] = true
		}
		existingRows.Close()
		if err := existingRows.Err(); err != nil {
			return 0, fmt.Errorf("db: iterate notification keys: %w", err)
		}
	}

	userIDs := make([]int64, 0, len(rows))
	kinds := make([]string, 0, len(rows))
	titles := make([]string, 0, len(rows))
	bodies := make([]string, 0, len(rows))
	links := make([]string, 0, len(rows))
	payloads := make([]string, 0, len(rows))
	for _, item := range rows {
		if item.dedupeKey != "" && existing[item.dedupeKey] {
			continue
		}
		userIDs = append(userIDs, item.userID)
		kinds = append(kinds, item.kind)
		titles = append(titles, item.title)
		bodies = append(bodies, item.body)
		links = append(links, item.link)
		payloads = append(payloads, item.payload)
	}
	created := 0
	if len(userIDs) > 0 {
		tag, err := tx.Exec(ctx, `
			INSERT INTO notifications (user_id, kind, title, body, link, payload)
			SELECT user_id, kind, title, body, link, payload::jsonb
			  FROM unnest($1::bigint[], $2::text[], $3::text[], $4::text[], $5::text[], $6::text[])
			       AS batch(user_id, kind, title, body, link, payload)`,
			userIDs, kinds, titles, bodies, links, payloads)
		if err != nil {
			return 0, fmt.Errorf("db: create notifications batch: %w", err)
		}
		created = int(tag.RowsAffected())
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("db: commit notifications batch: %w", err)
	}
	return created, nil
}

// ListNotifications returns the current user's notifications, newest first.
func (d *DB) ListNotifications(ctx context.Context, userID int64, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := d.pool.Query(ctx,
		`SELECT id, user_id, kind, title, body, link, payload, read_at, created_at
		 FROM notifications
		 WHERE user_id = $1
		 ORDER BY created_at DESC, id DESC
		 LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list notifications: %w", err)
	}
	defer rows.Close()

	out := make([]Notification, 0)
	for rows.Next() {
		var n Notification
		var payload []byte
		if err := rows.Scan(&n.ID, &n.UserID, &n.Kind, &n.Title, &n.Body, &n.Link,
			&payload, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		n.Payload = map[string]any{}
		if len(payload) > 0 {
			if err := json.Unmarshal(payload, &n.Payload); err != nil {
				return nil, fmt.Errorf("db: decode notification payload: %w", err)
			}
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// UnreadNotificationCount counts unread rows for one owner.
func (d *DB) UnreadNotificationCount(ctx context.Context, userID int64) (int, error) {
	var count int
	err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`,
		userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("db: count unread notifications: %w", err)
	}
	return count, nil
}

// MarkNotificationRead marks one notification read for its owner. It reports
// false when the id does not belong to the user.
func (d *DB) MarkNotificationRead(ctx context.Context, userID, notificationID int64) (bool, error) {
	tag, err := d.pool.Exec(ctx,
		`UPDATE notifications SET read_at = now()
		 WHERE id = $1 AND user_id = $2 AND read_at IS NULL`,
		notificationID, userID)
	if err != nil {
		return false, fmt.Errorf("db: mark notification read: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return true, nil
	}
	var exists bool
	err = d.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM notifications WHERE id = $1 AND user_id = $2)`,
		notificationID, userID).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("db: check notification owner: %w", err)
	}
	return exists, nil
}

// MarkAllNotificationsRead marks every unread notification for the user.
func (d *DB) MarkAllNotificationsRead(ctx context.Context, userID int64) (int64, error) {
	tag, err := d.pool.Exec(ctx,
		`UPDATE notifications SET read_at = now()
		 WHERE user_id = $1 AND read_at IS NULL`, userID)
	if err != nil {
		return 0, fmt.Errorf("db: mark all notifications read: %w", err)
	}
	return tag.RowsAffected(), nil
}
