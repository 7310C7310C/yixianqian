package db

import (
	"errors"
	"testing"
	"time"
)

// TestIntegrationActivityTimeWindowRules covers audit UD-03: an activity whose
// status is still 报名中 but whose start time has passed must reject new
// signups and cancellations; self check-in only works inside the time window.
func TestIntegrationActivityTimeWindowRules(t *testing.T) {
	d, ctx := openActivitiesTestDB(t)
	past := time.Now().Add(-3 * time.Hour)
	recent := time.Now().Add(-1 * time.Hour)
	future := time.Now().Add(2 * time.Hour)
	farFuture := time.Now().Add(4 * time.Hour)

	user := createActivityFixtureUser(t, d, ctx, "时间窗甲")
	other := createActivityFixtureUser(t, d, ctx, "时间窗乙")

	started := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusOpen, MaxSignup: 10, StartAt: &past, EndAt: &future,
	})
	if _, err := d.SignupActivity(ctx, user, started.ActivityNo); !errors.Is(err, ErrActivityStarted) {
		t.Fatalf("signup on started activity err = %v, want ErrActivityStarted", err)
	}

	upcoming := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusOpen, MaxSignup: 10, StartAt: &future, EndAt: &farFuture,
	})
	if _, err := d.SignupActivity(ctx, user, upcoming.ActivityNo); err != nil {
		t.Fatalf("signup before start: %v", err)
	}
	if _, err := d.CancelSignup(ctx, user, upcoming.ActivityNo); err != nil {
		t.Fatalf("cancel before start: %v", err)
	}

	// A row that was created before the start time cannot be cancelled after
	// the activity began.
	if _, err := d.pool.Exec(ctx,
		`INSERT INTO signups (activity_id, user_id, nickname, status) VALUES ($1, $2, $3, $4)`,
		started.ID, other, "时间窗乙", SignupStatusSigned); err != nil {
		t.Fatalf("seed started signup: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.pool.Exec(ctx, `DELETE FROM signups WHERE activity_id = $1 AND user_id = $2`,
			started.ID, other)
	})
	if _, err := d.CancelSignup(ctx, other, started.ActivityNo); !errors.Is(err, ErrActivityStarted) {
		t.Fatalf("cancel after start err = %v, want ErrActivityStarted", err)
	}

	// Self sign-in: not started yet, then ended.
	signinUpcoming := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusOpen, SigninEnabled: true, SigninCode: "0123",
		StartAt: &future, EndAt: &farFuture,
	})
	if _, err := d.pool.Exec(ctx,
		`INSERT INTO signups (activity_id, user_id, nickname, status) VALUES ($1, $2, $3, $4)`,
		signinUpcoming.ID, user, "时间窗甲", SignupStatusSigned); err != nil {
		t.Fatalf("seed upcoming signup: %v", err)
	}
	if _, _, err := d.SelfSignin(ctx, user, signinUpcoming.ActivityNo, "0123"); !errors.Is(err, ErrActivityNotStarted) {
		t.Fatalf("signin before start err = %v, want ErrActivityNotStarted", err)
	}

	signinEnded := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusOpen, SigninEnabled: true, SigninCode: "0123",
		StartAt: &past, EndAt: &recent,
	})
	if _, err := d.pool.Exec(ctx,
		`INSERT INTO signups (activity_id, user_id, nickname, status) VALUES ($1, $2, $3, $4)`,
		signinEnded.ID, user, "时间窗甲", SignupStatusSigned); err != nil {
		t.Fatalf("seed ended signup: %v", err)
	}
	if _, _, err := d.SelfSignin(ctx, user, signinEnded.ActivityNo, "0123"); !errors.Is(err, ErrActivityEnded) {
		t.Fatalf("signin after end err = %v, want ErrActivityEnded", err)
	}

	// Inside the window the published code works and is idempotent.
	signinLive := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusOpen, SigninEnabled: true, SigninCode: "0123",
		StartAt: &past, EndAt: &future,
	})
	if _, err := d.pool.Exec(ctx,
		`INSERT INTO signups (activity_id, user_id, nickname, status) VALUES ($1, $2, $3, $4)`,
		signinLive.ID, user, "时间窗甲", SignupStatusSigned); err != nil {
		t.Fatalf("seed live signup: %v", err)
	}
	attendance, already, err := d.SelfSignin(ctx, user, signinLive.ActivityNo, "0123")
	if err != nil || already || attendance != AttendanceOnTime {
		t.Fatalf("signin inside window = %q already=%v err=%v", attendance, already, err)
	}
	attendance, already, err = d.SelfSignin(ctx, user, signinLive.ActivityNo, "0123")
	if err != nil || !already || attendance != AttendanceOnTime {
		t.Fatalf("repeat signin = %q already=%v err=%v", attendance, already, err)
	}

	// A finished activity rejects check-in even without a start/end time.
	finished := createActivityFixture(t, d, ctx, activityFixtureOptions{
		Status: ActivityStatusFinished, SigninEnabled: true, SigninCode: "0123",
	})
	if _, err := d.pool.Exec(ctx,
		`INSERT INTO signups (activity_id, user_id, nickname, status) VALUES ($1, $2, $3, $4)`,
		finished.ID, other, "时间窗乙", SignupStatusSigned); err != nil {
		t.Fatalf("seed finished signup: %v", err)
	}
	if _, _, err := d.SelfSignin(ctx, other, finished.ActivityNo, "0123"); !errors.Is(err, ErrActivityEnded) {
		t.Fatalf("signin on finished activity err = %v, want ErrActivityEnded", err)
	}
}
