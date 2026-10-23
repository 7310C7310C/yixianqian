package db

import (
	"context"
	"fmt"
	"time"
)

// The v1 SPA's 麦穗管理 page reads aggregated views that predate the v2 admin
// console. These queries keep that contract in one place so the legacy adapter
// stays a pure shape mapper and the SQL can be tested against the real schema.

// LegacyPointsOverviewStats backs the four number tiles and the totals line of
// the SPA overview. Earned/Spent are plain ledger sums so that
// Earned - Spent == Outstanding for every data set.
type LegacyPointsOverviewStats struct {
	UserCount       int
	EntryCount      int
	Earned          int64
	Spent           int64
	Outstanding     int64
	RedemptionCount int
	InviteCount     int
}

// LegacyPointsUserRow is one user card in the SPA overview list.
type LegacyPointsUserRow struct {
	ID           int64
	LegacyUserID string
	Name         string
	Nickname     string
	Earned       int
	Spent        int
	EntryCount   int
	LastAt       *time.Time
	Balance      int
}

// LegacyPointsKindTotal groups positive ledger entries by kind.
type LegacyPointsKindTotal struct {
	Kind  string
	Total int
	Count int
}

// LegacyPointsItemTotal groups redemption orders by item.
type LegacyPointsItemTotal struct {
	Item  string
	Total int
	Count int
}

// LegacyPointsOverviewStats aggregates the whole ledger plus the two work
// counters shown on the page.
func (d *DB) LegacyPointsOverviewStats(ctx context.Context) (LegacyPointsOverviewStats, error) {
	var s LegacyPointsOverviewStats
	err := d.pool.QueryRow(ctx,
		`SELECT count(DISTINCT user_id), count(*),
		        COALESCE(SUM(delta) FILTER (WHERE delta > 0), 0),
		        COALESCE(-SUM(delta) FILTER (WHERE delta < 0), 0),
		        COALESCE(SUM(delta), 0)
		   FROM points_ledger`).
		Scan(&s.UserCount, &s.EntryCount, &s.Earned, &s.Spent, &s.Outstanding)
	if err != nil {
		return LegacyPointsOverviewStats{}, fmt.Errorf("db: legacy points overview stats: %w", err)
	}
	err = d.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM points_redemptions),
		        (SELECT count(*) FROM invites)`).
		Scan(&s.RedemptionCount, &s.InviteCount)
	if err != nil {
		return LegacyPointsOverviewStats{}, fmt.Errorf("db: legacy points work counts: %w", err)
	}
	return s, nil
}

// ListLegacyPointsUsers lists users that have ledger history, newest activity
// first. The SPA filters this list client-side, so the limit stays generous.
func (d *DB) ListLegacyPointsUsers(ctx context.Context, limit int) ([]LegacyPointsUserRow, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := d.pool.Query(ctx,
		`SELECT u.id, COALESCE(u.legacy_user_id, ''), u.name,
		        COALESCE(u.nickname, ''),
		        COALESCE(SUM(l.delta) FILTER (WHERE l.delta > 0), 0),
		        COALESCE(-SUM(l.delta) FILTER (WHERE l.delta < 0), 0),
		        count(l.id), max(l.created_at),
		        COALESCE(SUM(l.delta), 0)
		   FROM users u
		   JOIN points_ledger l ON l.user_id = u.id
		  GROUP BY u.id, u.legacy_user_id, u.name, u.nickname
		  ORDER BY max(l.created_at) DESC, u.id DESC
		  LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("db: list legacy points users: %w", err)
	}
	defer rows.Close()
	out := make([]LegacyPointsUserRow, 0)
	for rows.Next() {
		var (
			row    LegacyPointsUserRow
			lastAt time.Time
		)
		if err := rows.Scan(&row.ID, &row.LegacyUserID, &row.Name, &row.Nickname,
			&row.Earned, &row.Spent, &row.EntryCount, &lastAt, &row.Balance); err != nil {
			return nil, fmt.Errorf("db: scan legacy points user: %w", err)
		}
		row.LastAt = &lastAt
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate legacy points users: %w", err)
	}
	return out, nil
}

// LegacyPointsEarnedByKind breaks a user's positive ledger entries down by
// kind, largest total first.
func (d *DB) LegacyPointsEarnedByKind(ctx context.Context, userID int64) ([]LegacyPointsKindTotal, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT kind, SUM(delta), count(*)
		   FROM points_ledger
		  WHERE user_id = $1 AND delta > 0
		  GROUP BY kind
		  ORDER BY SUM(delta) DESC, kind`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: legacy points earned by kind: %w", err)
	}
	defer rows.Close()
	out := make([]LegacyPointsKindTotal, 0)
	for rows.Next() {
		var row LegacyPointsKindTotal
		if err := rows.Scan(&row.Kind, &row.Total, &row.Count); err != nil {
			return nil, fmt.Errorf("db: scan legacy points kind total: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate legacy points kind totals: %w", err)
	}
	return out, nil
}

// LegacyPointsRedeemedByItem breaks a user's redemption orders down by item.
func (d *DB) LegacyPointsRedeemedByItem(ctx context.Context, userID int64) ([]LegacyPointsItemTotal, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT item, SUM(cost), count(*)
		   FROM points_redemptions
		  WHERE user_id = $1
		  GROUP BY item
		  ORDER BY SUM(cost) DESC, item`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: legacy points redeemed by item: %w", err)
	}
	defer rows.Close()
	out := make([]LegacyPointsItemTotal, 0)
	for rows.Next() {
		var row LegacyPointsItemTotal
		if err := rows.Scan(&row.Item, &row.Total, &row.Count); err != nil {
			return nil, fmt.Errorf("db: scan legacy points item total: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate legacy points item totals: %w", err)
	}
	return out, nil
}
