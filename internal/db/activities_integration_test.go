package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

func openActivitiesTestDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	d, err := Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(d.Close)
	if err := d.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := d.Migrate(ctx, logger); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d, ctx
}

func createActivityFixtureUser(t *testing.T, d *DB, ctx context.Context, name string) int64 {
	t.Helper()
	legacy := "U-ACT-" + randomDigits(t, 10)
	phone := "1" + randomDigits(t, 10)
	user, err := d.UpsertLegacyUser(ctx, legacy, phone, name)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE users SET account_status = 'active', account_kind = 'user', nickname = $2
		  WHERE id = $1`, user.ID, name); err != nil {
		t.Fatalf("activate user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = d.pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, user.ID)
	})
	return user.ID
}

type activityFixtureOptions struct {
	Status        string
	MaxSignup     int
	SigninEnabled bool
	SigninCode    string
	StartAt       *time.Time
}

func createActivityFixture(t *testing.T, d *DB, ctx context.Context, opts activityFixtureOptions) Activity {
	t.Helper()
	if opts.Status == "" {
		opts.Status = ActivityStatusOpen
	}
	activityNo := "A-ACT-" + randomDigits(t, 10)
	a, err := scanActivity(d.pool.QueryRow(ctx,
		`INSERT INTO activities (legacy_record_id, activity_no, name, description, location,
		                        condition, max_signup, current_signup, fee, food, status,
		                        signin_enabled, signin_code, start_at)
		 VALUES ($1, $2, $3, '测试活动描述', '测试地点', '测试条件', $4, 0, '100', '自理',
		         $5, $6, $7, $8)
		 RETURNING `+activityColumns,
		activityNo+"-REC", activityNo, "活动 "+activityNo, opts.MaxSignup, opts.Status,
		opts.SigninEnabled, opts.SigninCode, opts.StartAt))
	if err != nil {
		t.Fatalf("create activity: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = d.pool.Exec(cleanupCtx, `DELETE FROM activities WHERE id = $1`, a.ID)
	})
	return a
}

func reloadActivity(t *testing.T, d *DB, ctx context.Context, id int64) Activity {
	t.Helper()
	a, err := scanActivity(d.pool.QueryRow(ctx,
		"SELECT "+activityColumns+" FROM activities WHERE id = $1", id))
	if err != nil {
		t.Fatalf("reload activity: %v", err)
	}
	return a
}

func TestIntegrationActivitySignupCancelLifecycle(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{MaxSignup: 2})
	user1 := createActivityFixtureUser(t, d, ctx, "甲")
	user2 := createActivityFixtureUser(t, d, ctx, "乙")
	user3 := createActivityFixtureUser(t, d, ctx, "丙")

	signup, err := d.SignupActivity(ctx, user1, activity.ActivityNo)
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if signup.Status != SignupStatusSigned || signup.Nickname != "甲" {
		t.Fatalf("signup = %+v", signup)
	}
	if got := reloadActivity(t, d, ctx, activity.ID); got.CurrentSignup != 1 || got.Status != ActivityStatusOpen {
		t.Fatalf("activity after first signup = %+v", got)
	}
	if _, err := d.SignupActivity(ctx, user1, activity.ActivityNo); !errors.Is(err, ErrAlreadySignedUp) {
		t.Fatalf("duplicate signup err = %v, want ErrAlreadySignedUp", err)
	}

	if _, err := d.SignupActivity(ctx, user2, activity.ActivityNo); err != nil {
		t.Fatalf("second signup: %v", err)
	}
	if got := reloadActivity(t, d, ctx, activity.ID); got.CurrentSignup != 2 || got.Status != ActivityStatusFull {
		t.Fatalf("activity after second signup = %+v", got)
	}
	if _, err := d.SignupActivity(ctx, user3, activity.ActivityNo); !errors.Is(err, ErrActivityFull) {
		t.Fatalf("full signup err = %v, want ErrActivityFull", err)
	}

	cancelled, err := d.CancelSignup(ctx, user1, activity.ActivityNo)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Signup.Status != SignupStatusCancelled {
		t.Fatalf("cancelled signup = %+v", cancelled.Signup)
	}
	if got := reloadActivity(t, d, ctx, activity.ID); got.CurrentSignup != 1 || got.Status != ActivityStatusOpen {
		t.Fatalf("activity after cancel = %+v", got)
	}
	if _, err := d.CancelSignup(ctx, user1, activity.ActivityNo); !errors.Is(err, ErrAlreadyCancelled) {
		t.Fatalf("double cancel err = %v, want ErrAlreadyCancelled", err)
	}
	if _, err := d.CancelSignup(ctx, user3, activity.ActivityNo); !errors.Is(err, ErrNotSignedUp) {
		t.Fatalf("cancel without signup err = %v, want ErrNotSignedUp", err)
	}

	mine1, err := d.ListMyActivities(ctx, user1)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine1) != 0 {
		t.Fatalf("cancelled activity still in my activities: %+v", mine1)
	}
	mine2, err := d.ListMyActivities(ctx, user2)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine2) != 1 || mine2[0].ID != activity.ID || mine2[0].SignupStatus != SignupStatusSigned {
		t.Fatalf("my activities = %+v", mine2)
	}

	// Re-signup after cancel reuses the unique row and reopens history.
	if _, err := d.SignupActivity(ctx, user1, activity.ActivityNo); err != nil {
		t.Fatalf("re-signup: %v", err)
	}
	if got := reloadActivity(t, d, ctx, activity.ID); got.CurrentSignup != 2 || got.Status != ActivityStatusFull {
		t.Fatalf("activity after re-signup = %+v", got)
	}
	roster, err := d.ListActivitySignups(ctx, activity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(roster) != 2 {
		t.Fatalf("roster = %+v", roster)
	}
	for _, row := range roster {
		if row.Nickname == "" || row.LegacyUserID == "" {
			t.Fatalf("roster row missing public fields: %+v", row)
		}
	}

	if _, err := d.pool.Exec(ctx,
		`UPDATE activities SET status = $2 WHERE id = $1`, activity.ID, ActivityStatusFinished); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CancelSignup(ctx, user2, activity.ActivityNo); !errors.Is(err, ErrActivityNotOpen) {
		t.Fatalf("cancel finished activity err = %v, want ErrActivityNotOpen", err)
	}
}

func TestIntegrationSignupRequiresActiveAccount(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{})
	user := createActivityFixtureUser(t, d, ctx, "癸")
	if _, err := d.pool.Exec(ctx,
		`UPDATE users SET account_status = 'pending' WHERE id = $1`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SignupActivity(ctx, user, activity.ActivityNo); !errors.Is(err, ErrAccountNotActive) {
		t.Fatalf("pending signup err = %v, want ErrAccountNotActive", err)
	}
}

func TestIntegrationActivityRefAndClosedSignup(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{Status: ActivityStatusNotStarted})
	user := createActivityFixtureUser(t, d, ctx, "丁")

	byLegacy, err := d.GetActivity(ctx, *activity.LegacyRecordID)
	if err != nil || byLegacy.ID != activity.ID {
		t.Fatalf("get by legacy record = %+v err=%v", byLegacy, err)
	}
	byNo, err := d.GetActivity(ctx, activity.ActivityNo)
	if err != nil || byNo.ID != activity.ID {
		t.Fatalf("get by activity no = %+v err=%v", byNo, err)
	}
	_, err = d.SignupActivity(ctx, user, activity.ActivityNo)
	if !errors.Is(err, ErrActivityNotOpen) {
		t.Fatalf("closed signup err = %v, want ErrActivityNotOpen", err)
	}
	var statusErr *ActivityStatusError
	if !errors.As(err, &statusErr) || statusErr.Status != ActivityStatusNotStarted {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestIntegrationSelfSigninRules(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	start := time.Now().Add(-time.Hour)
	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{
		SigninEnabled: true, SigninCode: "0123", StartAt: &start,
	})
	user := createActivityFixtureUser(t, d, ctx, "戊")
	if _, err := d.SignupActivity(ctx, user, activity.ActivityNo); err != nil {
		t.Fatal(err)
	}

	if _, _, err := d.SelfSignin(ctx, user, activity.ActivityNo, "9999"); !errors.Is(err, ErrSigninCodeInvalid) {
		t.Fatalf("wrong code err = %v, want ErrSigninCodeInvalid", err)
	}
	state, already, err := d.SelfSignin(ctx, user, activity.ActivityNo, "0123")
	if err != nil {
		t.Fatalf("self signin: %v", err)
	}
	if state != AttendanceOnTime || already {
		t.Fatalf("self signin state=%q already=%v", state, already)
	}
	state, already, err = d.SelfSignin(ctx, user, activity.ActivityNo, "0000")
	if err != nil || !already || state != AttendanceOnTime {
		t.Fatalf("idempotent self signin state=%q already=%v err=%v", state, already, err)
	}
	if !IsCheckedIn(string(state)) || IsCheckedIn(string(AttendanceAbsent)) {
		t.Fatal("IsCheckedIn mismatch")
	}

	other := createActivityFixtureUser(t, d, ctx, "己")
	if _, err := d.SignupActivity(ctx, other, activity.ActivityNo); err != nil {
		t.Fatal(err)
	}
	if _, err := d.pool.Exec(ctx,
		`UPDATE signups SET attendance = $3 WHERE activity_id = $1 AND user_id = $2`,
		activity.ID, other, string(AttendanceAbsent)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.SelfSignin(ctx, other, activity.ActivityNo, "0123"); !errors.Is(err, ErrMarkedAbsent) {
		t.Fatalf("absent overwrite err = %v, want ErrMarkedAbsent", err)
	}

	attendance, err := d.AttendanceByActivity(ctx, activity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if attendance[user] != AttendanceOnTime || attendance[other] != AttendanceAbsent {
		t.Fatalf("attendance map = %+v", attendance)
	}
	if one, err := d.GetAttendance(ctx, activity.ID, user); err != nil || one != AttendanceOnTime {
		t.Fatalf("get attendance = %q err=%v", one, err)
	}
}

func TestIntegrationSelfSigninGuards(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	noFlag := createActivityFixture(t, d, ctx, activityFixtureOptions{SigninCode: "0123"})
	noCode := createActivityFixture(t, d, ctx, activityFixtureOptions{SigninEnabled: true})
	user := createActivityFixtureUser(t, d, ctx, "庚")
	if _, err := d.SignupActivity(ctx, user, noFlag.ActivityNo); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SignupActivity(ctx, user, noCode.ActivityNo); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.SelfSignin(ctx, user, noFlag.ActivityNo, "0123"); !errors.Is(err, ErrSigninDisabled) {
		t.Fatalf("disabled err = %v, want ErrSigninDisabled", err)
	}
	if _, _, err := d.SelfSignin(ctx, user, noCode.ActivityNo, "0123"); !errors.Is(err, ErrSigninCodeNotSet) {
		t.Fatalf("no code err = %v, want ErrSigninCodeNotSet", err)
	}
	outsider := createActivityFixtureUser(t, d, ctx, "辛")
	if _, _, err := d.SelfSignin(ctx, outsider, noFlag.ActivityNo, "0123"); !errors.Is(err, ErrNotSignedUp) {
		t.Fatalf("not signed up err = %v, want ErrNotSignedUp", err)
	}
	if _, _, err := d.SelfSignin(ctx, user, "A-NOT-FOUND", "0123"); !errors.Is(err, ErrActivityNotFound) {
		t.Fatalf("missing activity err = %v, want ErrActivityNotFound", err)
	}
}

func TestIntegrationCancelRefundHook(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{})
	user := createActivityFixtureUser(t, d, ctx, "壬")
	if _, err := d.SignupActivity(ctx, user, activity.ActivityNo); err != nil {
		t.Fatal(err)
	}

	var seen SignupCancelInfo
	SignupCancelRefundHook = func(_ context.Context, info SignupCancelInfo) (int, error) {
		seen = info
		return 30, nil
	}
	t.Cleanup(func() { SignupCancelRefundHook = nil })

	result, err := d.CancelSignup(ctx, user, activity.ActivityNo)
	if err != nil {
		t.Fatal(err)
	}
	if result.RefundPoints != 30 || seen.ActivityNo != activity.ActivityNo || seen.UserID != user {
		t.Fatalf("hook result=%+v info=%+v", result, seen)
	}
}
