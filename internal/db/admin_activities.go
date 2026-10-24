package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminActivityInput is the editable subset of an activity. CurrentSignup is
// intentionally not editable; it is maintained by signup/cancel transactions.
type AdminActivityInput struct {
	Name            string
	Description     string
	Location        string
	Condition       string
	MaxSignup       int
	Fee             string
	Food            string
	Status          string
	GroupEnabled    bool
	MalePerGroup    int
	FemalePerGroup  int
	OnetooneEnabled bool
	SigninEnabled   bool
	SigninCode      string
	PublishAt       *time.Time
	StartAt         *time.Time
	EndAt           *time.Time
}

// ErrInvalidAttendance reports an attendance value outside the allowed set.
var ErrInvalidAttendance = errors.New("db: invalid attendance state")

// AdminActivityRosterEntry is the admin-only roster row. PhoneMasked is already
// masked inside the DB layer so the raw phone never reaches handlers/templates.
type AdminActivityRosterEntry struct {
	UserID       int64
	LegacyUserID string
	Name         string
	Nickname     string
	Gender       string
	PhoneMasked  string
	SignupAt     time.Time
	Attendance   Attendance
}

// AdminActivitySigninStats summarises one activity's signups and check-ins.
type AdminActivitySigninStats struct {
	SignupCount    int
	CheckedInCount int
}

// AdminActivityBroadcastAudience lists the two independent recipient sets.
// Cross-set duplicates are kept so the handler can show and dedupe both counts.
type AdminActivityBroadcastAudience struct {
	SignupUserIDs   []int64
	ObserverUserIDs []int64
}

// maskPhone keeps the first three and last four digits for staff recognition.
func maskPhone(phone string) string {
	phone = strings.TrimSpace(phone)
	runes := []rune(phone)
	switch {
	case len(runes) >= 8:
		return string(runes[:3]) + "****" + string(runes[len(runes)-4:])
	case len(runes) >= 4:
		return string(runes[:1]) + "***" + string(runes[len(runes)-1:])
	case len(runes) == 0:
		return ""
	default:
		return "***"
	}
}

func normalAttendance(state Attendance) (Attendance, bool) {
	state = Attendance(strings.TrimSpace(string(state)))
	switch state {
	case "", AttendanceOnTime, AttendanceLate, AttendanceAbsent:
		return state, true
	}
	return "", false
}

// AdminActivityRoster returns the active roster with privacy-safe fields.
func (d *DB) AdminActivityRoster(ctx context.Context, activityID int64) ([]AdminActivityRosterEntry, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT s.user_id, COALESCE(u.legacy_user_id, ''), u.name,
		       COALESCE(NULLIF(s.nickname, ''), NULLIF(u.nickname, ''), u.name),
		       COALESCE(u.gender, ''), u.phone, s.created_at, COALESCE(s.attendance, '')
		  FROM signups s
		  JOIN users u ON u.id = s.user_id
		 WHERE s.activity_id = $1 AND s.status = $2
		 ORDER BY s.created_at, s.id`, activityID, SignupStatusSigned)
	if err != nil {
		return nil, fmt.Errorf("db: list activity roster: %w", err)
	}
	defer rows.Close()

	out := make([]AdminActivityRosterEntry, 0)
	for rows.Next() {
		var entry AdminActivityRosterEntry
		var phone, attendance string
		if err := rows.Scan(&entry.UserID, &entry.LegacyUserID, &entry.Name,
			&entry.Nickname, &entry.Gender, &phone, &entry.SignupAt, &attendance); err != nil {
			return nil, fmt.Errorf("db: scan activity roster: %w", err)
		}
		entry.PhoneMasked = maskPhone(phone)
		entry.Attendance = Attendance(strings.TrimSpace(attendance))
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate activity roster: %w", err)
	}
	return out, nil
}

// AdminActivitySigninStats groups active signups and check-ins by activity.
func (d *DB) AdminActivitySigninStats(ctx context.Context) (map[int64]AdminActivitySigninStats, error) {
	rows, err := d.pool.Query(ctx, `
		SELECT activity_id,
		       count(*),
		       count(*) FILTER (WHERE attendance IN ($2, $3))
		  FROM signups
		 WHERE status = $1
		 GROUP BY activity_id`,
		SignupStatusSigned, string(AttendanceOnTime), string(AttendanceLate))
	if err != nil {
		return nil, fmt.Errorf("db: load activity signin stats: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]AdminActivitySigninStats)
	for rows.Next() {
		var activityID int64
		var stats AdminActivitySigninStats
		if err := rows.Scan(&activityID, &stats.SignupCount, &stats.CheckedInCount); err != nil {
			return nil, fmt.Errorf("db: scan activity signin stats: %w", err)
		}
		out[activityID] = stats
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate activity signin stats: %w", err)
	}
	return out, nil
}

// AdminSetActivityAttendance updates one active signup. A cancelled signup is
// history and is never rewritten; an empty state clears the check-in.
func (d *DB) AdminSetActivityAttendance(ctx context.Context, activityID, userID int64, state Attendance) (Attendance, error) {
	state, ok := normalAttendance(state)
	if !ok {
		return "", ErrInvalidAttendance
	}
	var stored string
	err := d.pool.QueryRow(ctx, `
		UPDATE signups SET attendance = $3
		 WHERE activity_id = $1 AND user_id = $2 AND status = $4
		 RETURNING COALESCE(attendance, '')`,
		activityID, userID, string(state), SignupStatusSigned).Scan(&stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotSignedUp
	}
	if err != nil {
		return "", fmt.Errorf("db: set activity attendance: %w", err)
	}
	return Attendance(strings.TrimSpace(stored)), nil
}

// SetActivityPoster links an uploaded media row to the activity. An empty
// mediaID clears the poster.
func (d *DB) SetActivityPoster(ctx context.Context, activityID int64, mediaID string) error {
	tag, err := d.pool.Exec(ctx, `
		UPDATE activities SET poster_media_id = NULLIF($2, ''), updated_at = now()
		 WHERE id = $1`, activityID, strings.TrimSpace(mediaID))
	if err != nil {
		return fmt.Errorf("db: set activity poster: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrActivityNotFound
	}
	return nil
}

// DeleteActivity removes an activity row. The admin create flow only uses it
// to roll back a freshly created activity whose poster could not be linked;
// such a row has no signups, so the signup cascade is never exercised.
func (d *DB) DeleteActivity(ctx context.Context, id int64) error {
	tag, err := d.pool.Exec(ctx, `DELETE FROM activities WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("db: delete activity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrActivityNotFound
	}
	return nil
}

// AdminActivityBroadcastAudience resolves the two recipient sets. Signups are
// limited to active/matched accounts; observers need the observer flag and an
// active/observer account. The sets stay separate for preview counts.
func (d *DB) AdminActivityBroadcastAudience(ctx context.Context, activityID int64, includeSignups, includeObservers bool) (AdminActivityBroadcastAudience, error) {
	out := AdminActivityBroadcastAudience{
		SignupUserIDs:   make([]int64, 0),
		ObserverUserIDs: make([]int64, 0),
	}
	if includeSignups {
		rows, err := d.pool.Query(ctx, `
			SELECT DISTINCT u.id
			  FROM signups s
			  JOIN users u ON u.id = s.user_id
			 WHERE s.activity_id = $1 AND s.status = $2
			   AND u.status = $3
			   AND u.account_status IN ($4, $5)
			 ORDER BY u.id`,
			activityID, SignupStatusSigned, "active", "active", "matched")
		if err != nil {
			return out, fmt.Errorf("db: list activity broadcast signups: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var userID int64
			if err := rows.Scan(&userID); err != nil {
				return out, fmt.Errorf("db: scan activity broadcast signup: %w", err)
			}
			out.SignupUserIDs = append(out.SignupUserIDs, userID)
		}
		if err := rows.Err(); err != nil {
			return out, fmt.Errorf("db: iterate activity broadcast signups: %w", err)
		}
		rows.Close()
	}
	if includeObservers {
		rows, err := d.pool.Query(ctx, `
			SELECT id FROM users
			 WHERE observer_enabled = true
			   AND status = $1
			   AND account_status IN ($2, $3)
			 ORDER BY id`, "active", "active", "observer")
		if err != nil {
			return out, fmt.Errorf("db: list activity broadcast observers: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var userID int64
			if err := rows.Scan(&userID); err != nil {
				return out, fmt.Errorf("db: scan activity broadcast observer: %w", err)
			}
			out.ObserverUserIDs = append(out.ObserverUserIDs, userID)
		}
		if err := rows.Err(); err != nil {
			return out, fmt.Errorf("db: iterate activity broadcast observers: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

func (in AdminActivityInput) clean() AdminActivityInput {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Location = strings.TrimSpace(in.Location)
	in.Condition = strings.TrimSpace(in.Condition)
	in.Fee = strings.TrimSpace(in.Fee)
	in.Food = strings.TrimSpace(in.Food)
	in.Status = strings.TrimSpace(in.Status)
	in.SigninCode = strings.TrimSpace(in.SigninCode)
	return in
}

// IsFourDigitCode reports whether raw is exactly four ASCII digits. The check
// lives in the DB package so form parsing and writes agree.
func IsFourDigitCode(raw string) bool {
	raw = strings.TrimSpace(raw)
	if len(raw) != 4 {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return false
		}
	}
	return true
}

// Validate enforces the invariants the edit form only hints at in HTML:
// grouping and one-to-one are mutually exclusive, an enabled check-in needs a
// 4-digit code, and the end time cannot precede the start time. Callers must
// run clean() first so trimmed values are validated.
func (in AdminActivityInput) Validate() error {
	if in.GroupEnabled && in.OnetooneEnabled {
		return &ValidationError{
			Field:   "onetoone_enabled",
			Message: "「分组」与「一对一」不能同时开启，请只保留一种模式。",
		}
	}
	if in.SigninEnabled {
		if in.SigninCode == "" {
			return &ValidationError{
				Field:   "signin_code",
				Message: "开启现场签到后必须填写 4 位数字签到码。",
			}
		}
		if !IsFourDigitCode(in.SigninCode) {
			return &ValidationError{
				Field:   "signin_code",
				Message: "签到码必须是 4 位数字（例如 0123）。",
			}
		}
	}
	if in.StartAt != nil && in.EndAt != nil && in.EndAt.Before(*in.StartAt) {
		return &ValidationError{
			Field:   "end_at",
			Message: "结束时间不能早于开始时间。",
		}
	}
	return nil
}

func (d *DB) nextActivityNo(ctx context.Context, tx pgx.Tx) (string, error) {
	var next int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(MAX((regexp_replace(activity_no, '\D', '', 'g'))::int), 0) + 1
		FROM activities
		WHERE activity_no ~ '^A-[0-9]+$'`).Scan(&next); err != nil {
		return "", fmt.Errorf("db: next activity no: %w", err)
	}
	return fmt.Sprintf("A-%04d", next), nil
}

func scanAdminActivity(row rowScanner) (Activity, error) { return scanActivity(row) }

// CreateActivity inserts an activity and returns the persisted row.
func (d *DB) CreateActivity(ctx context.Context, in AdminActivityInput) (Activity, error) {
	in = in.clean()
	if err := in.Validate(); err != nil {
		return Activity{}, err
	}
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Activity{}, fmt.Errorf("db: begin activity: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	no, err := d.nextActivityNo(ctx, tx)
	if err != nil {
		return Activity{}, err
	}
	row := tx.QueryRow(ctx, `
		INSERT INTO activities (
			activity_no, name, description, location, condition, max_signup,
			fee, food, status, group_enabled, male_per_group, female_per_group,
			onetoone_enabled, signin_enabled, signin_code, publish_at, start_at, end_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING `+qualifiedActivityColumns("activities"),
		no, in.Name, in.Description, in.Location, in.Condition, in.MaxSignup,
		in.Fee, in.Food, in.Status, in.GroupEnabled, in.MalePerGroup, in.FemalePerGroup,
		in.OnetooneEnabled, in.SigninEnabled, in.SigninCode, in.PublishAt, in.StartAt, in.EndAt)
	activity, err := scanAdminActivity(row)
	if err != nil {
		return Activity{}, fmt.Errorf("db: create activity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Activity{}, fmt.Errorf("db: commit activity: %w", err)
	}
	return activity, nil
}

// UpdateActivity updates the editable fields; current_signup is preserved.
func (d *DB) UpdateActivity(ctx context.Context, id int64, in AdminActivityInput) (Activity, error) {
	in = in.clean()
	if err := in.Validate(); err != nil {
		return Activity{}, err
	}
	row := d.pool.QueryRow(ctx, `
		UPDATE activities SET
			name=$2, description=$3, location=$4, condition=$5, max_signup=$6,
			fee=$7, food=$8, status=$9, group_enabled=$10, male_per_group=$11,
			female_per_group=$12, onetoone_enabled=$13, signin_enabled=$14,
			signin_code=$15, publish_at=$16, start_at=$17, end_at=$18,
			updated_at=now()
		WHERE id=$1
		RETURNING `+qualifiedActivityColumns("activities"),
		id, in.Name, in.Description, in.Location, in.Condition, in.MaxSignup,
		in.Fee, in.Food, in.Status, in.GroupEnabled, in.MalePerGroup, in.FemalePerGroup,
		in.OnetooneEnabled, in.SigninEnabled, in.SigninCode, in.PublishAt, in.StartAt, in.EndAt)
	activity, err := scanAdminActivity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("db: update activity: %w", err)
	}
	return activity, nil
}
