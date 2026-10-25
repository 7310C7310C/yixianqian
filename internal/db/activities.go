package db

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Activity status values match the migrated Bitable select options.
const (
	ActivityStatusOpen       = "报名中"
	ActivityStatusNotStarted = "未开始报名"
	ActivityStatusFull       = "已满员"
	ActivityStatusFinished   = "已结束"
	ActivityStatusCancelled  = "已取消"

	SignupStatusSigned    = "已报名"
	SignupStatusCancelled = "已取消"
)

// Attendance values are the legacy Bitable options. "未到" is written by the
// admin roster, never by self sign-in.
type Attendance string

const (
	AttendanceOnTime Attendance = "按时"
	AttendanceLate   Attendance = "迟到"
	AttendanceAbsent Attendance = "未到"
)

// CheckedIn mirrors the legacy D13 rule: on-time and late both count as
// present; absent, empty and unknown values do not.
func (a Attendance) CheckedIn() bool {
	return a == AttendanceOnTime || a == AttendanceLate
}

// IsCheckedIn is the string form used by grouping gates.
func IsCheckedIn(state string) bool {
	return Attendance(strings.TrimSpace(state)).CheckedIn()
}

// Activity not-found and state errors. Handlers map these to stable API codes.
var (
	ErrActivityNotFound  = errors.New("db: activity not found")
	ErrActivityNotOpen   = errors.New("db: activity does not allow this action")
	ErrActivityFull      = errors.New("db: activity is full")
	ErrAlreadySignedUp   = errors.New("db: user already signed up")
	ErrAccountNotActive  = errors.New("db: account is not active")
	ErrNotSignedUp       = errors.New("db: user is not signed up")
	ErrAlreadyCancelled  = errors.New("db: signup was already cancelled")
	ErrSigninDisabled    = errors.New("db: activity signin is disabled")
	ErrSigninCodeNotSet  = errors.New("db: activity signin code is not set")
	ErrSigninCodeInvalid = errors.New("db: signin code is incorrect")
	ErrMarkedAbsent      = errors.New("db: signup is already marked absent")
	// 活动时间窗校验：状态仍是「报名中」但已过开始时间的活动不能再报名/取消，
	// 自助签到只能在活动开始后、结束前进行。
	ErrActivityStarted    = errors.New("db: activity has already started")
	ErrActivityEnded      = errors.New("db: activity has already ended")
	ErrActivityNotStarted = errors.New("db: activity has not started yet")
)

// ActivityStatusError reports the activity state that blocked the action.
type ActivityStatusError struct {
	Status string
}

func (e *ActivityStatusError) Error() string {
	return "db: activity status " + e.Status + " does not allow this action"
}

func (e *ActivityStatusError) Unwrap() error { return ErrActivityNotOpen }

// Activity is the user-visible activity row. The sign-in code is deliberately
// not part of this struct so list/detail serialization cannot leak it.
type Activity struct {
	ID              int64
	LegacyRecordID  *string
	ActivityNo      string
	Name            string
	Description     string
	Location        string
	Condition       string
	MaxSignup       int
	CurrentSignup   int
	Fee             string
	Food            string
	Status          string
	PosterMediaID   *string
	GroupStatus     string
	MalePerGroup    int
	FemalePerGroup  int
	GroupEnabled    bool
	OnetooneStatus  string
	OnetooneEnabled bool
	SigninEnabled   bool
	SigninCode      string
	PublishAt       *time.Time
	StartAt         *time.Time
	EndAt           *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Signup is one registration row. Cancelled rows are kept as history.
type Signup struct {
	ID         int64
	ActivityID int64
	UserID     int64
	Nickname   string
	Status     string
	Attendance Attendance
	CreatedAt  time.Time
}

// MyActivity is an activity joined with the owner's active signup.
type MyActivity struct {
	Activity
	SignupStatus    string
	Attendance      Attendance
	SignupCreatedAt time.Time
}

// ActivitySignup is the privacy-safe roster row: no phone, open id or real
// name, only the public legacy id and public profile fields.
type ActivitySignup struct {
	UserID       int64
	LegacyUserID string
	Nickname     string
	Gender       string
	CreatedAt    time.Time
}

const activityColumns = `id, legacy_record_id, activity_no, name, description, location,
	condition, max_signup, current_signup, fee, food, status, poster_media_id,
	group_status, male_per_group, female_per_group, group_enabled,
	onetoone_status, onetoone_enabled, signin_enabled, signin_code, publish_at, start_at,
	end_at, created_at, updated_at`

func qualifiedActivityColumns(alias string) string {
	prefix := alias + "."
	return prefix + `id, ` + prefix + `legacy_record_id, ` + prefix + `activity_no, ` +
		prefix + `name, ` + prefix + `description, ` + prefix + `location, ` +
		prefix + `condition, ` + prefix + `max_signup, ` + prefix + `current_signup, ` +
		prefix + `fee, ` + prefix + `food, ` + prefix + `status, ` + prefix + `poster_media_id, ` +
		prefix + `group_status, ` + prefix + `male_per_group, ` + prefix + `female_per_group, ` +
		prefix + `group_enabled, ` + prefix + `onetoone_status, ` + prefix + `onetoone_enabled, ` +
		prefix + `signin_enabled, ` + prefix + `signin_code, ` + prefix + `publish_at, ` + prefix + `start_at, ` +
		prefix + `end_at, ` + prefix + `created_at, ` + prefix + `updated_at`
}

const signupColumns = `id, activity_id, user_id, nickname, status, attendance, created_at`

func scanActivity(row rowScanner) (Activity, error) {
	var a Activity
	err := row.Scan(&a.ID, &a.LegacyRecordID, &a.ActivityNo, &a.Name, &a.Description,
		&a.Location, &a.Condition, &a.MaxSignup, &a.CurrentSignup, &a.Fee, &a.Food,
		&a.Status, &a.PosterMediaID, &a.GroupStatus, &a.MalePerGroup, &a.FemalePerGroup,
		&a.GroupEnabled, &a.OnetooneStatus, &a.OnetooneEnabled, &a.SigninEnabled, &a.SigninCode,
		&a.PublishAt, &a.StartAt, &a.EndAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Activity{}, ErrActivityNotFound
	}
	if err != nil {
		return Activity{}, fmt.Errorf("db: scan activity: %w", err)
	}
	return a, nil
}

func scanSignup(row rowScanner) (Signup, error) {
	var s Signup
	var attendance string
	err := row.Scan(&s.ID, &s.ActivityID, &s.UserID, &s.Nickname, &s.Status,
		&attendance, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Signup{}, ErrNotSignedUp
	}
	if err != nil {
		return Signup{}, fmt.Errorf("db: scan signup: %w", err)
	}
	s.Attendance = Attendance(strings.TrimSpace(attendance))
	return s, nil
}

// activityRefFilter matches the text activity number, an imported record id or
// a numeric primary key. The handler only exposes the activity number, but the
// legacy frontend used record ids for deep links.
const activityRefFilter = `(activity_no = $1 OR legacy_record_id = $1 OR id = $2)`

const qualifiedActivityRefFilter = `(a.activity_no = $1 OR a.legacy_record_id = $1 OR a.id = $2)`

func activityRefQueryArgs(ref string) (string, int64) {
	ref = strings.TrimSpace(ref)
	var numeric int64
	for _, r := range ref {
		if r < '0' || r > '9' {
			return ref, 0
		}
	}
	if ref != "" {
		if parsed, err := strconv.ParseInt(ref, 10, 64); err == nil {
			numeric = parsed
		}
	}
	return ref, numeric
}

// ListActivities returns every non-cancelled activity in signup order: open
// events first, then full/upcoming/past ones.
// ListActivitiesForUser returns the public activity list with the viewer's
// active signup attached in one statement. It replaces ListActivities +
// ListMyActivities on the home aggregate; ordering and cancellation filters
// stay identical to those two queries.
func (d *DB) ListActivitiesForUser(ctx context.Context, userID int64) ([]MyActivity, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT `+qualifiedActivityColumns("a")+`,
		        s.status, COALESCE(s.attendance, '')
		   FROM activities a
		   LEFT JOIN LATERAL (
		     SELECT status, attendance FROM signups
		      WHERE activity_id = a.id AND user_id = $1 AND status <> $2
		      ORDER BY id DESC LIMIT 1
		   ) s ON true
		  WHERE a.status <> $3
		  ORDER BY CASE a.status
		            WHEN $4 THEN 0
		            WHEN $5 THEN 1
		            WHEN $6 THEN 2
		            WHEN $7 THEN 3
		            ELSE 4 END,
		          a.start_at NULLS LAST, a.id`,
		userID, SignupStatusCancelled, ActivityStatusCancelled,
		ActivityStatusOpen, ActivityStatusNotStarted,
		ActivityStatusFull, ActivityStatusFinished)
	if err != nil {
		return nil, fmt.Errorf("db: list activities for user: %w", err)
	}
	defer rows.Close()
	out := make([]MyActivity, 0)
	for rows.Next() {
		var (
			item       MyActivity
			status     *string
			attendance string
		)
		a := &item.Activity
		if err := rows.Scan(&a.ID, &a.LegacyRecordID, &a.ActivityNo, &a.Name, &a.Description,
			&a.Location, &a.Condition, &a.MaxSignup, &a.CurrentSignup, &a.Fee, &a.Food,
			&a.Status, &a.PosterMediaID, &a.GroupStatus, &a.MalePerGroup, &a.FemalePerGroup,
			&a.GroupEnabled, &a.OnetooneStatus, &a.OnetooneEnabled, &a.SigninEnabled, &a.SigninCode,
			&a.PublishAt, &a.StartAt, &a.EndAt, &a.CreatedAt, &a.UpdatedAt,
			&status, &attendance); err != nil {
			return nil, fmt.Errorf("db: scan activity for user: %w", err)
		}
		if status != nil {
			item.SignupStatus = *status
			item.Attendance = Attendance(strings.TrimSpace(attendance))
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate activities for user: %w", err)
	}
	return out, nil
}

func (d *DB) ListActivities(ctx context.Context) ([]Activity, error) {
	rows, err := d.pool.Query(ctx,
		"SELECT "+activityColumns+` FROM activities
		 WHERE status <> $1
		 ORDER BY CASE status
		            WHEN $2 THEN 0
		            WHEN $3 THEN 1
		            WHEN $4 THEN 2
		            WHEN $5 THEN 3
		            ELSE 4 END,
		          start_at NULLS LAST, id`,
		ActivityStatusCancelled, ActivityStatusOpen, ActivityStatusNotStarted,
		ActivityStatusFull, ActivityStatusFinished)
	if err != nil {
		return nil, fmt.Errorf("db: list activities: %w", err)
	}
	defer rows.Close()

	var out []Activity
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate activities: %w", err)
	}
	return out, nil
}

// GetActivity resolves an activity by number, legacy record id or numeric id.
func (d *DB) GetActivity(ctx context.Context, ref string) (Activity, error) {
	text, numeric := activityRefQueryArgs(ref)
	return scanActivity(d.pool.QueryRow(ctx,
		"SELECT "+activityColumns+" FROM activities WHERE "+activityRefFilter+
			" ORDER BY (activity_no = $1) DESC, id LIMIT 1", text, numeric))
}

// GetActivitySigninCode loads the secret code only for the self sign-in path.
func (d *DB) GetActivitySigninCode(ctx context.Context, activityID int64) (string, error) {
	var code string
	err := d.pool.QueryRow(ctx,
		`SELECT signin_code FROM activities WHERE id = $1`, activityID).Scan(&code)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrActivityNotFound
	}
	if err != nil {
		return "", fmt.Errorf("db: load signin code: %w", err)
	}
	return strings.TrimSpace(code), nil
}

// ListMyActivities returns the caller's active signups, cancelled rows filtered
// and one row per activity (the schema already enforces one signup per user).
func (d *DB) ListMyActivities(ctx context.Context, userID int64) ([]MyActivity, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT `+qualifiedActivityColumns("a")+`,
		        s.status, COALESCE(s.attendance, ''), s.created_at
		   FROM signups s
		   JOIN activities a ON a.id = s.activity_id
		  WHERE s.user_id = $1
		    AND s.status <> $2
		    AND a.status <> $3
		  ORDER BY CASE a.status
		            WHEN $4 THEN 0
		            WHEN $5 THEN 1
		            WHEN $6 THEN 2
		            WHEN $7 THEN 3
		            ELSE 4 END,
		          a.start_at NULLS LAST, a.id`,
		userID, SignupStatusCancelled, ActivityStatusCancelled,
		ActivityStatusOpen, ActivityStatusNotStarted,
		ActivityStatusFull, ActivityStatusFinished)
	if err != nil {
		return nil, fmt.Errorf("db: list my activities: %w", err)
	}
	defer rows.Close()

	var out []MyActivity
	for rows.Next() {
		var item MyActivity
		var attendance string
		if err := rows.Scan(&item.ID, &item.LegacyRecordID, &item.ActivityNo, &item.Name,
			&item.Description, &item.Location, &item.Condition, &item.MaxSignup,
			&item.CurrentSignup, &item.Fee, &item.Food, &item.Status,
			&item.PosterMediaID, &item.GroupStatus, &item.MalePerGroup,
			&item.FemalePerGroup, &item.GroupEnabled, &item.OnetooneStatus,
			&item.OnetooneEnabled, &item.SigninEnabled, &item.SigninCode, &item.PublishAt,
			&item.StartAt, &item.EndAt, &item.CreatedAt, &item.UpdatedAt,
			&item.SignupStatus, &attendance, &item.SignupCreatedAt); err != nil {
			return nil, fmt.Errorf("db: scan my activity: %w", err)
		}
		item.Attendance = Attendance(strings.TrimSpace(attendance))
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate my activities: %w", err)
	}
	return out, nil
}

func (d *DB) userDisplayName(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID int64) (string, error) {
	var name string
	err := q.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(nickname, ''), name) FROM users WHERE id = $1`, userID).
		Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("db: load signup nickname: %w", err)
	}
	return name, nil
}

// SignupActivity registers an active user in an open activity. The activity
// row is locked so concurrent signups cannot overshoot max_signup.
func (d *DB) SignupActivity(ctx context.Context, userID int64, ref string) (Signup, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return Signup{}, fmt.Errorf("db: begin signup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	text, numeric := activityRefQueryArgs(ref)
	var activityID int64
	var status string
	var maxSignup, currentSignup int
	var startAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT id, status, max_signup, current_signup, start_at
		   FROM activities WHERE `+activityRefFilter+` FOR UPDATE`, text, numeric).
		Scan(&activityID, &status, &maxSignup, &currentSignup, &startAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Signup{}, ErrActivityNotFound
	}
	if err != nil {
		return Signup{}, fmt.Errorf("db: lock activity for signup: %w", err)
	}
	var accountKind, accountStatus string
	if err := tx.QueryRow(ctx,
		`SELECT account_kind, account_status FROM users WHERE id = $1`, userID).
		Scan(&accountKind, &accountStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Signup{}, ErrNotFound
		}
		return Signup{}, fmt.Errorf("db: load signup user: %w", err)
	}
	if accountKind == "observer" || accountStatus != "active" {
		return Signup{}, ErrAccountNotActive
	}
	var existingID int64
	var existingStatus string
	err = tx.QueryRow(ctx,
		`SELECT id, status FROM signups WHERE activity_id = $1 AND user_id = $2 FOR UPDATE`,
		activityID, userID).Scan(&existingID, &existingStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Signup{}, fmt.Errorf("db: load existing signup: %w", err)
	}
	hasExisting := err == nil
	// Duplicate wins over the state/full checks: the user already holds a
	// place, so telling them "full" would be misleading.
	if hasExisting && existingStatus != SignupStatusCancelled {
		return Signup{}, ErrAlreadySignedUp
	}
	if status == ActivityStatusFull {
		return Signup{}, ErrActivityFull
	}
	if status != ActivityStatusOpen {
		return Signup{}, &ActivityStatusError{Status: status}
	}
	// 状态仍是「报名中」但开始时间已过：活动实际已开始，报名入口应收敛，
	// 否则过期活动会持续接受报名（审计 UD-03）。
	if startAt != nil && !startAt.After(time.Now()) {
		return Signup{}, ErrActivityStarted
	}
	if maxSignup > 0 && currentSignup >= maxSignup {
		return Signup{}, ErrActivityFull
	}

	nickname, err := d.userDisplayName(ctx, tx, userID)
	if err != nil {
		return Signup{}, err
	}

	if !hasExisting {
		s, err := scanSignup(tx.QueryRow(ctx,
			`INSERT INTO signups (activity_id, user_id, nickname, status)
			 VALUES ($1, $2, $3, $4)
			 RETURNING `+signupColumns,
			activityID, userID, nickname, SignupStatusSigned))
		if err != nil {
			return Signup{}, mapSignupWriteError(err)
		}
		if err := d.bumpActivityAfterSignup(ctx, tx, activityID); err != nil {
			return Signup{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Signup{}, fmt.Errorf("db: commit signup: %w", err)
		}
		return s, nil
	}
	// Re-signup reuses the single row allowed by signups_unique_user.
	s, err := scanSignup(tx.QueryRow(ctx,
		`UPDATE signups
		    SET status = $2, nickname = $3, attendance = '', created_at = now()
		  WHERE id = $1
		  RETURNING `+signupColumns,
		existingID, SignupStatusSigned, nickname))
	if err != nil {
		return Signup{}, mapSignupWriteError(err)
	}
	if err := d.bumpActivityAfterSignup(ctx, tx, activityID); err != nil {
		return Signup{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Signup{}, fmt.Errorf("db: commit signup: %w", err)
	}
	return s, nil
}

func (d *DB) bumpActivityAfterSignup(ctx context.Context, tx pgx.Tx, activityID int64) error {
	_, err := tx.Exec(ctx,
		`UPDATE activities
		    SET current_signup = current_signup + 1,
		        status = CASE
		                   WHEN max_signup > 0 AND current_signup + 1 >= max_signup
		                     THEN $2
		                   ELSE status
		                 END,
		        updated_at = now()
		  WHERE id = $1`, activityID, ActivityStatusFull)
	if err != nil {
		return fmt.Errorf("db: update activity signup count: %w", err)
	}
	return nil
}

// SignupCancelResult reports what the cancellation changed. RefundPoints is the
// number of wheat ears returned for an active priority order.
type SignupCancelResult struct {
	Signup       Signup
	RefundPoints int
	RefundErr    error
}

// CancelSignup keeps the signup row as history, decrements the activity count
// and reopens a full activity. Cancelling is allowed while the activity is
// open or full, not after it ended.
func (d *DB) CancelSignup(ctx context.Context, userID int64, ref string) (SignupCancelResult, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return SignupCancelResult{}, fmt.Errorf("db: begin cancel: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	text, numeric := activityRefQueryArgs(ref)
	var activityID int64
	var activityNo, status string
	var startAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT id, activity_no, status, start_at
		   FROM activities WHERE `+activityRefFilter+` FOR UPDATE`, text, numeric).
		Scan(&activityID, &activityNo, &status, &startAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SignupCancelResult{}, ErrActivityNotFound
	}
	if err != nil {
		return SignupCancelResult{}, fmt.Errorf("db: lock activity for cancel: %w", err)
	}

	var signupID int64
	var signupStatus string
	err = tx.QueryRow(ctx,
		`SELECT id, status FROM signups
		  WHERE activity_id = $1 AND user_id = $2 FOR UPDATE`, activityID, userID).
		Scan(&signupID, &signupStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return SignupCancelResult{}, ErrNotSignedUp
	}
	if err != nil {
		return SignupCancelResult{}, fmt.Errorf("db: load signup for cancel: %w", err)
	}
	if signupStatus == SignupStatusCancelled {
		return SignupCancelResult{}, ErrAlreadyCancelled
	}
	if signupStatus != SignupStatusSigned {
		return SignupCancelResult{}, ErrNotSignedUp
	}
	if status != ActivityStatusOpen && status != ActivityStatusFull {
		return SignupCancelResult{}, &ActivityStatusError{Status: status}
	}
	// 已开始的活动不允许再取消：名额与退穗都应在活动开始前结算。
	if startAt != nil && !startAt.After(time.Now()) {
		return SignupCancelResult{}, ErrActivityStarted
	}

	s, err := scanSignup(tx.QueryRow(ctx,
		`UPDATE signups SET status = $2 WHERE id = $1 RETURNING `+signupColumns,
		signupID, SignupStatusCancelled))
	if err != nil {
		return SignupCancelResult{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE activities
		    SET current_signup = GREATEST(current_signup - 1, 0),
		        status = CASE WHEN status = $2 THEN $3 ELSE status END,
		        updated_at = now()
		  WHERE id = $1`, activityID, ActivityStatusFull, ActivityStatusOpen); err != nil {
		return SignupCancelResult{}, fmt.Errorf("db: update activity after cancel: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SignupCancelResult{}, fmt.Errorf("db: commit cancel: %w", err)
	}

	// The signup is already cancelled: a points failure must not make the user
	// think the cancellation failed. The priority order is looked up by
	// activity and stays refundable on a later retry.
	result := SignupCancelResult{Signup: s}
	var hoursToStart *float64
	if startAt != nil {
		hours := startAt.Sub(time.Now()).Hours()
		hoursToStart = &hours
	}
	order, refundErr := d.CancelPriorityOnSignupCancel(ctx, userID, activityNo, hoursToStart, "")
	if refundErr != nil {
		result.RefundErr = refundErr
		return result, nil
	}
	if order != nil && order.Refunded {
		result.RefundPoints = order.Points
	}
	return result, nil
}

// ListActivitySignups returns the non-cancelled roster (old rule), oldest
// first. Attendance is intentionally omitted from this user-facing row.
func (d *DB) ListActivitySignups(ctx context.Context, activityID int64) ([]ActivitySignup, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT s.user_id, COALESCE(u.legacy_user_id, ''), 
		        COALESCE(NULLIF(s.nickname, ''), NULLIF(u.nickname, ''), u.name),
		        COALESCE(u.gender, ''), s.created_at
		   FROM signups s
		   JOIN users u ON u.id = s.user_id
		  WHERE s.activity_id = $1 AND s.status = $2
		  ORDER BY s.created_at, s.id`, activityID, SignupStatusSigned)
	if err != nil {
		return nil, fmt.Errorf("db: list activity signups: %w", err)
	}
	defer rows.Close()

	var out []ActivitySignup
	for rows.Next() {
		var s ActivitySignup
		if err := rows.Scan(&s.UserID, &s.LegacyUserID, &s.Nickname, &s.Gender,
			&s.CreatedAt); err != nil {
			return nil, fmt.Errorf("db: scan activity signup: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate activity signups: %w", err)
	}
	return out, nil
}

// CountActivitySignups returns the number of active registrations. It powers
// the roster feature-flag "count only" mode.
func (d *DB) CountActivitySignups(ctx context.Context, activityID int64) (int, error) {
	var count int
	err := d.pool.QueryRow(ctx,
		`SELECT count(*) FROM signups WHERE activity_id = $1 AND status = $2`,
		activityID, SignupStatusSigned).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("db: count activity signups: %w", err)
	}
	return count, nil
}

// GetAttendance returns one user's sign-in state for the grouping gate.
func (d *DB) GetAttendance(ctx context.Context, activityID, userID int64) (Attendance, error) {
	var state string
	err := d.pool.QueryRow(ctx,
		`SELECT attendance FROM signups
		  WHERE activity_id = $1 AND user_id = $2 AND status = $3`,
		activityID, userID, SignupStatusSigned).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotSignedUp
	}
	if err != nil {
		return "", fmt.Errorf("db: load attendance: %w", err)
	}
	return Attendance(strings.TrimSpace(state)), nil
}

// AttendanceByActivity returns {user_id: attendance} for every active signup
// in one activity. Missing rows mean "not checked in".
func (d *DB) AttendanceByActivity(ctx context.Context, activityID int64) (map[int64]Attendance, error) {
	rows, err := d.pool.Query(ctx,
		`SELECT user_id, COALESCE(attendance, '')
		   FROM signups WHERE activity_id = $1 AND status = $2`,
		activityID, SignupStatusSigned)
	if err != nil {
		return nil, fmt.Errorf("db: load attendance map: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]Attendance)
	for rows.Next() {
		var userID int64
		var state string
		if err := rows.Scan(&userID, &state); err != nil {
			return nil, fmt.Errorf("db: scan attendance: %w", err)
		}
		out[userID] = Attendance(strings.TrimSpace(state))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate attendance: %w", err)
	}
	return out, nil
}

// SelfSignin validates the published code and marks the caller on time. It is
// idempotent: an already checked-in signup returns Already=true, and a history
// value such as "未到" is never overwritten.
func (d *DB) SelfSignin(ctx context.Context, userID int64, ref, submitted string) (attendance Attendance, already bool, err error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("db: begin self signin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	text, numeric := activityRefQueryArgs(ref)
	var activityID int64
	var signinEnabled bool
	var expectedCode, status string
	var startAt, endAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT id, signin_enabled, signin_code, status, start_at, end_at
		   FROM activities WHERE `+activityRefFilter, text, numeric).
		Scan(&activityID, &signinEnabled, &expectedCode, &status, &startAt, &endAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrActivityNotFound
	}
	if err != nil {
		return "", false, fmt.Errorf("db: load activity for signin: %w", err)
	}

	var signupID int64
	var signupStatus, current string
	err = tx.QueryRow(ctx,
		`SELECT id, status, COALESCE(attendance, '')
		   FROM signups WHERE activity_id = $1 AND user_id = $2 FOR UPDATE`,
		activityID, userID).Scan(&signupID, &signupStatus, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrNotSignedUp
	}
	if err != nil {
		return "", false, fmt.Errorf("db: load signup for signin: %w", err)
	}
	if signupStatus != SignupStatusSigned {
		return "", false, ErrNotSignedUp
	}

	currentAttendance := Attendance(strings.TrimSpace(current))
	if currentAttendance.CheckedIn() {
		return currentAttendance, true, nil
	}
	if currentAttendance != "" {
		return "", false, ErrMarkedAbsent
	}
	// 自助签到只在活动进行期间有效：开始前没有现场码可核，结束后不再补签。
	now := time.Now()
	if status == ActivityStatusFinished || status == ActivityStatusCancelled {
		return "", false, ErrActivityEnded
	}
	if startAt != nil && startAt.After(now) {
		return "", false, ErrActivityNotStarted
	}
	if endAt != nil && !endAt.After(now) {
		return "", false, ErrActivityEnded
	}
	if !signinEnabled {
		return "", false, ErrSigninDisabled
	}
	expectedCode = strings.TrimSpace(expectedCode)
	if expectedCode == "" {
		return "", false, ErrSigninCodeNotSet
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(submitted)), []byte(expectedCode)) != 1 {
		return "", false, ErrSigninCodeInvalid
	}

	updated, err := scanSignup(tx.QueryRow(ctx,
		`UPDATE signups SET attendance = $2
		  WHERE id = $1 AND (attendance IS NULL OR attendance = '')
		  RETURNING `+signupColumns, signupID, string(AttendanceOnTime)))
	if errors.Is(err, ErrNotSignedUp) {
		// The row was filled by a concurrent request; treat that write as the
		// winner and report the stored state.
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return "", false, fmt.Errorf("db: rollback signin race: %w", rollbackErr)
		}
		state, err := d.GetAttendance(ctx, activityID, userID)
		if err != nil {
			return "", false, err
		}
		return state, state.CheckedIn(), nil
	}
	if err != nil {
		return "", false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("db: commit self signin: %w", err)
	}
	return updated.Attendance, false, nil
}

func mapSignupWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if pgErr.ConstraintName == "signups_unique_user" {
			return ErrAlreadySignedUp
		}
	}
	return err
}

// GetSignup returns one user's signup row, including cancelled history. The
// caller decides how to present it.
func (d *DB) GetSignup(ctx context.Context, activityID, userID int64) (Signup, error) {
	return scanSignup(d.pool.QueryRow(ctx,
		"SELECT "+signupColumns+" FROM signups WHERE activity_id = $1 AND user_id = $2",
		activityID, userID))
}

// ActivityPoster holds the stored object details needed to stream a poster.
// OriginalName is the sanitized upload name shown in the admin form.
type ActivityPoster struct {
	MediaID      string
	ObjectKey    string
	ThumbKey     string
	ContentType  string
	ByteSize     int64
	ThumbSize    int64
	Width        int
	Height       int
	OriginalName string
}

// GetActivityPoster resolves the poster media row for an activity. Posters are
// shared assets, so access control is the activity viewer gate, not ownership.
func (d *DB) GetActivityPoster(ctx context.Context, ref string) (ActivityPoster, error) {
	text, numeric := activityRefQueryArgs(ref)
	var p ActivityPoster
	err := d.pool.QueryRow(ctx,
		`SELECT m.id, m.object_key, m.thumb_key, m.content_type, m.byte_size, m.thumb_size,
		        m.width, m.height, COALESCE(m.original_name, '')
		   FROM activities a
		   JOIN media m ON m.id = a.poster_media_id
		  WHERE `+qualifiedActivityRefFilter+`
		  LIMIT 1`, text, numeric).
		Scan(&p.MediaID, &p.ObjectKey, &p.ThumbKey, &p.ContentType, &p.ByteSize, &p.ThumbSize,
			&p.Width, &p.Height, &p.OriginalName)
	if errors.Is(err, pgx.ErrNoRows) {
		return ActivityPoster{}, ErrNotFound
	}
	if err != nil {
		return ActivityPoster{}, fmt.Errorf("db: load activity poster: %w", err)
	}
	return p, nil
}
