package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// ErrPhotoDailyLimit reports that a photo-quota reservation would exceed the
// configured per-account daily limit. Callers translate it to HTTP 429.
var ErrPhotoDailyLimit = errors.New("db: photo daily limit reached")

// queryRower is implemented by *pgxpool.Pool and pgx.Tx so the same quota SQL
// can run standalone or inside the registration transaction.
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// TakePhotoQuota atomically reserves n photo uploads for userID on day.
//
// The single upsert serialises concurrent reservations on the (user_id, day)
// primary key: the ON CONFLICT WHERE clause only applies the increment when
// the new total stays within limit, and the INSERT branch only writes a first
// row when n itself fits. A denied reservation leaves used unchanged and
// returns allowed=false with the current used count.
//
// limit <= 0 means "no limit": the call always allows and writes no row.
// n <= 0 is treated the same way (nothing to reserve).
func (d *DB) TakePhotoQuota(ctx context.Context, userID int64, day time.Time, n, limit int) (bool, int, error) {
	return takePhotoQuota(ctx, d.pool, userID, day, n, limit)
}

func takePhotoQuota(ctx context.Context, q queryRower, userID int64, day time.Time, n, limit int) (bool, int, error) {
	if limit <= 0 || n <= 0 {
		return true, 0, nil
	}
	var used int
	err := q.QueryRow(ctx, `
		INSERT INTO photo_upload_daily AS q (user_id, day, used)
		SELECT $1::bigint, $2::date, $3::integer WHERE $3::integer <= $4::integer
		ON CONFLICT (user_id, day) DO UPDATE
		  SET used = q.used + EXCLUDED.used
		  WHERE q.used + EXCLUDED.used <= $4::integer
		RETURNING used`,
		userID, day, n, limit).Scan(&used)
	if err == nil {
		return true, used, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, 0, fmt.Errorf("db: take photo quota: %w", err)
	}
	// Denied: report the unchanged counter so callers/logs can show progress.
	var current int
	err = q.QueryRow(ctx,
		`SELECT COALESCE((SELECT used FROM photo_upload_daily
		                   WHERE user_id = $1 AND day = $2), 0)`,
		userID, day).Scan(&current)
	if err != nil {
		return false, 0, fmt.Errorf("db: read photo quota: %w", err)
	}
	return false, current, nil
}

// PrunePhotoQuota deletes quota rows older than before. v1 kept only today and
// yesterday, so callers pass yesterday's date (in the quota timezone).
func (d *DB) PrunePhotoQuota(ctx context.Context, before time.Time) (int64, error) {
	tag, err := d.pool.Exec(ctx,
		`DELETE FROM photo_upload_daily WHERE day < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("db: prune photo quota: %w", err)
	}
	return tag.RowsAffected(), nil
}
