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
	PublishAt     *time.Time
	StartAt       *time.Time
	EndAt         *time.Time
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
		                        signin_enabled, signin_code, publish_at, start_at, end_at)
		 VALUES ($1, $2, $3, '测试活动描述', '测试地点', '测试条件', $4, 0, '100', '自理',
		         $5, $6, $7, $8, $9, $10)
		 RETURNING `+activityColumns,
		activityNo+"-REC", activityNo, "活动 "+activityNo, opts.MaxSignup, opts.Status,
		opts.SigninEnabled, opts.SigninCode, opts.PublishAt, opts.StartAt, opts.EndAt))
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

func TestIntegrationUserActivityOrderNewestPublishedEndedLast(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	now := time.Now()
	at := func(offset time.Duration) *time.Time {
		ts := now.Add(offset)
		return &ts
	}
	published := func(offset time.Duration, opts activityFixtureOptions) Activity {
		opts.PublishAt = at(offset)
		return createActivityFixture(t, d, ctx, opts)
	}

	endedByStatusNewest := published(0, activityFixtureOptions{Status: ActivityStatusFinished})
	activeNewest := published(-time.Hour, activityFixtureOptions{})
	endedByEndAt := published(-2*time.Hour, activityFixtureOptions{EndAt: at(-time.Hour)})
	activeOlder := published(-3*time.Hour, activityFixtureOptions{})
	notStartedNewest := published(-30*time.Minute, activityFixtureOptions{Status: ActivityStatusNotStarted})
	endedByStatusOldest := published(-5*time.Hour, activityFixtureOptions{Status: ActivityStatusFinished})

	want := []Activity{
		notStartedNewest, activeNewest, activeOlder,
		endedByStatusNewest, endedByEndAt, endedByStatusOldest,
	}
	assertOrder := func(label string, positions map[int64]int) {
		t.Helper()
		last := -1
		for _, act := range want {
			pos, ok := positions[act.ID]
			if !ok {
				t.Fatalf("%s: activity %d missing from list", label, act.ID)
			}
			if pos <= last {
				t.Fatalf("%s: activity %d at %d is not after previous %d (want %+v)",
					label, act.ID, pos, last, positions)
			}
			last = pos
		}
	}

	items, err := d.ListActivitiesForUser(ctx, createActivityFixtureUser(t, d, ctx, "排序"))
	if err != nil {
		t.Fatalf("list activities for user: %v", err)
	}
	positions := make(map[int64]int, len(items))
	for i, item := range items {
		positions[item.ID] = i
	}
	assertOrder("home list", positions)

	user := createActivityFixtureUser(t, d, ctx, "排序报名")
	for _, act := range want {
		if _, err := d.pool.Exec(ctx,
			`INSERT INTO signups (activity_id, user_id, nickname, status)
			 VALUES ($1, $2, $3, $4)`,
			act.ID, user, "排序", SignupStatusSigned); err != nil {
			t.Fatalf("seed signup: %v", err)
		}
	}
	mine, err := d.ListMyActivities(ctx, user)
	if err != nil {
		t.Fatalf("list my activities: %v", err)
	}
	minePositions := make(map[int64]int, len(mine))
	for i, item := range mine {
		minePositions[item.ID] = i
	}
	assertOrder("my activities", minePositions)
}

func TestIntegrationSelfSigninRules(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	start := time.Now().Add(-time.Hour)
	activity := createActivityFixture(t, d, ctx, activityFixtureOptions{
		SigninEnabled: true, SigninCode: "0123", StartAt: &start,
	})
	user := createActivityFixtureUser(t, d, ctx, "戊")
	// 活动已开始，报名入口已关闭；这里直接落一条报名记录以测试签到规则。
	seedSignup := func(userID int64, nickname string) {
		t.Helper()
		if _, err := d.pool.Exec(ctx,
			`INSERT INTO signups (activity_id, user_id, nickname, status)
			 VALUES ($1, $2, $3, $4)`,
			activity.ID, userID, nickname, SignupStatusSigned); err != nil {
			t.Fatalf("seed signup: %v", err)
		}
	}
	seedSignup(user, "戊")

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
	seedSignup(other, "己")
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

func TestIntegrationCancelRefundPriority(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	user := createActivityFixtureUser(t, d, ctx, "壬")
	pointsGrant(t, d, ctx, user, 100, "cancel-refund-priority")

	// More than 48 hours before the start: cancelling refunds the priority
	// order (there is no hook to inject any more; CancelSignup calls the points
	// store directly).
	farStart := time.Now().Add(72 * time.Hour)
	far := createActivityFixture(t, d, ctx, activityFixtureOptions{MaxSignup: 10, StartAt: &farStart})
	order, err := d.RedeemPoints(ctx, user, RedeemRequest{Item: RedeemItemPriority, ActivityID: far.ActivityNo})
	if err != nil {
		t.Fatalf("redeem priority: %v", err)
	}
	if _, err := d.SignupActivity(ctx, user, far.ActivityNo); err != nil {
		t.Fatal(err)
	}
	result, err := d.CancelSignup(ctx, user, far.ActivityNo)
	if err != nil {
		t.Fatal(err)
	}
	if result.RefundPoints != order.Cost {
		t.Fatalf("refund points = %d, want %d", result.RefundPoints, order.Cost)
	}
	reloaded, err := d.GetRedemption(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != RedeemStatusRefunded {
		t.Fatalf("priority order status = %q, want refunded", reloaded.Status)
	}
	if balance, err := d.PointsBalance(ctx, user); err != nil || balance != 100 {
		t.Fatalf("balance after refund = %d err=%v, want 100", balance, err)
	}

	// Less than 48 hours before the start: the order is closed without a
	// refund, but the slot is released because it no longer counts as active.
	soonStart := time.Now().Add(24 * time.Hour)
	soon := createActivityFixture(t, d, ctx, activityFixtureOptions{MaxSignup: 10, StartAt: &soonStart})
	soonOrder, err := d.RedeemPoints(ctx, user, RedeemRequest{Item: RedeemItemPriority, ActivityID: soon.ActivityNo})
	if err != nil {
		t.Fatalf("redeem priority (soon): %v", err)
	}
	if _, err := d.SignupActivity(ctx, user, soon.ActivityNo); err != nil {
		t.Fatal(err)
	}
	result, err = d.CancelSignup(ctx, user, soon.ActivityNo)
	if err != nil {
		t.Fatal(err)
	}
	if result.RefundPoints != 0 {
		t.Fatalf("refund points inside 48h = %d, want 0", result.RefundPoints)
	}
	closed, err := d.GetRedemption(ctx, soonOrder.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != RedeemStatusCancelled {
		t.Fatalf("priority order inside 48h = %q, want cancelled", closed.Status)
	}
	if balance, err := d.PointsBalance(ctx, user); err != nil || balance != 70 {
		t.Fatalf("balance after cancelled priority = %d err=%v, want 70", balance, err)
	}
}
