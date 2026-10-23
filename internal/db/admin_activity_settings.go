package db

import (
	"context"
	"fmt"
)

// AdminActivitySettings is the narrow write surface the legacy activity console
// uses. UpdateActivity rewrites every editable column, so it must never back
// single-field edits: a caller that omits a field would silently clear it.
// These setters touch exactly one concern and return whether a write happened.

// SetActivityStatus changes only the activity status. A same-value request is
// an idempotent success with changed=false and no write.
func (d *DB) SetActivityStatus(ctx context.Context, ref, status string) (Activity, bool, error) {
	activity, err := d.GetActivity(ctx, ref)
	if err != nil {
		return Activity{}, false, err
	}
	if activity.Status == status {
		return activity, false, nil
	}
	updated, err := scanActivity(d.pool.QueryRow(ctx,
		`UPDATE activities SET status = $2, updated_at = now()
		  WHERE id = $1
		  RETURNING `+activityColumns, activity.ID, status))
	if err != nil {
		return Activity{}, false, fmt.Errorf("db: set activity status: %w", err)
	}
	return updated, true, nil
}

// SetActivityMode writes the two mutually exclusive entry flags in one
// transaction under the activity advisory lock, so a concurrent grouping run
// can never observe half a mode switch. Mode is group|onetoone|none.
func (d *DB) SetActivityMode(ctx context.Context, ref, mode string) (Activity, bool, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Activity{}, false, fmt.Errorf("db: begin activity mode: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	activity, err := d.lockActivityTx(ctx, tx, ref, DefaultGroupLockTimeout)
	if err != nil {
		return Activity{}, false, err
	}
	groupEnabled := mode == "group"
	onetooneEnabled := mode == "onetoone"
	if activity.GroupEnabled == groupEnabled && activity.OnetooneEnabled == onetooneEnabled {
		return activity, false, nil
	}
	updated, err := scanActivity(tx.QueryRow(ctx,
		`UPDATE activities SET group_enabled = $2, onetoone_enabled = $3,
		        updated_at = now()
		  WHERE id = $1
		  RETURNING `+activityColumns, activity.ID, groupEnabled, onetooneEnabled))
	if err != nil {
		return Activity{}, false, fmt.Errorf("db: set activity mode: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Activity{}, false, fmt.Errorf("db: commit activity mode: %w", err)
	}
	return updated, true, nil
}

// SetActivitySigninEnabled toggles the on-site check-in gate under the same
// advisory lock as grouping so the gate cannot flip mid-run.
func (d *DB) SetActivitySigninEnabled(ctx context.Context, ref string, enabled bool) (Activity, bool, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Activity{}, false, fmt.Errorf("db: begin signin flag: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	activity, err := d.lockActivityTx(ctx, tx, ref, DefaultGroupLockTimeout)
	if err != nil {
		return Activity{}, false, err
	}
	if activity.SigninEnabled == enabled {
		return activity, false, nil
	}
	updated, err := scanActivity(tx.QueryRow(ctx,
		`UPDATE activities SET signin_enabled = $2, updated_at = now()
		  WHERE id = $1
		  RETURNING `+activityColumns, activity.ID, enabled))
	if err != nil {
		return Activity{}, false, fmt.Errorf("db: set signin flag: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Activity{}, false, fmt.Errorf("db: commit signin flag: %w", err)
	}
	return updated, true, nil
}

// SetActivitySigninCode replaces the four-digit on-site code. The code is
// validated by the caller; it is intentionally never logged here.
func (d *DB) SetActivitySigninCode(ctx context.Context, ref, code string) (Activity, bool, error) {
	activity, err := d.GetActivity(ctx, ref)
	if err != nil {
		return Activity{}, false, err
	}
	if activity.SigninCode == code {
		return activity, false, nil
	}
	updated, err := scanActivity(d.pool.QueryRow(ctx,
		`UPDATE activities SET signin_code = $2, updated_at = now()
		  WHERE id = $1
		  RETURNING `+activityColumns, activity.ID, code))
	if err != nil {
		return Activity{}, false, fmt.Errorf("db: set signin code: %w", err)
	}
	return updated, true, nil
}

// ClearActivityAttendance resets every active signup's attendance to the empty
// "not checked in" value and reports how many rows changed.
func (d *DB) ClearActivityAttendance(ctx context.Context, activityID int64) (int64, error) {
	tag, err := d.pool.Exec(ctx,
		`UPDATE signups SET attendance = ''
		  WHERE activity_id = $1 AND status = $2
		    AND COALESCE(attendance, '') <> ''`,
		activityID, SignupStatusSigned)
	if err != nil {
		return 0, fmt.Errorf("db: clear activity attendance: %w", err)
	}
	return tag.RowsAffected(), nil
}
