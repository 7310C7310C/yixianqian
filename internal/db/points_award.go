package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ActivityAssistTotal is the assist total for one user in one activity, with
// reversed entries excluded so a refund frees the cap again.
func (d *DB) ActivityAssistTotal(ctx context.Context, userID int64, activityID string) (int, error) {
	var n int
	err := d.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(l.delta), 0) FROM points_ledger l
		 WHERE l.user_id = $1 AND l.kind = $2 AND l.ref_type = 'activity' AND l.ref_id = $3
		   AND NOT EXISTS (
		     SELECT 1 FROM points_ledger r
		     WHERE r.ref_type = 'ledger' AND r.ref_id = CAST(l.id AS text)
		   )`, userID, PointsKindAssist, strings.TrimSpace(activityID)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("db: activity assist total: %w", err)
	}
	return n, nil
}

// GrantAssist ports lib/points_award.grant_assist. Both limits matter: a
// single grant must be within assist_min..assist_max, and the per-activity
// total must stay under assist_activity_cap.
func (d *DB) GrantAssist(ctx context.Context, userID int64, activityID string, amount int, reason string, operatorID *int64, idempotencyKey string) (PointsEntry, int, error) {
	activityID = strings.TrimSpace(activityID)
	reason = strings.TrimSpace(reason)
	if userID <= 0 {
		return PointsEntry{}, 0, pointsError(ErrPointsNotFound, "用户不存在。")
	}
	if activityID == "" {
		return PointsEntry{}, 0, pointsError(ErrPointsUnavailable, "协助组织必须关联到某一场活动。")
	}
	if reason == "" {
		return PointsEntry{}, 0, pointsError(ErrPointsUnavailable, "必须填写加穗原因。")
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return PointsEntry{}, 0, fmt.Errorf("db: begin assist grant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	cfg, _, err := loadPointsConfig(ctx, tx)
	if err != nil {
		return PointsEntry{}, 0, err
	}
	if amount < cfg.AssistMin || amount > cfg.AssistMax {
		return PointsEntry{}, 0, pointsError(ErrPointsUnavailable,
			fmt.Sprintf("协助组织单次应为 %d~%d 穗，收到 %d。",
				cfg.AssistMin, cfg.AssistMax, amount))
	}
	if err := lockPointsUser(ctx, tx, userID); err != nil {
		return PointsEntry{}, 0, err
	}
	key := strings.TrimSpace(idempotencyKey)
	if key != "" {
		var existing int64
		err := tx.QueryRow(ctx,
			`SELECT id FROM points_ledger WHERE idempotency_key = $1`, key).Scan(&existing)
		if err == nil {
			return PointsEntry{}, 0, pointsError(ErrPointsDuplicate, "这笔麦穗已经记过账了。")
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return PointsEntry{}, 0, fmt.Errorf("db: check assist idempotency: %w", err)
		}
	}
	used, err := activityAssistTotalTx(ctx, tx, userID, activityID)
	if err != nil {
		return PointsEntry{}, 0, err
	}
	if used+amount > cfg.AssistActivityCap {
		return PointsEntry{}, 0, pointsError(ErrPointsUnavailable,
			fmt.Sprintf("这场活动已经给过 %d 穗，再加 %d 穗会超过上限 %d。",
				used, amount, cfg.AssistActivityCap))
	}
	entry, balance, err := addPointsEntryTx(ctx, tx, AddPointsParams{
		UserID:         userID,
		Delta:          amount,
		Kind:           PointsKindAssist,
		Reason:         strings.TrimSpace("协助组织活动 " + activityID + "：" + reason),
		RefType:        "activity",
		RefID:          activityID,
		OperatorID:     operatorID,
		IdempotencyKey: key,
	})
	if err != nil {
		return PointsEntry{}, 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PointsEntry{}, 0, fmt.Errorf("db: commit assist grant: %w", err)
	}
	return entry, balance, nil
}

func activityAssistTotalTx(ctx context.Context, tx pgx.Tx, userID int64, activityID string) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(l.delta), 0) FROM points_ledger l
		 WHERE l.user_id = $1 AND l.kind = $2 AND l.ref_type = 'activity' AND l.ref_id = $3
		   AND NOT EXISTS (
		     SELECT 1 FROM points_ledger r
		     WHERE r.ref_type = 'ledger' AND r.ref_id = CAST(l.id AS text)
		   )`, userID, PointsKindAssist, activityID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("db: activity assist total: %w", err)
	}
	return n, nil
}

// AdjustPoints is the administrator add/subtract path. Debits are new negative
// rows, never edits to the original entry.
func (d *DB) AdjustPoints(ctx context.Context, userID int64, amount int, reason string, operatorID *int64, idempotencyKey string) (PointsEntry, int, error) {
	if strings.TrimSpace(reason) == "" {
		return PointsEntry{}, 0, pointsError(ErrPointsUnavailable, "必须填写原因。")
	}
	if amount == 0 {
		return PointsEntry{}, 0, pointsError(ErrPointsInvalidDelta, "数量不能为 0。")
	}
	return d.AddPointsEntry(ctx, AddPointsParams{
		UserID:         userID,
		Delta:          amount,
		Kind:           PointsKindAdmin,
		Reason:         strings.TrimSpace(reason),
		RefType:        "manual",
		OperatorID:     operatorID,
		IdempotencyKey: idempotencyKey,
	})
}
