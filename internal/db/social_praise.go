package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// PraiseState is the private daily praise view for one user. ReceivedWeek is
// only ever returned to that same user.
type PraiseState struct {
	Limit        int     `json:"limit"`
	Left         int     `json:"left"`
	Used         int     `json:"used"`
	Praised      []int64 `json:"praised"`
	ReceivedWeek int     `json:"received_week"`
}

// PraiseResult reports whether a praise was already active and the new state.
type PraiseResult struct {
	Already bool
	State   PraiseState
}

type socialQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// PraiseStateFor loads the viewer's daily quota, praised targets and weekly
// received count (the only place the received count is exposed).
func (d *DB) PraiseStateFor(ctx context.Context, viewerID int64, now time.Time) (PraiseState, error) {
	return loadPraiseState(ctx, d.pool, viewerID, now)
}

func loadPraiseState(ctx context.Context, q socialQueryer, viewerID int64, now time.Time) (PraiseState, error) {
	state := PraiseState{Limit: DailyPraiseLimit, Praised: []int64{}}
	day := DayKey(now)
	week := WeekKey(now)

	var used int
	var praised []int64
	err := q.QueryRow(ctx,
		`SELECT
		   (SELECT count(*) FROM praises
		     WHERE initiator_id = $1 AND day = $2 AND status = $3),
		   (SELECT COALESCE(array_agg(DISTINCT target_id ORDER BY target_id), '{}'::bigint[])
		      FROM praises WHERE initiator_id = $1 AND status = $3),
		   (SELECT count(*) FROM praises
		     WHERE target_id = $1 AND week = $4 AND status = $3)`,
		viewerID, day, PraiseStatusActive, week).
		Scan(&used, &praised, &state.ReceivedWeek)
	if err != nil {
		return state, fmt.Errorf("db: praise state: %w", err)
	}
	if praised != nil {
		state.Praised = praised
	}
	state.Used = used
	state.Left = DailyPraiseLimit - used
	if state.Left < 0 {
		state.Left = 0
	}
	return state, nil
}

// CreatePraise writes one anonymous daily praise. Repeated active praise is a
// no-op success (v1 behavior), and cancelled praise does not consume quota.
func (d *DB) CreatePraise(ctx context.Context, initiatorID, targetID int64, now time.Time) (PraiseResult, error) {
	if initiatorID == targetID {
		return PraiseResult{}, ErrSelfAction
	}
	if now.IsZero() {
		now = SocialNow()
	}
	day := DayKey(now)
	week := WeekKey(now)

	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return PraiseResult{}, fmt.Errorf("db: begin praise: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	me, err := lockSocialActor(ctx, tx, initiatorID)
	if err != nil {
		return PraiseResult{}, err
	}
	target, err := socialUserState(ctx, tx, targetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PraiseResult{}, ErrNotFound
	}
	if err != nil {
		return PraiseResult{}, fmt.Errorf("db: load praise target: %w", err)
	}
	if err := validateSocialPair(me, target); err != nil {
		return PraiseResult{}, err
	}

	var existing int64
	err = tx.QueryRow(ctx,
		`SELECT id FROM praises
		 WHERE initiator_id = $1 AND target_id = $2 AND status = $3
		 LIMIT 1`, initiatorID, targetID, PraiseStatusActive).Scan(&existing)
	switch {
	case err == nil:
		state, serr := loadPraiseState(ctx, tx, initiatorID, now)
		if serr != nil {
			return PraiseResult{}, serr
		}
		if err := tx.Commit(ctx); err != nil {
			return PraiseResult{}, fmt.Errorf("db: commit duplicate praise: %w", err)
		}
		return PraiseResult{Already: true, State: state}, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return PraiseResult{}, fmt.Errorf("db: find praise: %w", err)
	}

	var used int
	err = tx.QueryRow(ctx,
		`SELECT count(*) FROM praises
		 WHERE initiator_id = $1 AND day = $2 AND status = $3`,
		initiatorID, day, PraiseStatusActive).Scan(&used)
	if err != nil {
		return PraiseResult{}, fmt.Errorf("db: count daily praise: %w", err)
	}
	if used >= DailyPraiseLimit {
		return PraiseResult{}, ErrPraiseQuota
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO praises (initiator_id, target_id, status, object_type, object_id,
		                      day, week, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		initiatorID, targetID, PraiseStatusActive, PraiseObjectProfile,
		strconv.FormatInt(targetID, 10), day, week, now)
	if err != nil {
		return PraiseResult{}, mapWriteError(err)
	}
	state, err := loadPraiseState(ctx, tx, initiatorID, now)
	if err != nil {
		return PraiseResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PraiseResult{}, fmt.Errorf("db: commit praise: %w", err)
	}
	return PraiseResult{State: state}, nil
}

// CancelPraise marks all active praises from initiator to target as cancelled.
// Cancel is idempotent and returns quota immediately.
func (d *DB) CancelPraise(ctx context.Context, initiatorID, targetID int64, now time.Time) (PraiseState, error) {
	if now.IsZero() {
		now = SocialNow()
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return PraiseState{}, fmt.Errorf("db: begin cancel praise: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`UPDATE praises SET status = $1
		 WHERE initiator_id = $2 AND target_id = $3 AND status = $4`,
		PraiseStatusCancelled, initiatorID, targetID, PraiseStatusActive); err != nil {
		return PraiseState{}, fmt.Errorf("db: cancel praise: %w", err)
	}
	state, err := loadPraiseState(ctx, tx, initiatorID, now)
	if err != nil {
		return PraiseState{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PraiseState{}, fmt.Errorf("db: commit cancel praise: %w", err)
	}
	return state, nil
}
